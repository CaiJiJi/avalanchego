// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/set"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs/builder"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs/fees"
	"github.com/ava-labs/avalanchego/vms/platformvm/utxo"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
	"github.com/ava-labs/avalanchego/wallet/chain/p/backends"

	commonfees "github.com/ava-labs/avalanchego/vms/components/fees"
)

func TestCreateSubnetTxAP3FeeChange(t *testing.T) {
	ap3Time := defaultGenesisTime.Add(time.Hour)
	tests := []struct {
		name        string
		time        time.Time
		fee         uint64
		expectedErr error
	}{
		{
			name:        "pre-fork - correctly priced",
			time:        defaultGenesisTime,
			fee:         0,
			expectedErr: nil,
		},
		{
			name:        "post-fork - incorrectly priced",
			time:        ap3Time,
			fee:         100*defaultTxFee - 1*units.NanoAvax,
			expectedErr: utxo.ErrInsufficientUnlockedFunds,
		},
		{
			name:        "post-fork - correctly priced",
			time:        ap3Time,
			fee:         100 * defaultTxFee,
			expectedErr: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			env := newEnvironment(t, apricotPhase3)
			env.config.ApricotPhase3Time = ap3Time
			env.ctx.Lock.Lock()
			defer env.ctx.Lock.Unlock()

			env.state.SetTimestamp(test.time) // to duly set fee

			addrs := set.NewSet[ids.ShortID](len(preFundedKeys))
			for _, key := range preFundedKeys {
				addrs.Add(key.Address())
			}

			cfg := *env.config
			cfg.CreateSubnetTxFee = test.fee
			var (
				backend  = builder.NewBackend(env.ctx, &cfg, env.state, env.atomicUTXOs)
				pBuilder = backends.NewBuilder(addrs, backend)
				feeCfg   = cfg.GetDynamicFeesConfig(env.state.GetTimestamp())
				feeCalc  = &fees.Calculator{
					IsEUpgradeActive: false,
					Log:              logging.NoLog{},
					Config:           &cfg,
					ChainTime:        test.time,
					FeeManager:       commonfees.NewManager(feeCfg.UnitFees),
					ConsumedUnitsCap: feeCfg.BlockUnitsCap,

					Fee: test.fee,
				}
			)
			backend.ResetAddresses(addrs)

			utx, err := pBuilder.NewCreateSubnetTx(
				&secp256k1fx.OutputOwners{}, // owner
				feeCalc,
			)
			require.NoError(err)

			kc := secp256k1fx.NewKeychain(preFundedKeys...)
			s := backends.NewSigner(kc, backend)
			tx, err := backends.SignUnsigned(context.Background(), s, utx)
			require.NoError(err)

			stateDiff, err := state.NewDiff(lastAcceptedID, env)
			require.NoError(err)

			stateDiff.SetTimestamp(test.time)

			feeCfg = env.config.GetDynamicFeesConfig(stateDiff.GetTimestamp())
			executor := StandardTxExecutor{
				Backend:       &env.backend,
				BlkFeeManager: commonfees.NewManager(feeCfg.UnitFees),
				UnitCaps:      feeCfg.BlockUnitsCap,
				State:         stateDiff,
				Tx:            tx,
			}
			err = tx.Unsigned.Visit(&executor)
			require.ErrorIs(err, test.expectedErr)
		})
	}
}
