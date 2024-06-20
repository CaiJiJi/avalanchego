// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/ava-labs/avalanchego/chains/atomic"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/utils/timer/mockable"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/platformvm/block"
	"github.com/ava-labs/avalanchego/vms/platformvm/config"
	"github.com/ava-labs/avalanchego/vms/platformvm/reward"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/platformvm/status"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs/executor"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs/fee"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs/mempool"
	"github.com/ava-labs/avalanchego/vms/platformvm/upgrade"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/subnet/primary/common"

	commonfee "github.com/ava-labs/avalanchego/vms/components/fee"
	walletsigner "github.com/ava-labs/avalanchego/wallet/chain/p/signer"
)

// Check that gas consumed by a standard blocks is duly calculated once block is verified
// Only transactions active post E upgrade are considered and tested pre and post E upgrade.
func TestStandardBlockGas(t *testing.T) {
	type test struct {
		name      string
		setupTest func(env *environment) *txs.Tx
	}

	tests := []test{
		{
			name: "AddPermissionlessValidatorTx",
			setupTest: func(env *environment) *txs.Tx {
				var (
					nodeID    = ids.GenerateTestNodeID()
					chainTime = env.state.GetTimestamp()
					endTime   = chainTime.Add(defaultMaxStakingDuration)
				)
				sk, err := bls.NewSecretKey()
				require.NoError(t, err)

				builder, s, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewAddPermissionlessValidatorTx(
					&txs.SubnetValidator{
						Validator: txs.Validator{
							NodeID: nodeID,
							End:    uint64(endTime.Unix()),
							Wght:   env.config.MinValidatorStake,
						},
						Subnet: constants.PrimaryNetworkID,
					},
					signer.NewProofOfPossession(sk),
					env.ctx.AVAXAssetID,
					&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs:     []ids.ShortID{ids.ShortEmpty},
					},
					&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs:     []ids.ShortID{ids.ShortEmpty},
					},
					reward.PercentDenominator,
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), s, utx)
				require.NoError(t, err)

				return tx
			},
		},
		{
			name: "AddPermissionlessDelegatorTx",
			setupTest: func(env *environment) *txs.Tx {
				var primaryValidator *state.Staker
				it, err := env.state.GetCurrentStakerIterator()
				require.NoError(t, err)
				for it.Next() {
					staker := it.Value()
					if staker.Priority != txs.PrimaryNetworkValidatorCurrentPriority {
						continue
					}
					primaryValidator = staker
					break
				}
				it.Release()

				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewAddPermissionlessDelegatorTx(
					&txs.SubnetValidator{
						Validator: txs.Validator{
							NodeID: primaryValidator.NodeID,
							End:    uint64(primaryValidator.EndTime.Unix()),
							Wght:   env.config.MinDelegatorStake,
						},
						Subnet: constants.PrimaryNetworkID,
					},
					env.ctx.AVAXAssetID,
					&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs:     []ids.ShortID{ids.ShortEmpty},
					},
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)

				return tx
			},
		},
		{
			name: "AddSubnetValidatorTx",
			setupTest: func(env *environment) *txs.Tx {
				var primaryValidator *state.Staker
				it, err := env.state.GetCurrentStakerIterator()
				require.NoError(t, err)
				for it.Next() {
					staker := it.Value()
					if staker.Priority != txs.PrimaryNetworkValidatorCurrentPriority {
						continue
					}
					primaryValidator = staker
					break
				}
				it.Release()

				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewAddSubnetValidatorTx(
					&txs.SubnetValidator{
						Validator: txs.Validator{
							NodeID: primaryValidator.NodeID,
							End:    uint64(primaryValidator.EndTime.Unix()),
							Wght:   defaultMinValidatorStake,
						},
						Subnet: testSubnet1.TxID,
					},
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)

				return tx
			},
		},
		{
			name: "CreateChainTx",
			setupTest: func(env *environment) *txs.Tx {
				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewCreateChainTx(
					testSubnet1.TxID,
					[]byte{},             // genesisData
					ids.GenerateTestID(), // vmID
					[]ids.ID{},           // fxIDs
					"aaa",                // chain name
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)
				return tx
			},
		},
		{
			name: "CreateSubnetTx",
			setupTest: func(env *environment) *txs.Tx {
				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewCreateSubnetTx(
					&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs:     []ids.ShortID{ids.GenerateTestShortID()},
					},
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)
				return tx
			},
		},
		{
			name: "RemoveSubnetValidatorTx",
			setupTest: func(env *environment) *txs.Tx {
				var primaryValidator *state.Staker
				it, err := env.state.GetCurrentStakerIterator()
				require.NoError(t, err)
				for it.Next() {
					staker := it.Value()
					if staker.Priority != txs.PrimaryNetworkValidatorCurrentPriority {
						continue
					}
					primaryValidator = staker
					break
				}
				it.Release()

				endTime := primaryValidator.EndTime

				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				uSubnetValTx, err := builder.NewAddSubnetValidatorTx(
					&txs.SubnetValidator{
						Validator: txs.Validator{
							NodeID: primaryValidator.NodeID,
							Start:  0,
							End:    uint64(endTime.Unix()),
							Wght:   defaultWeight,
						},
						Subnet: testSubnet1.ID(),
					},
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				subnetValTx, err := walletsigner.SignUnsigned(context.Background(), signer, uSubnetValTx)
				require.NoError(t, err)

				onAcceptState, err := state.NewDiffOn(env.state)
				require.NoError(t, err)

				feeCalculator, err := state.PickFeeCalculator(env.config, onAcceptState, onAcceptState.GetTimestamp())
				require.NoError(t, err)

				require.NoError(t, subnetValTx.Unsigned.Visit(&executor.StandardTxExecutor{
					Backend:       env.backend,
					State:         onAcceptState,
					FeeCalculator: feeCalculator,
					Tx:            subnetValTx,
				}))

				require.NoError(t, onAcceptState.Apply(env.state))
				require.NoError(t, env.state.Commit())

				utx, err := builder.NewRemoveSubnetValidatorTx(
					primaryValidator.NodeID,
					testSubnet1.ID(),
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)

				return tx
			},
		},
		{
			name: "TransformSubnetTx",
			setupTest: func(env *environment) *txs.Tx {
				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewTransformSubnetTx(
					testSubnet1.TxID,          // subnetID
					ids.GenerateTestID(),      // assetID
					10,                        // initial supply
					10,                        // max supply
					0,                         // min consumption rate
					reward.PercentDenominator, // max consumption rate
					2,                         // min validator stake
					10,                        // max validator stake
					time.Minute,               // min stake duration
					time.Hour,                 // max stake duration
					1,                         // min delegation fees
					10,                        // min delegator stake
					1,                         // max validator weight factor
					80,                        // uptime requirement
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)
				return tx
			},
		},
		{
			name: "TransferSubnetOwnershipTx",
			setupTest: func(env *environment) *txs.Tx {
				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewTransferSubnetOwnershipTx(
					testSubnet1.TxID,
					&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs:     []ids.ShortID{ids.ShortEmpty},
					},
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)

				return tx
			},
		},
		{
			name: "ImportTx",
			setupTest: func(env *environment) *txs.Tx {
				// Skip shared memory checks
				env.backend.Bootstrapped.Set(false)

				var (
					sourceChain  = env.ctx.XChainID
					sourceKey    = preFundedKeys[1]
					sourceAmount = 10 * units.Avax
				)

				sharedMemory := fundedSharedMemory(
					t,
					env,
					sourceKey,
					sourceChain,
					map[ids.ID]uint64{
						env.ctx.AVAXAssetID: sourceAmount,
					},
				)
				env.msm.SharedMemory = sharedMemory

				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewImportTx(
					sourceChain,
					&secp256k1fx.OutputOwners{
						Locktime:  0,
						Threshold: 1,
						Addrs:     []ids.ShortID{sourceKey.PublicKey().Address()},
					},
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)

				// reactivate checks
				env.backend.Bootstrapped.Set(true)
				return tx
			},
		},
		{
			name: "ExportTx",
			setupTest: func(env *environment) *txs.Tx {
				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewExportTx(
					env.ctx.XChainID,
					[]*avax.TransferableOutput{{
						Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
						Out: &secp256k1fx.TransferOutput{
							Amt: units.Avax,
							OutputOwners: secp256k1fx.OutputOwners{
								Locktime:  0,
								Threshold: 1,
								Addrs:     []ids.ShortID{ids.GenerateTestShortID()},
							},
						},
					}},
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)

				return tx
			},
		},
		{
			name: "BaseTx",
			setupTest: func(env *environment) *txs.Tx {
				builder, signer, feeCalc, err := env.factory.NewWallet(preFundedKeys...)
				require.NoError(t, err)
				utx, err := builder.NewBaseTx(
					[]*avax.TransferableOutput{
						{
							Asset: avax.Asset{ID: env.ctx.AVAXAssetID},
							Out: &secp256k1fx.TransferOutput{
								Amt: 1,
								OutputOwners: secp256k1fx.OutputOwners{
									Threshold: 1,
									Addrs:     []ids.ShortID{ids.ShortEmpty},
								},
							},
						},
					},
					feeCalc,
					common.WithChangeOwner(&secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs: []ids.ShortID{
							preFundedKeys[0].PublicKey().Address(),
						},
					}),
				)
				require.NoError(t, err)
				tx, err := walletsigner.SignUnsigned(context.Background(), signer, utx)
				require.NoError(t, err)
				return tx
			},
		},
	}

	for _, tt := range tests {
		for _, dynamicFeesActive := range []bool{false, true} {
			t.Run(tt.name, func(t *testing.T) {
				require := require.New(t)

				f := latestFork
				if !dynamicFeesActive {
					f = durango
				}
				env := newEnvironment(t, nil, f)
				env.ctx.Lock.Lock()
				defer env.ctx.Lock.Unlock()

				tx := tt.setupTest(env)

				nextBlkTime, _, err := state.NextBlockTime(env.state, env.clk)
				require.NoError(err)

				parentBlkID := env.state.GetLastAccepted()
				parentBlk, err := env.state.GetStatelessBlock(parentBlkID)
				require.NoError(err)

				statelessBlk, err := block.NewBanffStandardBlock(
					nextBlkTime,
					parentBlkID,
					parentBlk.Height()+1,
					[]*txs.Tx{tx},
				)
				require.NoError(err)

				blk := env.blkManager.NewBlock(statelessBlk)
				require.NoError(blk.Verify(context.Background()))

				// check that metered complexity is non-zero post E upgrade and zero pre E upgrade
				blkState, found := env.blkManager.(*manager).blkIDToState[blk.ID()]
				require.True(found)

				if dynamicFeesActive {
					require.NotEqual(commonfee.ZeroGas, blkState.blockGas)

					gasCap, err := blkState.onAcceptState.GetCurrentGasCap()
					require.NoError(err)
					require.Greater(gasCap, commonfee.ZeroGas)

					feeCfg, err := fee.GetDynamicConfig(dynamicFeesActive)
					require.NoError(err)
					require.Less(gasCap, feeCfg.MaxGasPerSecond)
				} else {
					require.Equal(commonfee.ZeroGas, blkState.blockGas)

					// GasCap unchanged wrt parent state
					parentGasCap, err := env.state.GetCurrentGasCap()
					require.NoError(err)

					gasCap, err := blkState.onAcceptState.GetCurrentGasCap()
					require.NoError(err)
					require.Equal(parentGasCap, gasCap)
				}
			})
		}
	}
}

func TestVerifierVisitProposalBlock(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	s := state.NewMockState(ctrl)
	mempool := mempool.NewMockMempool(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	parentOnAcceptState := state.NewMockDiff(ctrl)
	timestamp := time.Now()
	// One call for each of onCommitState and onAbortState.
	parentOnAcceptState.EXPECT().GetTimestamp().Return(timestamp).Times(2)
	parentOnAcceptState.EXPECT().GetCurrentGasCap().Return(commonfee.Gas(1_000_000), nil)

	backend := &backend{
		lastAccepted: parentID,
		blkIDToState: map[ids.ID]*blockState{
			parentID: {
				statelessBlock: parentStatelessBlk,
				onAcceptState:  parentOnAcceptState,
			},
		},
		Mempool: mempool,
		state:   s,
		ctx: &snow.Context{
			Log: logging.NoLog{},
		},
	}
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: mockable.MaxTime, // banff is not activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: backend,
	}
	manager := &manager{
		backend:  backend,
		verifier: verifier,
	}

	blkTx := txs.NewMockUnsignedTx(ctrl)
	blkTx.EXPECT().Visit(gomock.AssignableToTypeOf(&executor.ProposalTxExecutor{})).Return(nil).Times(1)

	// We can't serialize [blkTx] because it isn't
	// registered with the blocks.Codec.
	// Serialize this block with a dummy tx
	// and replace it after creation with the mock tx.
	// TODO allow serialization of mock txs.
	apricotBlk, err := block.NewApricotProposalBlock(
		parentID,
		2,
		&txs.Tx{
			Unsigned: &txs.AdvanceTimeTx{},
			Creds:    []verify.Verifiable{},
		},
	)
	require.NoError(err)
	apricotBlk.Tx.Unsigned = blkTx

	// Set expectations for dependencies.
	tx := apricotBlk.Txs()[0]
	parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)
	mempool.EXPECT().Remove([]*txs.Tx{tx}).Times(1)

	// Visit the block
	blk := manager.NewBlock(apricotBlk)
	require.NoError(blk.Verify(context.Background()))
	require.Contains(verifier.backend.blkIDToState, apricotBlk.ID())
	gotBlkState := verifier.backend.blkIDToState[apricotBlk.ID()]
	require.Equal(apricotBlk, gotBlkState.statelessBlock)
	require.Equal(timestamp, gotBlkState.timestamp)

	// Assert that the expected tx statuses are set.
	_, gotStatus, err := gotBlkState.onCommitState.GetTx(tx.ID())
	require.NoError(err)
	require.Equal(status.Committed, gotStatus)

	_, gotStatus, err = gotBlkState.onAbortState.GetTx(tx.ID())
	require.NoError(err)
	require.Equal(status.Aborted, gotStatus)

	// Visiting again should return nil without using dependencies.
	require.NoError(blk.Verify(context.Background()))
}

func TestVerifierVisitAtomicBlock(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	mempool := mempool.NewMockMempool(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	grandparentID := ids.GenerateTestID()
	parentState := state.NewMockDiff(ctrl)

	backend := &backend{
		blkIDToState: map[ids.ID]*blockState{
			parentID: {
				statelessBlock: parentStatelessBlk,
				onAcceptState:  parentState,
			},
		},
		Mempool: mempool,
		state:   s,
		ctx: &snow.Context{
			Log: logging.NoLog{},
		},
	}
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					ApricotPhase5Time: time.Now().Add(time.Hour),
					BanffTime:         mockable.MaxTime, // banff is not activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: backend,
	}
	manager := &manager{
		backend:  backend,
		verifier: verifier,
	}

	onAccept := state.NewMockDiff(ctrl)
	blkTx := txs.NewMockUnsignedTx(ctrl)
	inputs := set.Of(ids.GenerateTestID())
	blkTx.EXPECT().Visit(gomock.AssignableToTypeOf(&executor.AtomicTxExecutor{})).DoAndReturn(
		func(e *executor.AtomicTxExecutor) error {
			e.OnAccept = onAccept
			e.Inputs = inputs
			return nil
		},
	).Times(1)

	// We can't serialize [blkTx] because it isn't registered with blocks.Codec.
	// Serialize this block with a dummy tx and replace it after creation with
	// the mock tx.
	// TODO allow serialization of mock txs.
	apricotBlk, err := block.NewApricotAtomicBlock(
		parentID,
		2,
		&txs.Tx{
			Unsigned: &txs.AdvanceTimeTx{},
			Creds:    []verify.Verifiable{},
		},
	)
	require.NoError(err)
	apricotBlk.Tx.Unsigned = blkTx

	// Set expectations for dependencies.
	timestamp := time.Now()
	parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)
	parentStatelessBlk.EXPECT().Parent().Return(grandparentID).Times(1)
	mempool.EXPECT().Remove([]*txs.Tx{apricotBlk.Tx}).Times(1)
	onAccept.EXPECT().AddTx(apricotBlk.Tx, status.Committed).Times(1)
	onAccept.EXPECT().GetTimestamp().Return(timestamp).Times(1)

	blk := manager.NewBlock(apricotBlk)
	require.NoError(blk.Verify(context.Background()))

	require.Contains(verifier.backend.blkIDToState, apricotBlk.ID())
	gotBlkState := verifier.backend.blkIDToState[apricotBlk.ID()]
	require.Equal(apricotBlk, gotBlkState.statelessBlock)
	require.Equal(onAccept, gotBlkState.onAcceptState)
	require.Equal(inputs, gotBlkState.inputs)
	require.Equal(timestamp, gotBlkState.timestamp)

	// Visiting again should return nil without using dependencies.
	require.NoError(blk.Verify(context.Background()))
}

func TestVerifierVisitStandardBlock(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	mempool := mempool.NewMockMempool(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	parentState := state.NewMockDiff(ctrl)

	backend := &backend{
		blkIDToState: map[ids.ID]*blockState{
			parentID: {
				statelessBlock: parentStatelessBlk,
				onAcceptState:  parentState,
			},
		},
		Mempool: mempool,
		state:   s,
		ctx: &snow.Context{
			Log: logging.NoLog{},
		},
	}
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					ApricotPhase5Time: time.Now().Add(time.Hour),
					BanffTime:         mockable.MaxTime, // banff is not activated
					CortinaTime:       mockable.MaxTime,
					DurangoTime:       mockable.MaxTime,
					EUpgradeTime:      mockable.MaxTime,
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: backend,
	}
	manager := &manager{
		backend:  backend,
		verifier: verifier,
	}

	blkTx := txs.NewMockUnsignedTx(ctrl)
	atomicRequests := map[ids.ID]*atomic.Requests{
		ids.GenerateTestID(): {
			RemoveRequests: [][]byte{{1}, {2}},
			PutRequests: []*atomic.Element{
				{
					Key:    []byte{3},
					Value:  []byte{4},
					Traits: [][]byte{{5}, {6}},
				},
			},
		},
	}
	blkTx.EXPECT().Visit(gomock.AssignableToTypeOf(&executor.StandardTxExecutor{})).DoAndReturn(
		func(e *executor.StandardTxExecutor) error {
			e.OnAccept = func() {}
			e.Inputs = set.Set[ids.ID]{}
			e.AtomicRequests = atomicRequests
			return nil
		},
	).Times(1)

	// We can't serialize [blkTx] because it isn't
	// registered with the blocks.Codec.
	// Serialize this block with a dummy tx
	// and replace it after creation with the mock tx.
	// TODO allow serialization of mock txs.
	apricotBlk, err := block.NewApricotStandardBlock(
		parentID,
		2, /*height*/
		[]*txs.Tx{
			{
				Unsigned: &txs.AdvanceTimeTx{},
				Creds:    []verify.Verifiable{},
			},
		},
	)
	require.NoError(err)
	apricotBlk.Transactions[0].Unsigned = blkTx

	// Set expectations for dependencies.
	timestamp := time.Now()
	parentState.EXPECT().GetTimestamp().Return(timestamp)
	parentState.EXPECT().GetCurrentGasCap().Return(commonfee.Gas(1_000_000), nil)
	parentStatelessBlk.EXPECT().Height().Return(uint64(1))
	mempool.EXPECT().Remove(apricotBlk.Txs()).Times(1)

	blk := manager.NewBlock(apricotBlk)
	require.NoError(blk.Verify(context.Background()))

	// Assert expected state.
	require.Contains(verifier.backend.blkIDToState, apricotBlk.ID())
	gotBlkState := verifier.backend.blkIDToState[apricotBlk.ID()]
	require.Equal(apricotBlk, gotBlkState.statelessBlock)
	require.Equal(set.Set[ids.ID]{}, gotBlkState.inputs)
	require.Equal(timestamp, gotBlkState.timestamp)

	// Visiting again should return nil without using dependencies.
	require.NoError(blk.Verify(context.Background()))
}

func TestVerifierVisitCommitBlock(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	mempool := mempool.NewMockMempool(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	parentOnDecisionState := state.NewMockDiff(ctrl)
	parentOnCommitState := state.NewMockDiff(ctrl)
	parentOnAbortState := state.NewMockDiff(ctrl)

	backend := &backend{
		blkIDToState: map[ids.ID]*blockState{
			parentID: {
				statelessBlock: parentStatelessBlk,
				proposalBlockState: proposalBlockState{
					onDecisionState: parentOnDecisionState,
					onCommitState:   parentOnCommitState,
					onAbortState:    parentOnAbortState,
				},
			},
		},
		Mempool: mempool,
		state:   s,
		ctx: &snow.Context{
			Log: logging.NoLog{},
		},
	}
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: mockable.MaxTime, // banff is not activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: backend,
	}
	manager := &manager{
		backend:  backend,
		verifier: verifier,
	}

	apricotBlk, err := block.NewApricotCommitBlock(
		parentID,
		2,
	)
	require.NoError(err)

	// Set expectations for dependencies.
	timestamp := time.Now()
	gomock.InOrder(
		parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1),
		parentOnCommitState.EXPECT().GetTimestamp().Return(timestamp).Times(1),
	)

	// Verify the block.
	blk := manager.NewBlock(apricotBlk)
	require.NoError(blk.Verify(context.Background()))

	// Assert expected state.
	require.Contains(verifier.backend.blkIDToState, apricotBlk.ID())
	gotBlkState := verifier.backend.blkIDToState[apricotBlk.ID()]
	require.Equal(parentOnAbortState, gotBlkState.onAcceptState)
	require.Equal(timestamp, gotBlkState.timestamp)

	// Visiting again should return nil without using dependencies.
	require.NoError(blk.Verify(context.Background()))
}

func TestVerifierVisitAbortBlock(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	mempool := mempool.NewMockMempool(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	parentOnDecisionState := state.NewMockDiff(ctrl)
	parentOnCommitState := state.NewMockDiff(ctrl)
	parentOnAbortState := state.NewMockDiff(ctrl)

	backend := &backend{
		blkIDToState: map[ids.ID]*blockState{
			parentID: {
				statelessBlock: parentStatelessBlk,
				proposalBlockState: proposalBlockState{
					onDecisionState: parentOnDecisionState,
					onCommitState:   parentOnCommitState,
					onAbortState:    parentOnAbortState,
				},
			},
		},
		Mempool: mempool,
		state:   s,
		ctx: &snow.Context{
			Log: logging.NoLog{},
		},
	}
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: mockable.MaxTime, // banff is not activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: backend,
	}
	manager := &manager{
		backend:  backend,
		verifier: verifier,
	}

	apricotBlk, err := block.NewApricotAbortBlock(
		parentID,
		2,
	)
	require.NoError(err)

	// Set expectations for dependencies.
	timestamp := time.Now()
	gomock.InOrder(
		parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1),
		parentOnAbortState.EXPECT().GetTimestamp().Return(timestamp).Times(1),
	)

	// Verify the block.
	blk := manager.NewBlock(apricotBlk)
	require.NoError(blk.Verify(context.Background()))

	// Assert expected state.
	require.Contains(verifier.backend.blkIDToState, apricotBlk.ID())
	gotBlkState := verifier.backend.blkIDToState[apricotBlk.ID()]
	require.Equal(parentOnAbortState, gotBlkState.onAcceptState)
	require.Equal(timestamp, gotBlkState.timestamp)

	// Visiting again should return nil without using dependencies.
	require.NoError(blk.Verify(context.Background()))
}

// Assert that a block with an unverified parent fails verification.
func TestVerifyUnverifiedParent(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	mempool := mempool.NewMockMempool(ctrl)
	parentID := ids.GenerateTestID()

	backend := &backend{
		blkIDToState: map[ids.ID]*blockState{},
		Mempool:      mempool,
		state:        s,
		ctx: &snow.Context{
			Log: logging.NoLog{},
		},
	}
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: mockable.MaxTime, // banff is not activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: backend,
	}

	blk, err := block.NewApricotAbortBlock(parentID /*not in memory or persisted state*/, 2 /*height*/)
	require.NoError(err)

	// Set expectations for dependencies.
	s.EXPECT().GetTimestamp().Return(time.Now()).Times(1)
	s.EXPECT().GetStatelessBlock(parentID).Return(nil, database.ErrNotFound).Times(1)

	// Verify the block.
	err = blk.Visit(verifier)
	require.ErrorIs(err, database.ErrNotFound)
}

func TestBanffAbortBlockTimestampChecks(t *testing.T) {
	ctrl := gomock.NewController(t)

	now := defaultGenesisTime.Add(time.Hour)

	tests := []struct {
		description string
		parentTime  time.Time
		childTime   time.Time
		result      error
	}{
		{
			description: "abort block timestamp matching parent's one",
			parentTime:  now,
			childTime:   now,
			result:      nil,
		},
		{
			description: "abort block timestamp before parent's one",
			childTime:   now.Add(-1 * time.Second),
			parentTime:  now,
			result:      errOptionBlockTimestampNotMatchingParent,
		},
		{
			description: "abort block timestamp after parent's one",
			parentTime:  now,
			childTime:   now.Add(time.Second),
			result:      errOptionBlockTimestampNotMatchingParent,
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			require := require.New(t)

			// Create mocked dependencies.
			s := state.NewMockState(ctrl)
			mempool := mempool.NewMockMempool(ctrl)
			parentID := ids.GenerateTestID()
			parentStatelessBlk := block.NewMockBlock(ctrl)
			parentHeight := uint64(1)

			backend := &backend{
				blkIDToState: make(map[ids.ID]*blockState),
				Mempool:      mempool,
				state:        s,
				ctx: &snow.Context{
					Log: logging.NoLog{},
				},
			}
			verifier := &verifier{
				txExecutorBackend: &executor.Backend{
					Config: &config.Config{
						UpgradeConfig: upgrade.Config{
							BanffTime: time.Time{}, // banff is activated
						},
					},
					Clk: &mockable.Clock{},
				},
				backend: backend,
			}

			// build and verify child block
			childHeight := parentHeight + 1
			statelessAbortBlk, err := block.NewBanffAbortBlock(test.childTime, parentID, childHeight)
			require.NoError(err)

			// setup parent state
			parentTime := defaultGenesisTime
			s.EXPECT().GetLastAccepted().Return(parentID).Times(3)
			s.EXPECT().GetTimestamp().Return(parentTime).Times(3)

			onDecisionState, err := state.NewDiff(parentID, backend)
			require.NoError(err)
			onCommitState, err := state.NewDiff(parentID, backend)
			require.NoError(err)
			onAbortState, err := state.NewDiff(parentID, backend)
			require.NoError(err)
			backend.blkIDToState[parentID] = &blockState{
				timestamp:      test.parentTime,
				statelessBlock: parentStatelessBlk,
				proposalBlockState: proposalBlockState{
					onDecisionState: onDecisionState,
					onCommitState:   onCommitState,
					onAbortState:    onAbortState,
				},
			}

			// Set expectations for dependencies.
			parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)

			err = statelessAbortBlk.Visit(verifier)
			require.ErrorIs(err, test.result)
		})
	}
}

// TODO combine with TestApricotCommitBlockTimestampChecks
func TestBanffCommitBlockTimestampChecks(t *testing.T) {
	ctrl := gomock.NewController(t)

	now := defaultGenesisTime.Add(time.Hour)

	tests := []struct {
		description string
		parentTime  time.Time
		childTime   time.Time
		result      error
	}{
		{
			description: "commit block timestamp matching parent's one",
			parentTime:  now,
			childTime:   now,
			result:      nil,
		},
		{
			description: "commit block timestamp before parent's one",
			childTime:   now.Add(-1 * time.Second),
			parentTime:  now,
			result:      errOptionBlockTimestampNotMatchingParent,
		},
		{
			description: "commit block timestamp after parent's one",
			parentTime:  now,
			childTime:   now.Add(time.Second),
			result:      errOptionBlockTimestampNotMatchingParent,
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			require := require.New(t)

			// Create mocked dependencies.
			s := state.NewMockState(ctrl)
			mempool := mempool.NewMockMempool(ctrl)
			parentID := ids.GenerateTestID()
			parentStatelessBlk := block.NewMockBlock(ctrl)
			parentHeight := uint64(1)

			backend := &backend{
				blkIDToState: make(map[ids.ID]*blockState),
				Mempool:      mempool,
				state:        s,
				ctx: &snow.Context{
					Log: logging.NoLog{},
				},
			}
			verifier := &verifier{
				txExecutorBackend: &executor.Backend{
					Config: &config.Config{
						UpgradeConfig: upgrade.Config{
							BanffTime: time.Time{}, // banff is activated
						},
					},
					Clk: &mockable.Clock{},
				},
				backend: backend,
			}

			// build and verify child block
			childHeight := parentHeight + 1
			statelessCommitBlk, err := block.NewBanffCommitBlock(test.childTime, parentID, childHeight)
			require.NoError(err)

			// setup parent state
			parentTime := defaultGenesisTime
			s.EXPECT().GetLastAccepted().Return(parentID).Times(3)
			s.EXPECT().GetTimestamp().Return(parentTime).Times(3)

			onDecisionState, err := state.NewDiff(parentID, backend)
			require.NoError(err)
			onCommitState, err := state.NewDiff(parentID, backend)
			require.NoError(err)
			onAbortState, err := state.NewDiff(parentID, backend)
			require.NoError(err)
			backend.blkIDToState[parentID] = &blockState{
				timestamp:      test.parentTime,
				statelessBlock: parentStatelessBlk,
				proposalBlockState: proposalBlockState{
					onDecisionState: onDecisionState,
					onCommitState:   onCommitState,
					onAbortState:    onAbortState,
				},
			}

			// Set expectations for dependencies.
			parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)

			err = statelessCommitBlk.Visit(verifier)
			require.ErrorIs(err, test.result)
		})
	}
}

func TestVerifierVisitStandardBlockWithDuplicateInputs(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	mempool := mempool.NewMockMempool(ctrl)

	grandParentID := ids.GenerateTestID()
	grandParentStatelessBlk := block.NewMockBlock(ctrl)
	grandParentState := state.NewMockDiff(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	parentState := state.NewMockDiff(ctrl)
	atomicInputs := set.Of(ids.GenerateTestID())

	backend := &backend{
		blkIDToState: map[ids.ID]*blockState{
			grandParentID: {
				statelessBlock: grandParentStatelessBlk,
				onAcceptState:  grandParentState,
				inputs:         atomicInputs,
			},
			parentID: {
				statelessBlock: parentStatelessBlk,
				onAcceptState:  parentState,
			},
		},
		Mempool: mempool,
		state:   s,
		ctx: &snow.Context{
			Log: logging.NoLog{},
		},
	}
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					ApricotPhase5Time: time.Now().Add(time.Hour),
					BanffTime:         mockable.MaxTime, // banff is not activated
					CortinaTime:       mockable.MaxTime,
					DurangoTime:       mockable.MaxTime,
					EUpgradeTime:      mockable.MaxTime,
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: backend,
	}

	blkTx := txs.NewMockUnsignedTx(ctrl)
	atomicRequests := map[ids.ID]*atomic.Requests{
		ids.GenerateTestID(): {
			RemoveRequests: [][]byte{{1}, {2}},
			PutRequests: []*atomic.Element{
				{
					Key:    []byte{3},
					Value:  []byte{4},
					Traits: [][]byte{{5}, {6}},
				},
			},
		},
	}
	blkTx.EXPECT().Visit(gomock.AssignableToTypeOf(&executor.StandardTxExecutor{})).DoAndReturn(
		func(e *executor.StandardTxExecutor) error {
			e.OnAccept = func() {}
			e.Inputs = atomicInputs
			e.AtomicRequests = atomicRequests
			return nil
		},
	).Times(1)

	// We can't serialize [blkTx] because it isn't
	// registered with the blocks.Codec.
	// Serialize this block with a dummy tx
	// and replace it after creation with the mock tx.
	// TODO allow serialization of mock txs.
	blk, err := block.NewApricotStandardBlock(
		parentID,
		2,
		[]*txs.Tx{
			{
				Unsigned: &txs.AdvanceTimeTx{},
				Creds:    []verify.Verifiable{},
			},
		},
	)
	require.NoError(err)
	blk.Transactions[0].Unsigned = blkTx

	// Set expectations for dependencies.
	timestamp := time.Now()
	parentStatelessBlk.EXPECT().Height().Return(uint64(1))
	parentState.EXPECT().GetTimestamp().Return(timestamp)
	parentState.EXPECT().GetCurrentGasCap().Return(commonfee.Gas(1_000_000), nil)

	parentStatelessBlk.EXPECT().Parent().Return(grandParentID).Times(1)

	err = verifier.ApricotStandardBlock(blk)
	require.ErrorIs(err, errConflictingParentTxs)
}

func TestVerifierVisitApricotStandardBlockWithProposalBlockParent(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	mempool := mempool.NewMockMempool(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	parentOnCommitState := state.NewMockDiff(ctrl)
	parentOnAbortState := state.NewMockDiff(ctrl)

	backend := &backend{
		blkIDToState: map[ids.ID]*blockState{
			parentID: {
				statelessBlock: parentStatelessBlk,
				proposalBlockState: proposalBlockState{
					onCommitState: parentOnCommitState,
					onAbortState:  parentOnAbortState,
				},
			},
		},
		Mempool: mempool,
		state:   s,
		ctx: &snow.Context{
			Log: logging.NoLog{},
		},
	}
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: mockable.MaxTime, // banff is not activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: backend,
	}

	blk, err := block.NewApricotStandardBlock(
		parentID,
		2,
		[]*txs.Tx{
			{
				Unsigned: &txs.AdvanceTimeTx{},
				Creds:    []verify.Verifiable{},
			},
		},
	)
	require.NoError(err)

	parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)

	err = verifier.ApricotStandardBlock(blk)
	require.ErrorIs(err, state.ErrMissingParentState)
}

func TestVerifierVisitBanffStandardBlockWithProposalBlockParent(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	mempool := mempool.NewMockMempool(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	parentTime := time.Now()
	parentOnCommitState := state.NewMockDiff(ctrl)
	parentOnAbortState := state.NewMockDiff(ctrl)

	backend := &backend{
		blkIDToState: map[ids.ID]*blockState{
			parentID: {
				statelessBlock: parentStatelessBlk,
				proposalBlockState: proposalBlockState{
					onCommitState: parentOnCommitState,
					onAbortState:  parentOnAbortState,
				},
			},
		},
		Mempool: mempool,
		state:   s,
		ctx: &snow.Context{
			Log: logging.NoLog{},
		},
	}
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: time.Time{}, // banff is activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: backend,
	}

	blk, err := block.NewBanffStandardBlock(
		parentTime.Add(time.Second),
		parentID,
		2,
		[]*txs.Tx{
			{
				Unsigned: &txs.AdvanceTimeTx{},
				Creds:    []verify.Verifiable{},
			},
		},
	)
	require.NoError(err)

	parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)

	err = verifier.BanffStandardBlock(blk)
	require.ErrorIs(err, state.ErrMissingParentState)
}

func TestVerifierVisitApricotCommitBlockUnexpectedParentState(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: mockable.MaxTime, // banff is not activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: &backend{
			blkIDToState: map[ids.ID]*blockState{
				parentID: {
					statelessBlock: parentStatelessBlk,
				},
			},
			state: s,
			ctx: &snow.Context{
				Log: logging.NoLog{},
			},
		},
	}

	blk, err := block.NewApricotCommitBlock(
		parentID,
		2,
	)
	require.NoError(err)

	// Set expectations for dependencies.
	parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)

	// Verify the block.
	err = verifier.ApricotCommitBlock(blk)
	require.ErrorIs(err, state.ErrMissingParentState)
}

func TestVerifierVisitBanffCommitBlockUnexpectedParentState(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	timestamp := time.Unix(12345, 0)
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: time.Time{}, // banff is activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: &backend{
			blkIDToState: map[ids.ID]*blockState{
				parentID: {
					statelessBlock: parentStatelessBlk,
					timestamp:      timestamp,
				},
			},
			state: s,
			ctx: &snow.Context{
				Log: logging.NoLog{},
			},
		},
	}

	blk, err := block.NewBanffCommitBlock(
		timestamp,
		parentID,
		2,
	)
	require.NoError(err)

	// Set expectations for dependencies.
	parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)

	// Verify the block.
	err = verifier.BanffCommitBlock(blk)
	require.ErrorIs(err, state.ErrMissingParentState)
}

func TestVerifierVisitApricotAbortBlockUnexpectedParentState(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: mockable.MaxTime, // banff is not activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: &backend{
			blkIDToState: map[ids.ID]*blockState{
				parentID: {
					statelessBlock: parentStatelessBlk,
				},
			},
			state: s,
			ctx: &snow.Context{
				Log: logging.NoLog{},
			},
		},
	}

	blk, err := block.NewApricotAbortBlock(
		parentID,
		2,
	)
	require.NoError(err)

	// Set expectations for dependencies.
	parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)

	// Verify the block.
	err = verifier.ApricotAbortBlock(blk)
	require.ErrorIs(err, state.ErrMissingParentState)
}

func TestVerifierVisitBanffAbortBlockUnexpectedParentState(t *testing.T) {
	require := require.New(t)
	ctrl := gomock.NewController(t)

	// Create mocked dependencies.
	s := state.NewMockState(ctrl)
	parentID := ids.GenerateTestID()
	parentStatelessBlk := block.NewMockBlock(ctrl)
	timestamp := time.Unix(12345, 0)
	verifier := &verifier{
		txExecutorBackend: &executor.Backend{
			Config: &config.Config{
				UpgradeConfig: upgrade.Config{
					BanffTime: time.Time{}, // banff is activated
				},
			},
			Clk: &mockable.Clock{},
		},
		backend: &backend{
			blkIDToState: map[ids.ID]*blockState{
				parentID: {
					statelessBlock: parentStatelessBlk,
					timestamp:      timestamp,
				},
			},
			state: s,
			ctx: &snow.Context{
				Log: logging.NoLog{},
			},
		},
	}

	blk, err := block.NewBanffAbortBlock(
		timestamp,
		parentID,
		2,
	)
	require.NoError(err)

	// Set expectations for dependencies.
	parentStatelessBlk.EXPECT().Height().Return(uint64(1)).Times(1)

	// Verify the block.
	err = verifier.BanffAbortBlock(blk)
	require.ErrorIs(err, state.ErrMissingParentState)
}
