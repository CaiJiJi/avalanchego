// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package avm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/ava-labs/avalanchego/api"
	"github.com/ava-labs/avalanchego/chains/atomic"
	"github.com/ava-labs/avalanchego/codec"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/formatting"
	"github.com/ava-labs/avalanchego/utils/formatting/address"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/ava-labs/avalanchego/utils/units"
	"github.com/ava-labs/avalanchego/vms/avm/block"
	"github.com/ava-labs/avalanchego/vms/avm/block/executor"
	"github.com/ava-labs/avalanchego/vms/avm/config"
	"github.com/ava-labs/avalanchego/vms/avm/state"
	"github.com/ava-labs/avalanchego/vms/avm/txs"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/index"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/nftfx"
	"github.com/ava-labs/avalanchego/vms/propertyfx"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"

	avajson "github.com/ava-labs/avalanchego/utils/json"
	commonfees "github.com/ava-labs/avalanchego/vms/components/fees"
)

func TestServiceIssueTx(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	txArgs := &api.FormattedTx{}
	txReply := &api.JSONTxID{}
	err := env.service.IssueTx(nil, txArgs, txReply)
	require.ErrorIs(err, codec.ErrCantUnpackVersion)

	tx := newTx(t, env.genesisBytes, env.vm.ctx.ChainID, env.vm.parser, "AVAX")
	txArgs.Tx, err = formatting.Encode(formatting.Hex, tx.Bytes())
	require.NoError(err)
	txArgs.Encoding = formatting.Hex
	txReply = &api.JSONTxID{}
	require.NoError(env.service.IssueTx(nil, txArgs, txReply))
	require.Equal(tx.ID(), txReply.TxID)
}

func TestServiceGetTxStatus(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	statusArgs := &api.JSONTxID{}
	statusReply := &GetTxStatusReply{}
	err := env.service.GetTxStatus(nil, statusArgs, statusReply)
	require.ErrorIs(err, errNilTxID)

	newTx := newAvaxBaseTxWithOutputs(t, env)
	txID := newTx.ID()

	statusArgs = &api.JSONTxID{
		TxID: txID,
	}
	statusReply = &GetTxStatusReply{}
	require.NoError(env.service.GetTxStatus(nil, statusArgs, statusReply))
	require.Equal(choices.Unknown, statusReply.Status)

	issueAndAccept(require, env.vm, env.issuer, newTx)

	statusReply = &GetTxStatusReply{}
	require.NoError(env.service.GetTxStatus(nil, statusArgs, statusReply))
	require.Equal(choices.Accepted, statusReply.Status)
}

// Test the GetBalance method when argument Strict is true
func TestServiceGetBalanceStrict(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	assetID := ids.GenerateTestID()
	addr := ids.GenerateTestShortID()
	addrStr, err := env.vm.FormatLocalAddress(addr)
	require.NoError(err)

	// A UTXO with a 2 out of 2 multisig
	// where one of the addresses is [addr]
	twoOfTwoUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 2,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	env.vm.state.AddUTXO(twoOfTwoUTXO)
	require.NoError(env.vm.state.Commit())

	env.vm.ctx.Lock.Unlock()

	// Check the balance with IncludePartial set to true
	balanceArgs := &GetBalanceArgs{
		Address:        addrStr,
		AssetID:        assetID.String(),
		IncludePartial: true,
	}
	balanceReply := &GetBalanceReply{}
	require.NoError(env.service.GetBalance(nil, balanceArgs, balanceReply))
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Equal(uint64(1337), uint64(balanceReply.Balance))
	require.Len(balanceReply.UTXOIDs, 1)

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetBalanceArgs{
		Address: addrStr,
		AssetID: assetID.String(),
	}
	balanceReply = &GetBalanceReply{}
	require.NoError(env.service.GetBalance(nil, balanceArgs, balanceReply))
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Zero(balanceReply.Balance)
	require.Empty(balanceReply.UTXOIDs)

	env.vm.ctx.Lock.Lock()

	// A UTXO with a 1 out of 2 multisig
	// where one of the addresses is [addr]
	oneOfTwoUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	env.vm.state.AddUTXO(oneOfTwoUTXO)
	require.NoError(env.vm.state.Commit())

	env.vm.ctx.Lock.Unlock()

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetBalanceArgs{
		Address:        addrStr,
		AssetID:        assetID.String(),
		IncludePartial: true,
	}
	balanceReply = &GetBalanceReply{}
	require.NoError(env.service.GetBalance(nil, balanceArgs, balanceReply))
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Equal(uint64(1337+1337), uint64(balanceReply.Balance))
	require.Len(balanceReply.UTXOIDs, 2)

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetBalanceArgs{
		Address: addrStr,
		AssetID: assetID.String(),
	}
	balanceReply = &GetBalanceReply{}
	require.NoError(env.service.GetBalance(nil, balanceArgs, balanceReply))
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Zero(balanceReply.Balance)
	require.Empty(balanceReply.UTXOIDs)

	env.vm.ctx.Lock.Lock()

	// A UTXO with a 1 out of 1 multisig
	// but with a locktime in the future
	now := env.vm.clock.Time()
	futureUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Locktime:  uint64(now.Add(10 * time.Hour).Unix()),
				Threshold: 1,
				Addrs:     []ids.ShortID{addr},
			},
		},
	}
	// Insert the UTXO
	env.vm.state.AddUTXO(futureUTXO)
	require.NoError(env.vm.state.Commit())

	env.vm.ctx.Lock.Unlock()

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetBalanceArgs{
		Address:        addrStr,
		AssetID:        assetID.String(),
		IncludePartial: true,
	}
	balanceReply = &GetBalanceReply{}
	require.NoError(env.service.GetBalance(nil, balanceArgs, balanceReply))
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Equal(uint64(1337*3), uint64(balanceReply.Balance))
	require.Len(balanceReply.UTXOIDs, 3)

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetBalanceArgs{
		Address: addrStr,
		AssetID: assetID.String(),
	}
	balanceReply = &GetBalanceReply{}
	require.NoError(env.service.GetBalance(nil, balanceArgs, balanceReply))
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Zero(balanceReply.Balance)
	require.Empty(balanceReply.UTXOIDs)
}

func TestServiceGetTxs(t *testing.T) {
	require := require.New(t)
	env := setup(t, &envConfig{
		fork: latest,
	})
	var err error
	env.vm.addressTxsIndexer, err = index.NewIndexer(env.vm.db, env.vm.ctx.Log, "", prometheus.NewRegistry(), false)
	require.NoError(err)
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	assetID := ids.GenerateTestID()
	addr := ids.GenerateTestShortID()
	addrStr, err := env.vm.FormatLocalAddress(addr)
	require.NoError(err)

	testTxCount := 25
	testTxs := initTestTxIndex(t, env.vm.db, addr, assetID, testTxCount)

	env.vm.ctx.Lock.Unlock()

	// get the first page
	getTxsArgs := &GetAddressTxsArgs{
		PageSize:    10,
		JSONAddress: api.JSONAddress{Address: addrStr},
		AssetID:     assetID.String(),
	}
	getTxsReply := &GetAddressTxsReply{}
	require.NoError(env.service.GetAddressTxs(nil, getTxsArgs, getTxsReply))
	require.Len(getTxsReply.TxIDs, 10)
	require.Equal(getTxsReply.TxIDs, testTxs[:10])

	// get the second page
	getTxsArgs.Cursor = getTxsReply.Cursor
	getTxsReply = &GetAddressTxsReply{}
	require.NoError(env.service.GetAddressTxs(nil, getTxsArgs, getTxsReply))
	require.Len(getTxsReply.TxIDs, 10)
	require.Equal(getTxsReply.TxIDs, testTxs[10:20])
}

func TestServiceGetAllBalances(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	assetID := ids.GenerateTestID()
	addr := ids.GenerateTestShortID()
	addrStr, err := env.vm.FormatLocalAddress(addr)
	require.NoError(err)
	// A UTXO with a 2 out of 2 multisig
	// where one of the addresses is [addr]
	twoOfTwoUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 2,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	env.vm.state.AddUTXO(twoOfTwoUTXO)
	require.NoError(env.vm.state.Commit())

	env.vm.ctx.Lock.Unlock()

	// Check the balance with IncludePartial set to true
	balanceArgs := &GetAllBalancesArgs{
		JSONAddress:    api.JSONAddress{Address: addrStr},
		IncludePartial: true,
	}
	reply := &GetAllBalancesReply{}
	require.NoError(env.service.GetAllBalances(nil, balanceArgs, reply))
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Len(reply.Balances, 1)
	require.Equal(assetID.String(), reply.Balances[0].AssetID)
	require.Equal(uint64(1337), uint64(reply.Balances[0].Balance))

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress: api.JSONAddress{Address: addrStr},
	}
	reply = &GetAllBalancesReply{}
	require.NoError(env.service.GetAllBalances(nil, balanceArgs, reply))
	require.Empty(reply.Balances)

	env.vm.ctx.Lock.Lock()

	// A UTXO with a 1 out of 2 multisig
	// where one of the addresses is [addr]
	oneOfTwoUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	env.vm.state.AddUTXO(oneOfTwoUTXO)
	require.NoError(env.vm.state.Commit())

	env.vm.ctx.Lock.Unlock()

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress:    api.JSONAddress{Address: addrStr},
		IncludePartial: true,
	}
	reply = &GetAllBalancesReply{}
	require.NoError(env.service.GetAllBalances(nil, balanceArgs, reply))
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Len(reply.Balances, 1)
	require.Equal(assetID.String(), reply.Balances[0].AssetID)
	require.Equal(uint64(1337*2), uint64(reply.Balances[0].Balance))

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress: api.JSONAddress{Address: addrStr},
	}
	reply = &GetAllBalancesReply{}
	require.NoError(env.service.GetAllBalances(nil, balanceArgs, reply))
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Empty(reply.Balances)

	env.vm.ctx.Lock.Lock()

	// A UTXO with a 1 out of 1 multisig
	// but with a locktime in the future
	now := env.vm.clock.Time()
	futureUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: assetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Locktime:  uint64(now.Add(10 * time.Hour).Unix()),
				Threshold: 1,
				Addrs:     []ids.ShortID{addr},
			},
		},
	}
	// Insert the UTXO
	env.vm.state.AddUTXO(futureUTXO)
	require.NoError(env.vm.state.Commit())

	env.vm.ctx.Lock.Unlock()

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress:    api.JSONAddress{Address: addrStr},
		IncludePartial: true,
	}
	reply = &GetAllBalancesReply{}
	require.NoError(env.service.GetAllBalances(nil, balanceArgs, reply))
	// The balance should include the UTXO since it is partly owned by [addr]
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Len(reply.Balances, 1)
	require.Equal(assetID.String(), reply.Balances[0].AssetID)
	require.Equal(uint64(1337*3), uint64(reply.Balances[0].Balance))
	// Check the balance with IncludePartial set to false
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress: api.JSONAddress{Address: addrStr},
	}
	reply = &GetAllBalancesReply{}
	require.NoError(env.service.GetAllBalances(nil, balanceArgs, reply))
	// The balance should not include the UTXO since it is only partly owned by [addr]
	require.Empty(reply.Balances)

	env.vm.ctx.Lock.Lock()

	// A UTXO for a different asset
	otherAssetID := ids.GenerateTestID()
	otherAssetUTXO := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID:        ids.GenerateTestID(),
			OutputIndex: 0,
		},
		Asset: avax.Asset{ID: otherAssetID},
		Out: &secp256k1fx.TransferOutput{
			Amt: 1337,
			OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 2,
				Addrs:     []ids.ShortID{addr, ids.GenerateTestShortID()},
			},
		},
	}
	// Insert the UTXO
	env.vm.state.AddUTXO(otherAssetUTXO)
	require.NoError(env.vm.state.Commit())

	env.vm.ctx.Lock.Unlock()

	// Check the balance with IncludePartial set to true
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress:    api.JSONAddress{Address: addrStr},
		IncludePartial: true,
	}
	reply = &GetAllBalancesReply{}
	require.NoError(env.service.GetAllBalances(nil, balanceArgs, reply))
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Len(reply.Balances, 2)
	gotAssetIDs := []string{reply.Balances[0].AssetID, reply.Balances[1].AssetID}
	require.Contains(gotAssetIDs, assetID.String())
	require.Contains(gotAssetIDs, otherAssetID.String())
	gotBalances := []uint64{uint64(reply.Balances[0].Balance), uint64(reply.Balances[1].Balance)}
	require.Contains(gotBalances, uint64(1337))
	require.Contains(gotBalances, uint64(1337*3))

	// Check the balance with IncludePartial set to false
	balanceArgs = &GetAllBalancesArgs{
		JSONAddress: api.JSONAddress{Address: addrStr},
	}
	reply = &GetAllBalancesReply{}
	require.NoError(env.service.GetAllBalances(nil, balanceArgs, reply))
	// The balance should include the UTXO since it is partly owned by [addr]
	require.Empty(reply.Balances)
}

func TestServiceGetTx(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	txID := env.genesisTx.ID()

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     txID,
		Encoding: formatting.Hex,
	}, &reply))

	var txStr string
	require.NoError(json.Unmarshal(reply.Tx, &txStr))

	txBytes, err := formatting.Decode(reply.Encoding, txStr)
	require.NoError(err)
	require.Equal(env.genesisTx.Bytes(), txBytes)
}

func TestServiceGetTxJSON_BaseTx(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	env.vm.ctx.Lock.Unlock()
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	// to avoid tests flackiness we fix clock time wrt chain time
	// so to have stable updated fee rates.
	env.vm.clock.Set(env.vm.state.GetTimestamp().Add(time.Second))

	newTx := newAvaxBaseTxWithOutputs(t, env)
	issueAndAccept(require, env.vm, env.issuer, newTx)

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     newTx.ID(),
		Encoding: formatting.JSON,
	}, &reply))

	require.Equal(formatting.JSON, reply.Encoding)

	replyTxBytes, err := json.MarshalIndent(reply.Tx, "", "\t")
	require.NoError(err)

	expectedReplyTxString := `{
	"unsignedTx": {
		"networkID": 10,
		"blockchainID": "PLACEHOLDER_BLOCKCHAIN_ID",
		"outputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 1000,
					"locktime": 0,
					"threshold": 1
				}
			},
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1d6kkj0qh4wcmus3tk59npwt3rluc6en72ngurd"
					],
					"amount": 999992084,
					"locktime": 0,
					"threshold": 1
				}
			}
		],
		"inputs": [
			{
				"txID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"outputIndex": 2,
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"input": {
					"amount": 1000000000,
					"signatureIndices": [
						0
					]
				}
			}
		],
		"memo": "0x0102030405060708"
	},
	"credentials": [
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"PLACEHOLDER_SIGNATURE"
				]
			}
		}
	],
	"id": "PLACEHOLDER_TX_ID"
}`

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_TX_ID", newTx.ID().String(), 1)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_BLOCKCHAIN_ID", newTx.Unsigned.(*txs.BaseTx).BlockchainID.String(), 1)

	sigStr, err := formatting.Encode(formatting.HexNC, newTx.Creds[0].Credential.(*secp256k1fx.Credential).Sigs[0][:])
	require.NoError(err)

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_SIGNATURE", sigStr, 1)

	require.Equal(expectedReplyTxString, string(replyTxBytes))
}

func TestServiceGetTxJSON_ExportTx(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	env.vm.ctx.Lock.Unlock()
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	// to avoid tests flackiness we fix clock time wrt chain time
	// so to have stable updated fee rates.
	env.vm.clock.Set(env.vm.state.GetTimestamp().Add(time.Second))

	newTx := buildTestExportTx(t, env, env.vm.ctx.CChainID)
	issueAndAccept(require, env.vm, env.issuer, newTx)

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     newTx.ID(),
		Encoding: formatting.JSON,
	}, &reply))

	require.Equal(formatting.JSON, reply.Encoding)
	replyTxBytes, err := json.MarshalIndent(reply.Tx, "", "\t")
	require.NoError(err)

	expectedReplyTxString := `{
	"unsignedTx": {
		"networkID": 10,
		"blockchainID": "PLACEHOLDER_BLOCKCHAIN_ID",
		"outputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 999991972,
					"locktime": 0,
					"threshold": 1
				}
			}
		],
		"inputs": [
			{
				"txID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"outputIndex": 2,
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"input": {
					"amount": 1000000000,
					"signatureIndices": [
						0
					]
				}
			}
		],
		"memo": "0x",
		"destinationChain": "2mcwQKiD8VEspmMJpL1dc7okQQ5dDVAWeCBZ7FWBFAbxpv3t7w",
		"exportedOutputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 1000,
					"locktime": 0,
					"threshold": 1
				}
			}
		]
	},
	"credentials": [
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"PLACEHOLDER_SIGNATURE"
				]
			}
		}
	],
	"id": "PLACEHOLDER_TX_ID"
}`

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_TX_ID", newTx.ID().String(), 1)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_BLOCKCHAIN_ID", newTx.Unsigned.(*txs.ExportTx).BlockchainID.String(), 1)

	sigStr, err := formatting.Encode(formatting.HexNC, newTx.Creds[0].Credential.(*secp256k1fx.Credential).Sigs[0][:])
	require.NoError(err)

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_SIGNATURE", sigStr, 1)

	require.Equal(expectedReplyTxString, string(replyTxBytes))
}

func TestServiceGetTxJSON_CreateAssetTx(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
		additionalFxs: []*common.Fx{{
			ID: propertyfx.ID,
			Fx: &propertyfx.Fx{},
		}},
	})
	env.vm.ctx.Lock.Unlock()
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	// to avoid tests flackiness we fix clock time wrt chain time
	// so to have stable updated fee rates.
	env.vm.clock.Set(env.vm.state.GetTimestamp().Add(time.Second))

	initialStates := map[uint32][]verify.State{
		uint32(0): {
			&nftfx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			}, &secp256k1fx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
		},
		uint32(1): {
			&nftfx.MintOutput{
				GroupID: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
			&nftfx.MintOutput{
				GroupID: 2,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
		},
		uint32(2): {
			&propertyfx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
			&propertyfx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
		},
	}
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, env, initialStates)
	issueAndAccept(require, env.vm, env.issuer, createAssetTx)

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     createAssetTx.ID(),
		Encoding: formatting.JSON,
	}, &reply))

	require.Equal(formatting.JSON, reply.Encoding)

	replyTxBytes, err := json.MarshalIndent(reply.Tx, "", "\t")
	require.NoError(err)

	expectedReplyTxString := `{
	"unsignedTx": {
		"networkID": 10,
		"blockchainID": "PLACEHOLDER_BLOCKCHAIN_ID",
		"outputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 999992572,
					"locktime": 0,
					"threshold": 1
				}
			}
		],
		"inputs": [
			{
				"txID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"outputIndex": 2,
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"input": {
					"amount": 1000000000,
					"signatureIndices": [
						0
					]
				}
			}
		],
		"memo": "0x",
		"name": "Team Rocket",
		"symbol": "TR",
		"denomination": 0,
		"initialStates": [
			{
				"fxIndex": 0,
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"outputs": [
					{
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"locktime": 0,
						"threshold": 1
					},
					{
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"groupID": 0,
						"locktime": 0,
						"threshold": 1
					}
				]
			},
			{
				"fxIndex": 1,
				"fxID": "qd2U4HDWUvMrVUeTcCHp6xH3Qpnn1XbU5MDdnBoiifFqvgXwT",
				"outputs": [
					{
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"groupID": 1,
						"locktime": 0,
						"threshold": 1
					},
					{
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"groupID": 2,
						"locktime": 0,
						"threshold": 1
					}
				]
			},
			{
				"fxIndex": 2,
				"fxID": "rXJsCSEYXg2TehWxCEEGj6JU2PWKTkd6cBdNLjoe2SpsKD9cy",
				"outputs": [
					{
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"locktime": 0,
						"threshold": 1
					},
					{
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"locktime": 0,
						"threshold": 1
					}
				]
			}
		]
	},
	"credentials": [
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"0xe943dfd81049dc87f0acecd7a94f2b42717891f230ce04d73fe501c9d4e29f8b5fcd3c6b763f1074da01799fb782d221bc6f5fdebce41180b18bd6aa1cff91c700"
				]
			}
		}
	],
	"id": "PLACEHOLDER_TX_ID"
}`

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_TX_ID", createAssetTx.ID().String(), 1)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_BLOCKCHAIN_ID", createAssetTx.Unsigned.(*txs.CreateAssetTx).BlockchainID.String(), 1)

	require.Equal(expectedReplyTxString, string(replyTxBytes))
}

func TestServiceGetTxJSON_OperationTxWithNftxMintOp(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
		additionalFxs: []*common.Fx{{
			ID: propertyfx.ID,
			Fx: &propertyfx.Fx{},
		}},
	})
	env.vm.ctx.Lock.Unlock()
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	// to avoid tests flackiness we fix clock time wrt chain time
	// so to have stable updated fee rates.
	env.vm.clock.Set(env.vm.state.GetTimestamp().Add(time.Second))

	key := keys[0]
	initialStates := map[uint32][]verify.State{
		uint32(1): {
			&nftfx.MintOutput{
				GroupID: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
			&nftfx.MintOutput{
				GroupID: 2,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
		},
	}
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, env, initialStates)
	issueAndAccept(require, env.vm, env.issuer, createAssetTx)

	op := buildNFTxMintOp(createAssetTx, key, 1, 1)
	mintNFTTx := buildOperationTxWithOp(t, env, []*txs.Operation{op})
	issueAndAccept(require, env.vm, env.issuer, mintNFTTx)

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     mintNFTTx.ID(),
		Encoding: formatting.JSON,
	}, &reply))

	require.Equal(formatting.JSON, reply.Encoding)

	replyTxBytes, err := json.MarshalIndent(reply.Tx, "", "\t")
	require.NoError(err)

	expectedReplyTxString := `{
	"unsignedTx": {
		"networkID": 10,
		"blockchainID": "PLACEHOLDER_BLOCKCHAIN_ID",
		"outputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 999988192,
					"locktime": 0,
					"threshold": 1
				}
			}
		],
		"inputs": [
			{
				"txID": "L46hctVP2oNMKje6VQdk6bSxbXbi2BCgWvWWmHhFJ3yGZMTJc",
				"outputIndex": 0,
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"input": {
					"amount": 999993292,
					"signatureIndices": [
						0
					]
				}
			}
		],
		"memo": "0x",
		"operations": [
			{
				"assetID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
				"inputIDs": [
					{
						"txID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
						"outputIndex": 1
					}
				],
				"fxID": "qd2U4HDWUvMrVUeTcCHp6xH3Qpnn1XbU5MDdnBoiifFqvgXwT",
				"operation": {
					"mintInput": {
						"signatureIndices": [
							0
						]
					},
					"groupID": 1,
					"payload": "0x68656c6c6f",
					"outputs": [
						{
							"addresses": [
								"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
							],
							"locktime": 0,
							"threshold": 1
						}
					]
				}
			}
		]
	},
	"credentials": [
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"0x3ca34a1e672a4d34b0ac30df5b90c0afeda87702464d13e9e69fb6173ae3537b5736c2d72848d86d99d7f91b3475abfba9c0cc32b10a9e29393144e4dedaf44800"
				]
			}
		},
		{
			"fxID": "qd2U4HDWUvMrVUeTcCHp6xH3Qpnn1XbU5MDdnBoiifFqvgXwT",
			"credential": {
				"signatures": [
					"0x3ca34a1e672a4d34b0ac30df5b90c0afeda87702464d13e9e69fb6173ae3537b5736c2d72848d86d99d7f91b3475abfba9c0cc32b10a9e29393144e4dedaf44800"
				]
			}
		}
	],
	"id": "PLACEHOLDER_TX_ID"
}`

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_CREATE_ASSET_TX_ID", createAssetTx.ID().String(), 2)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_TX_ID", mintNFTTx.ID().String(), 1)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_BLOCKCHAIN_ID", mintNFTTx.Unsigned.(*txs.OperationTx).BlockchainID.String(), 1)

	sigStr, err := formatting.Encode(formatting.HexNC, mintNFTTx.Creds[1].Credential.(*nftfx.Credential).Sigs[0][:])
	require.NoError(err)

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_SIGNATURE", sigStr, 1)

	require.Equal(expectedReplyTxString, string(replyTxBytes))
}

func TestServiceGetTxJSON_OperationTxWithMultipleNftxMintOp(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
		additionalFxs: []*common.Fx{{
			ID: propertyfx.ID,
			Fx: &propertyfx.Fx{},
		}},
	})
	env.vm.ctx.Lock.Unlock()
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	// to avoid tests flackiness we fix clock time wrt chain time
	// so to have stable updated fee rates.
	env.vm.clock.Set(env.vm.state.GetTimestamp().Add(time.Second))

	key := keys[0]
	initialStates := map[uint32][]verify.State{
		uint32(0): {
			&nftfx.MintOutput{
				GroupID: 0,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
		},
		uint32(1): {
			&nftfx.MintOutput{
				GroupID: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
		},
	}
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, env, initialStates)
	issueAndAccept(require, env.vm, env.issuer, createAssetTx)

	mintOp1 := buildNFTxMintOp(createAssetTx, key, 1, 0)
	mintOp2 := buildNFTxMintOp(createAssetTx, key, 2, 1)
	mintNFTTx := buildOperationTxWithOp(t, env, []*txs.Operation{mintOp1, mintOp2})
	issueAndAccept(require, env.vm, env.issuer, mintNFTTx)

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     mintNFTTx.ID(),
		Encoding: formatting.JSON,
	}, &reply))

	require.Equal(formatting.JSON, reply.Encoding)

	replyTxBytes, err := json.MarshalIndent(reply.Tx, "", "\t")
	require.NoError(err)

	expectedReplyTxString := `{
	"unsignedTx": {
		"networkID": 10,
		"blockchainID": "PLACEHOLDER_BLOCKCHAIN_ID",
		"outputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 999987749,
					"locktime": 0,
					"threshold": 1
				}
			}
		],
		"inputs": [
			{
				"txID": "2aTrnk4R7eRdaZjYi6JwwVmKkce8xqbXDubYE54q4ojUbkGx51",
				"outputIndex": 0,
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"input": {
					"amount": 999993260,
					"signatureIndices": [
						0
					]
				}
			}
		],
		"memo": "0x",
		"operations": [
			{
				"assetID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
				"inputIDs": [
					{
						"txID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
						"outputIndex": 1
					}
				],
				"fxID": "qd2U4HDWUvMrVUeTcCHp6xH3Qpnn1XbU5MDdnBoiifFqvgXwT",
				"operation": {
					"mintInput": {
						"signatureIndices": [
							0
						]
					},
					"groupID": 0,
					"payload": "0x68656c6c6f",
					"outputs": [
						{
							"addresses": [
								"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
							],
							"locktime": 0,
							"threshold": 1
						}
					]
				}
			},
			{
				"assetID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
				"inputIDs": [
					{
						"txID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
						"outputIndex": 2
					}
				],
				"fxID": "qd2U4HDWUvMrVUeTcCHp6xH3Qpnn1XbU5MDdnBoiifFqvgXwT",
				"operation": {
					"mintInput": {
						"signatureIndices": [
							0
						]
					},
					"groupID": 1,
					"payload": "0x68656c6c6f",
					"outputs": [
						{
							"addresses": [
								"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
							],
							"locktime": 0,
							"threshold": 1
						}
					]
				}
			}
		]
	},
	"credentials": [
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"0x659fa1e905ccdb514746cee3f009d138e7a627504d9d1ac1f0bacfb31cef8250040db7ef63d6b941d3ffcbbd1f32ef236210a303d454a63ab9c7a1a51a07496c01"
				]
			}
		},
		{
			"fxID": "qd2U4HDWUvMrVUeTcCHp6xH3Qpnn1XbU5MDdnBoiifFqvgXwT",
			"credential": {
				"signatures": [
					"0x659fa1e905ccdb514746cee3f009d138e7a627504d9d1ac1f0bacfb31cef8250040db7ef63d6b941d3ffcbbd1f32ef236210a303d454a63ab9c7a1a51a07496c01"
				]
			}
		},
		{
			"fxID": "qd2U4HDWUvMrVUeTcCHp6xH3Qpnn1XbU5MDdnBoiifFqvgXwT",
			"credential": {
				"signatures": [
					"PLACEHOLDER_SIGNATURE"
				]
			}
		}
	],
	"id": "PLACEHOLDER_TX_ID"
}`

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_CREATE_ASSET_TX_ID", createAssetTx.ID().String(), 4)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_TX_ID", mintNFTTx.ID().String(), 1)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_BLOCKCHAIN_ID", mintNFTTx.Unsigned.(*txs.OperationTx).BlockchainID.String(), 1)

	sigStr, err := formatting.Encode(formatting.HexNC, mintNFTTx.Creds[1].Credential.(*nftfx.Credential).Sigs[0][:])
	require.NoError(err)

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_SIGNATURE", sigStr, 2)

	require.Equal(expectedReplyTxString, string(replyTxBytes))
}

func TestServiceGetTxJSON_OperationTxWithSecpMintOp(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
		additionalFxs: []*common.Fx{{
			ID: propertyfx.ID,
			Fx: &propertyfx.Fx{},
		}},
	})
	env.vm.ctx.Lock.Unlock()
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	// to avoid tests flackiness we fix clock time wrt chain time
	// so to have stable updated fee rates.
	env.vm.clock.Set(env.vm.state.GetTimestamp().Add(time.Second))

	key := keys[0]
	initialStates := map[uint32][]verify.State{
		uint32(0): {
			&nftfx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			}, &secp256k1fx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
		},
	}
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, env, initialStates)
	issueAndAccept(require, env.vm, env.issuer, createAssetTx)

	op := buildSecpMintOp(createAssetTx, key, 1)
	mintSecpOpTx := buildOperationTxWithOp(t, env, []*txs.Operation{op})
	issueAndAccept(require, env.vm, env.issuer, mintSecpOpTx)

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     mintSecpOpTx.ID(),
		Encoding: formatting.JSON,
	}, &reply))

	require.Equal(formatting.JSON, reply.Encoding)

	replyTxBytes, err := json.MarshalIndent(reply.Tx, "", "\t")
	require.NoError(err)

	expectedReplyTxString := `{
	"unsignedTx": {
		"networkID": 10,
		"blockchainID": "PLACEHOLDER_BLOCKCHAIN_ID",
		"outputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 999988127,
					"locktime": 0,
					"threshold": 1
				}
			}
		],
		"inputs": [
			{
				"txID": "2amsBFNL9FXTY7A3jZegVgC2fkYcoSVAzUsoh4ywmrCbQXYYDt",
				"outputIndex": 0,
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"input": {
					"amount": 999993308,
					"signatureIndices": [
						0
					]
				}
			}
		],
		"memo": "0x",
		"operations": [
			{
				"assetID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
				"inputIDs": [
					{
						"txID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
						"outputIndex": 1
					}
				],
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"operation": {
					"mintInput": {
						"signatureIndices": [
							0
						]
					},
					"mintOutput": {
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"locktime": 0,
						"threshold": 1
					},
					"transferOutput": {
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"amount": 1,
						"locktime": 0,
						"threshold": 1
					}
				}
			}
		]
	},
	"credentials": [
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"0x3398fcc938cf42475a65d1d748e782d0d5be8b0b9039a210e6abe943983953e451fceccc283eda0a93859ac920d0cf45e63b7f326667f7e79ae0573192556f4a00"
				]
			}
		},
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"0x3398fcc938cf42475a65d1d748e782d0d5be8b0b9039a210e6abe943983953e451fceccc283eda0a93859ac920d0cf45e63b7f326667f7e79ae0573192556f4a00"
				]
			}
		}
	],
	"id": "PLACEHOLDER_TX_ID"
}`

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_CREATE_ASSET_TX_ID", createAssetTx.ID().String(), 2)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_TX_ID", mintSecpOpTx.ID().String(), 1)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_BLOCKCHAIN_ID", mintSecpOpTx.Unsigned.(*txs.OperationTx).BlockchainID.String(), 1)

	sigStr, err := formatting.Encode(formatting.HexNC, mintSecpOpTx.Creds[0].Credential.(*secp256k1fx.Credential).Sigs[0][:])
	require.NoError(err)

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_SIGNATURE", sigStr, 1)

	require.Equal(expectedReplyTxString, string(replyTxBytes))
}

func TestServiceGetTxJSON_OperationTxWithMultipleSecpMintOp(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: eUpgrade,
		additionalFxs: []*common.Fx{{
			ID: propertyfx.ID,
			Fx: &propertyfx.Fx{},
		}},
	})
	env.vm.ctx.Lock.Unlock()
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	// to avoid tests flackiness we fix clock time wrt chain time
	// so to have stable updated fee rates.
	env.vm.clock.Set(env.vm.state.GetTimestamp().Add(time.Second))

	key := keys[0]
	initialStates := map[uint32][]verify.State{
		uint32(0): {
			&secp256k1fx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{key.PublicKey().Address()},
				},
			},
		},
		uint32(1): {
			&secp256k1fx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{key.PublicKey().Address()},
				},
			},
		},
	}
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, env, initialStates)
	issueAndAccept(require, env.vm, env.issuer, createAssetTx)

	op1 := buildSecpMintOp(createAssetTx, key, 1)
	op2 := buildSecpMintOp(createAssetTx, key, 2)
	mintSecpOpTx := buildOperationTxWithOp(t, env, []*txs.Operation{op1, op2})
	issueAndAccept(require, env.vm, env.issuer, mintSecpOpTx)

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     mintSecpOpTx.ID(),
		Encoding: formatting.JSON,
	}, &reply))

	require.Equal(formatting.JSON, reply.Encoding)

	replyTxBytes, err := json.MarshalIndent(reply.Tx, "", "\t")
	require.NoError(err)

	expectedReplyTxString := `{
	"unsignedTx": {
		"networkID": 10,
		"blockchainID": "PLACEHOLDER_BLOCKCHAIN_ID",
		"outputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 999987619,
					"locktime": 0,
					"threshold": 1
				}
			}
		],
		"inputs": [
			{
				"txID": "2XFtZCtpqfcQC8zGDTSfyy8v5ks2WX83Dj4iXfoUjcEcHBA1CJ",
				"outputIndex": 0,
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"input": {
					"amount": 999993292,
					"signatureIndices": [
						0
					]
				}
			}
		],
		"memo": "0x",
		"operations": [
			{
				"assetID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
				"inputIDs": [
					{
						"txID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
						"outputIndex": 1
					}
				],
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"operation": {
					"mintInput": {
						"signatureIndices": [
							0
						]
					},
					"mintOutput": {
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"locktime": 0,
						"threshold": 1
					},
					"transferOutput": {
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"amount": 1,
						"locktime": 0,
						"threshold": 1
					}
				}
			},
			{
				"assetID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
				"inputIDs": [
					{
						"txID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
						"outputIndex": 2
					}
				],
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"operation": {
					"mintInput": {
						"signatureIndices": [
							0
						]
					},
					"mintOutput": {
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"locktime": 0,
						"threshold": 1
					},
					"transferOutput": {
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"amount": 1,
						"locktime": 0,
						"threshold": 1
					}
				}
			}
		]
	},
	"credentials": [
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"PLACEHOLDER_SIGNATURE"
				]
			}
		},
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"0xd47a9aadf3acb5e46eca69104142c0c9a5b1db36c47255b0b764f56a4e3d0d4769a4a6bb1ca3d5159722635198d1edb85d4914c3732a391a4e8a96cb84205ad001"
				]
			}
		},
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"0xd47a9aadf3acb5e46eca69104142c0c9a5b1db36c47255b0b764f56a4e3d0d4769a4a6bb1ca3d5159722635198d1edb85d4914c3732a391a4e8a96cb84205ad001"
				]
			}
		}
	],
	"id": "PLACEHOLDER_TX_ID"
}`

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_CREATE_ASSET_TX_ID", createAssetTx.ID().String(), 4)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_TX_ID", mintSecpOpTx.ID().String(), 1)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_BLOCKCHAIN_ID", mintSecpOpTx.Unsigned.(*txs.OperationTx).BlockchainID.String(), 1)

	sigStr, err := formatting.Encode(formatting.HexNC, mintSecpOpTx.Creds[0].Credential.(*secp256k1fx.Credential).Sigs[0][:])
	require.NoError(err)

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_SIGNATURE", sigStr, 2)

	require.Equal(expectedReplyTxString, string(replyTxBytes))
}

func TestServiceGetTxJSON_OperationTxWithPropertyFxMintOp(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
		additionalFxs: []*common.Fx{{
			ID: propertyfx.ID,
			Fx: &propertyfx.Fx{},
		}},
	})
	env.vm.ctx.Lock.Unlock()
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	// to avoid tests flackiness we fix clock time wrt chain time
	// so to have stable updated fee rates.
	env.vm.clock.Set(env.vm.state.GetTimestamp().Add(time.Second))

	key := keys[0]
	initialStates := map[uint32][]verify.State{
		uint32(2): {
			&propertyfx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
		},
	}
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, env, initialStates)
	issueAndAccept(require, env.vm, env.issuer, createAssetTx)

	op := buildPropertyFxMintOp(createAssetTx, key, 1)
	mintPropertyFxOpTx := buildOperationTxWithOp(t, env, []*txs.Operation{op})
	issueAndAccept(require, env.vm, env.issuer, mintPropertyFxOpTx)

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     mintPropertyFxOpTx.ID(),
		Encoding: formatting.JSON,
	}, &reply))

	require.Equal(formatting.JSON, reply.Encoding)

	replyTxBytes, err := json.MarshalIndent(reply.Tx, "", "\t")
	require.NoError(err)

	expectedReplyTxString := `{
	"unsignedTx": {
		"networkID": 10,
		"blockchainID": "PLACEHOLDER_BLOCKCHAIN_ID",
		"outputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 999988387,
					"locktime": 0,
					"threshold": 1
				}
			}
		],
		"inputs": [
			{
				"txID": "2qYV13hjDYcy8KQTCT4pUGdN29MTqGHdcgXuZhHhrHAVW4W3Cu",
				"outputIndex": 0,
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"input": {
					"amount": 999993484,
					"signatureIndices": [
						0
					]
				}
			}
		],
		"memo": "0x",
		"operations": [
			{
				"assetID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
				"inputIDs": [
					{
						"txID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
						"outputIndex": 1
					}
				],
				"fxID": "rXJsCSEYXg2TehWxCEEGj6JU2PWKTkd6cBdNLjoe2SpsKD9cy",
				"operation": {
					"mintInput": {
						"signatureIndices": [
							0
						]
					},
					"mintOutput": {
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"locktime": 0,
						"threshold": 1
					},
					"ownedOutput": {
						"addresses": [],
						"locktime": 0,
						"threshold": 0
					}
				}
			}
		]
	},
	"credentials": [
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"0x5d55e6489ba9884d9fc88e1447d55e6a0fab85957e2afce004d85c5f34ce061e68c905707941fe16f59eb15df1ab1067c90c3102fea9e60d2ab505fbdf2d06fe00"
				]
			}
		},
		{
			"fxID": "rXJsCSEYXg2TehWxCEEGj6JU2PWKTkd6cBdNLjoe2SpsKD9cy",
			"credential": {
				"signatures": [
					"PLACEHOLDER_SIGNATURE"
				]
			}
		}
	],
	"id": "PLACEHOLDER_TX_ID"
}`

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_CREATE_ASSET_TX_ID", createAssetTx.ID().String(), 2)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_TX_ID", mintPropertyFxOpTx.ID().String(), 1)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_BLOCKCHAIN_ID", mintPropertyFxOpTx.Unsigned.(*txs.OperationTx).BlockchainID.String(), 1)

	sigStr, err := formatting.Encode(formatting.HexNC, mintPropertyFxOpTx.Creds[1].Credential.(*propertyfx.Credential).Sigs[0][:])
	require.NoError(err)

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_SIGNATURE", sigStr, 1)

	require.Equal(expectedReplyTxString, string(replyTxBytes))
}

func TestServiceGetTxJSON_OperationTxWithPropertyFxMintOpMultiple(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
		additionalFxs: []*common.Fx{{
			ID: propertyfx.ID,
			Fx: &propertyfx.Fx{},
		}},
	})
	env.vm.ctx.Lock.Unlock()
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	// to avoid tests flackiness we fix clock time wrt chain time
	// so to have stable updated fee rates.
	env.vm.clock.Set(env.vm.state.GetTimestamp().Add(time.Second))

	key := keys[0]
	initialStates := map[uint32][]verify.State{
		uint32(2): {
			&propertyfx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
			&propertyfx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
				},
			},
		},
	}
	createAssetTx := newAvaxCreateAssetTxWithOutputs(t, env, initialStates)
	issueAndAccept(require, env.vm, env.issuer, createAssetTx)

	op1 := buildPropertyFxMintOp(createAssetTx, key, 1)
	op2 := buildPropertyFxMintOp(createAssetTx, key, 2)
	mintPropertyFxOpTx := buildOperationTxWithOp(t, env, []*txs.Operation{op1, op2})
	issueAndAccept(require, env.vm, env.issuer, mintPropertyFxOpTx)

	reply := api.GetTxReply{}
	require.NoError(env.service.GetTx(nil, &api.GetTxArgs{
		TxID:     mintPropertyFxOpTx.ID(),
		Encoding: formatting.JSON,
	}, &reply))

	require.Equal(formatting.JSON, reply.Encoding)

	replyTxBytes, err := json.MarshalIndent(reply.Tx, "", "\t")
	require.NoError(err)

	expectedReplyTxString := `{
	"unsignedTx": {
		"networkID": 10,
		"blockchainID": "PLACEHOLDER_BLOCKCHAIN_ID",
		"outputs": [
			{
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"output": {
					"addresses": [
						"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
					],
					"amount": 999987819,
					"locktime": 0,
					"threshold": 1
				}
			}
		],
		"inputs": [
			{
				"txID": "vu6reemKheEHERYDUArcb6U1T3CJWP149ux1Liga1LrcoH7ta",
				"outputIndex": 0,
				"assetID": "tvLKci3hNoCX4NijS6TfiT6XJJY3gGKd2git6SSVTG5J8Nfby",
				"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
				"input": {
					"amount": 999993324,
					"signatureIndices": [
						0
					]
				}
			}
		],
		"memo": "0x",
		"operations": [
			{
				"assetID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
				"inputIDs": [
					{
						"txID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
						"outputIndex": 1
					}
				],
				"fxID": "rXJsCSEYXg2TehWxCEEGj6JU2PWKTkd6cBdNLjoe2SpsKD9cy",
				"operation": {
					"mintInput": {
						"signatureIndices": [
							0
						]
					},
					"mintOutput": {
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"locktime": 0,
						"threshold": 1
					},
					"ownedOutput": {
						"addresses": [],
						"locktime": 0,
						"threshold": 0
					}
				}
			},
			{
				"assetID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
				"inputIDs": [
					{
						"txID": "PLACEHOLDER_CREATE_ASSET_TX_ID",
						"outputIndex": 2
					}
				],
				"fxID": "rXJsCSEYXg2TehWxCEEGj6JU2PWKTkd6cBdNLjoe2SpsKD9cy",
				"operation": {
					"mintInput": {
						"signatureIndices": [
							0
						]
					},
					"mintOutput": {
						"addresses": [
							"X-testing1lnk637g0edwnqc2tn8tel39652fswa3xk4r65e"
						],
						"locktime": 0,
						"threshold": 1
					},
					"ownedOutput": {
						"addresses": [],
						"locktime": 0,
						"threshold": 0
					}
				}
			}
		]
	},
	"credentials": [
		{
			"fxID": "spdxUxVJQbX85MGxMHbKw1sHxMnSqJ3QBzDyDYEP3h6TLuxqQ",
			"credential": {
				"signatures": [
					"0xbe32eb12a68afae7955b88519c4499ce045f8883076410573fdf00f47139f1533be66ffcce307426a828b8c9a1ea719eb2197bab033e88f0ce05fecdb61d438601"
				]
			}
		},
		{
			"fxID": "rXJsCSEYXg2TehWxCEEGj6JU2PWKTkd6cBdNLjoe2SpsKD9cy",
			"credential": {
				"signatures": [
					"PLACEHOLDER_SIGNATURE"
				]
			}
		},
		{
			"fxID": "rXJsCSEYXg2TehWxCEEGj6JU2PWKTkd6cBdNLjoe2SpsKD9cy",
			"credential": {
				"signatures": [
					"PLACEHOLDER_SIGNATURE"
				]
			}
		}
	],
	"id": "PLACEHOLDER_TX_ID"
}`

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_CREATE_ASSET_TX_ID", createAssetTx.ID().String(), 4)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_TX_ID", mintPropertyFxOpTx.ID().String(), 1)
	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_BLOCKCHAIN_ID", mintPropertyFxOpTx.Unsigned.(*txs.OperationTx).BlockchainID.String(), 1)

	sigStr, err := formatting.Encode(formatting.HexNC, mintPropertyFxOpTx.Creds[1].Credential.(*propertyfx.Credential).Sigs[0][:])
	require.NoError(err)

	expectedReplyTxString = strings.Replace(expectedReplyTxString, "PLACEHOLDER_SIGNATURE", sigStr, 2)

	require.Equal(expectedReplyTxString, string(replyTxBytes))
}

func newAvaxBaseTxWithOutputs(t *testing.T, env *environment) *txs.Tx {
	var (
		memo      = []byte{1, 2, 3, 4, 5, 6, 7, 8}
		key       = keys[0]
		changeKey = keys[1]
		kc        = secp256k1fx.NewKeychain()
	)
	kc.Add(key)

	env.service.txBuilderBackend.ResetAddresses(kc.Addresses())
	tx, _, err := buildBaseTx(
		env.service.txBuilderBackend,
		[]*avax.TransferableOutput{{
			Asset: avax.Asset{ID: env.vm.feeAssetID},
			Out: &secp256k1fx.TransferOutput{
				Amt: units.MicroAvax,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{key.PublicKey().Address()},
				},
			},
		}},
		memo,
		kc,
		commonfees.NoTip,
		changeKey.PublicKey().Address(),
	)
	require.NoError(t, err)
	return tx
}

func newAvaxCreateAssetTxWithOutputs(t *testing.T, env *environment, initialStates map[uint32][]verify.State) *txs.Tx {
	var (
		key = keys[0]
		kc  = secp256k1fx.NewKeychain()
	)
	kc.Add(key)

	tx, _, err := buildCreateAssetTx(
		env.service.txBuilderBackend,
		"Team Rocket", // name
		"TR",          // symbol
		0,             // denomination
		initialStates,
		kc,
		commonfees.NoTip,
		key.Address(),
	)
	require.NoError(t, err)
	return tx
}

func buildTestExportTx(t *testing.T, env *environment, chainID ids.ID) *txs.Tx {
	var (
		key = keys[0]
		kc  = secp256k1fx.NewKeychain()
		to  = key.PublicKey().Address()
	)
	kc.Add(key)

	env.service.txBuilderBackend.ResetAddresses(kc.Addresses())
	tx, _, err := buildExportTx(
		env.service.txBuilderBackend,
		chainID,
		to,
		env.vm.feeAssetID,
		units.MicroAvax,
		kc,
		commonfees.NoTip,
		key.Address(),
	)
	require.NoError(t, err)
	return tx
}

func buildNFTxMintOp(createAssetTx *txs.Tx, key *secp256k1.PrivateKey, outputIndex, groupID uint32) *txs.Operation {
	return &txs.Operation{
		Asset: avax.Asset{ID: createAssetTx.ID()},
		UTXOIDs: []*avax.UTXOID{{
			TxID:        createAssetTx.ID(),
			OutputIndex: outputIndex,
		}},
		Op: &nftfx.MintOperation{
			MintInput: secp256k1fx.Input{
				SigIndices: []uint32{0},
			},
			GroupID: groupID,
			Payload: []byte{'h', 'e', 'l', 'l', 'o'},
			Outputs: []*secp256k1fx.OutputOwners{{
				Threshold: 1,
				Addrs:     []ids.ShortID{key.PublicKey().Address()},
			}},
		},
	}
}

func buildPropertyFxMintOp(createAssetTx *txs.Tx, key *secp256k1.PrivateKey, outputIndex uint32) *txs.Operation {
	return &txs.Operation{
		Asset: avax.Asset{ID: createAssetTx.ID()},
		UTXOIDs: []*avax.UTXOID{{
			TxID:        createAssetTx.ID(),
			OutputIndex: outputIndex,
		}},
		Op: &propertyfx.MintOperation{
			MintInput: secp256k1fx.Input{
				SigIndices: []uint32{0},
			},
			MintOutput: propertyfx.MintOutput{OutputOwners: secp256k1fx.OutputOwners{
				Threshold: 1,
				Addrs: []ids.ShortID{
					key.PublicKey().Address(),
				},
			}},
		},
	}
}

func buildSecpMintOp(createAssetTx *txs.Tx, key *secp256k1.PrivateKey, outputIndex uint32) *txs.Operation {
	return &txs.Operation{
		Asset: avax.Asset{ID: createAssetTx.ID()},
		UTXOIDs: []*avax.UTXOID{{
			TxID:        createAssetTx.ID(),
			OutputIndex: outputIndex,
		}},
		Op: &secp256k1fx.MintOperation{
			MintInput: secp256k1fx.Input{
				SigIndices: []uint32{0},
			},
			MintOutput: secp256k1fx.MintOutput{
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs: []ids.ShortID{
						key.PublicKey().Address(),
					},
				},
			},
			TransferOutput: secp256k1fx.TransferOutput{
				Amt: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Locktime:  0,
					Threshold: 1,
					Addrs:     []ids.ShortID{key.PublicKey().Address()},
				},
			},
		},
	}
}

func buildOperationTxWithOp(t *testing.T, env *environment, ops []*txs.Operation) *txs.Tx {
	var (
		key = keys[0]
		kc  = secp256k1fx.NewKeychain()
	)
	kc.Add(key)

	env.service.txBuilderBackend.ResetAddresses(kc.Addresses())
	tx, err := buildOperation(
		env.service.txBuilderBackend,
		ops,
		kc,
		commonfees.NoTip,
		key.Address(),
	)
	require.NoError(t, err)
	return tx
}

func TestServiceGetNilTx(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	reply := api.GetTxReply{}
	err := env.service.GetTx(nil, &api.GetTxArgs{}, &reply)
	require.ErrorIs(err, errNilTxID)
}

func TestServiceGetUnknownTx(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	reply := api.GetTxReply{}
	err := env.service.GetTx(nil, &api.GetTxArgs{TxID: ids.GenerateTestID()}, &reply)
	require.ErrorIs(err, database.ErrNotFound)
}

func TestServiceGetUTXOs(t *testing.T) {
	env := setup(t, &envConfig{
		fork: latest,
	})
	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(t, env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	rawAddr := ids.GenerateTestShortID()
	rawEmptyAddr := ids.GenerateTestShortID()

	numUTXOs := 10
	// Put a bunch of UTXOs
	for i := 0; i < numUTXOs; i++ {
		utxo := &avax.UTXO{
			UTXOID: avax.UTXOID{
				TxID: ids.GenerateTestID(),
			},
			Asset: avax.Asset{ID: env.vm.ctx.AVAXAssetID},
			Out: &secp256k1fx.TransferOutput{
				Amt: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{rawAddr},
				},
			},
		}
		env.vm.state.AddUTXO(utxo)
	}
	require.NoError(t, env.vm.state.Commit())

	sm := env.sharedMemory.NewSharedMemory(constants.PlatformChainID)

	elems := make([]*atomic.Element, numUTXOs)
	codec := env.vm.parser.Codec()
	for i := range elems {
		utxo := &avax.UTXO{
			UTXOID: avax.UTXOID{
				TxID: ids.GenerateTestID(),
			},
			Asset: avax.Asset{ID: env.vm.ctx.AVAXAssetID},
			Out: &secp256k1fx.TransferOutput{
				Amt: 1,
				OutputOwners: secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs:     []ids.ShortID{rawAddr},
				},
			},
		}

		utxoBytes, err := codec.Marshal(txs.CodecVersion, utxo)
		require.NoError(t, err)
		utxoID := utxo.InputID()
		elems[i] = &atomic.Element{
			Key:   utxoID[:],
			Value: utxoBytes,
			Traits: [][]byte{
				rawAddr.Bytes(),
			},
		}
	}

	require.NoError(t, sm.Apply(map[ids.ID]*atomic.Requests{
		env.vm.ctx.ChainID: {
			PutRequests: elems,
		},
	}))

	hrp := constants.GetHRP(env.vm.ctx.NetworkID)
	xAddr, err := env.vm.FormatLocalAddress(rawAddr)
	require.NoError(t, err)
	pAddr, err := env.vm.FormatAddress(constants.PlatformChainID, rawAddr)
	require.NoError(t, err)
	unknownChainAddr, err := address.Format("R", hrp, rawAddr.Bytes())
	require.NoError(t, err)
	xEmptyAddr, err := env.vm.FormatLocalAddress(rawEmptyAddr)
	require.NoError(t, err)

	env.vm.ctx.Lock.Unlock()

	tests := []struct {
		label       string
		count       int
		expectedErr error
		args        *api.GetUTXOsArgs
	}{
		{
			label:       "invalid address: ''",
			expectedErr: address.ErrNoSeparator,
			args: &api.GetUTXOsArgs{
				Addresses: []string{""},
			},
		},
		{
			label:       "invalid address: '-'",
			expectedErr: bech32.ErrInvalidLength(0),
			args: &api.GetUTXOsArgs{
				Addresses: []string{"-"},
			},
		},
		{
			label:       "invalid address: 'foo'",
			expectedErr: address.ErrNoSeparator,
			args: &api.GetUTXOsArgs{
				Addresses: []string{"foo"},
			},
		},
		{
			label:       "invalid address: 'foo-bar'",
			expectedErr: bech32.ErrInvalidLength(3),
			args: &api.GetUTXOsArgs{
				Addresses: []string{"foo-bar"},
			},
		},
		{
			label:       "invalid address: '<ChainID>'",
			expectedErr: address.ErrNoSeparator,
			args: &api.GetUTXOsArgs{
				Addresses: []string{env.vm.ctx.ChainID.String()},
			},
		},
		{
			label:       "invalid address: '<ChainID>-'",
			expectedErr: bech32.ErrInvalidLength(0),
			args: &api.GetUTXOsArgs{
				Addresses: []string{env.vm.ctx.ChainID.String() + "-"},
			},
		},
		{
			label:       "invalid address: '<Unknown ID>-<addr>'",
			expectedErr: ids.ErrNoIDWithAlias,
			args: &api.GetUTXOsArgs{
				Addresses: []string{unknownChainAddr},
			},
		},
		{
			label:       "no addresses",
			expectedErr: errNoAddresses,
			args:        &api.GetUTXOsArgs{},
		},
		{
			label: "get all X-chain UTXOs",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
			},
		},
		{
			label: "get one X-chain UTXO",
			count: 1,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				Limit: 1,
			},
		},
		{
			label: "limit greater than number of UTXOs",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				Limit: avajson.Uint32(numUTXOs + 1),
			},
		},
		{
			label: "no utxos to return",
			count: 0,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xEmptyAddr,
				},
			},
		},
		{
			label: "multiple address with utxos",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xEmptyAddr,
					xAddr,
				},
			},
		},
		{
			label: "get all P-chain UTXOs",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				SourceChain: "P",
			},
		},
		{
			label:       "invalid source chain ID",
			expectedErr: ids.ErrNoIDWithAlias,
			count:       numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				SourceChain: "HomeRunDerby",
			},
		},
		{
			label: "get all P-chain UTXOs",
			count: numUTXOs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
				},
				SourceChain: "P",
			},
		},
		{
			label:       "get UTXOs from multiple chains",
			expectedErr: avax.ErrMismatchedChainIDs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					xAddr,
					pAddr,
				},
			},
		},
		{
			label:       "get UTXOs for an address on a different chain",
			expectedErr: avax.ErrMismatchedChainIDs,
			args: &api.GetUTXOsArgs{
				Addresses: []string{
					pAddr,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			require := require.New(t)
			reply := &api.GetUTXOsReply{}
			err := env.service.GetUTXOs(nil, test.args, reply)
			require.ErrorIs(err, test.expectedErr)
			if test.expectedErr != nil {
				return
			}
			require.Len(reply.UTXOs, test.count)
		})
	}
}

func TestGetAssetDescription(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	avaxAssetID := env.genesisTx.ID()

	reply := GetAssetDescriptionReply{}
	require.NoError(env.service.GetAssetDescription(nil, &GetAssetDescriptionArgs{
		AssetID: avaxAssetID.String(),
	}, &reply))

	require.Equal("AVAX", reply.Name)
	require.Equal("SYMB", reply.Symbol)
}

func TestGetBalance(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		fork: latest,
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	avaxAssetID := env.genesisTx.ID()

	reply := GetBalanceReply{}
	addrStr, err := env.vm.FormatLocalAddress(keys[0].PublicKey().Address())
	require.NoError(err)
	require.NoError(env.service.GetBalance(nil, &GetBalanceArgs{
		Address: addrStr,
		AssetID: avaxAssetID.String(),
	}, &reply))

	require.Equal(startBalance, uint64(reply.Balance))
}

func TestCreateFixedCapAsset(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require := require.New(t)

			env := setup(t, &envConfig{
				isCustomFeeAsset: !tc.avaxAsset,
				keystoreUsers: []*user{{
					username:    username,
					password:    password,
					initialKeys: keys,
				}},
			})
			env.vm.ctx.Lock.Unlock()

			defer func() {
				env.vm.ctx.Lock.Lock()
				require.NoError(env.vm.Shutdown(context.Background()))
				env.vm.ctx.Lock.Unlock()
			}()

			reply := AssetIDChangeAddr{}
			addrStr, err := env.vm.FormatLocalAddress(keys[0].PublicKey().Address())
			require.NoError(err)

			changeAddrStr, err := env.vm.FormatLocalAddress(testChangeAddr)
			require.NoError(err)
			_, fromAddrsStr := sampleAddrs(t, env.vm.AddressManager, addrs)

			require.NoError(env.service.CreateFixedCapAsset(nil, &CreateAssetArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				Name:         "testAsset",
				Symbol:       "TEST",
				Denomination: 1,
				InitialHolders: []*Holder{{
					Amount:  123456789,
					Address: addrStr,
				}},
			}, &reply))
			require.Equal(changeAddrStr, reply.ChangeAddr)
		})
	}
}

func TestCreateVariableCapAsset(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require := require.New(t)

			env := setup(t, &envConfig{
				isCustomFeeAsset: !tc.avaxAsset,
				keystoreUsers: []*user{{
					username:    username,
					password:    password,
					initialKeys: keys,
				}},
			})
			env.vm.ctx.Lock.Unlock()

			defer func() {
				env.vm.ctx.Lock.Lock()
				require.NoError(env.vm.Shutdown(context.Background()))
				env.vm.ctx.Lock.Unlock()
			}()

			reply := AssetIDChangeAddr{}
			minterAddrStr, err := env.vm.FormatLocalAddress(keys[0].PublicKey().Address())
			require.NoError(err)
			_, fromAddrsStr := sampleAddrs(t, env.vm.AddressManager, addrs)
			changeAddrStr := fromAddrsStr[0]

			require.NoError(env.service.CreateVariableCapAsset(nil, &CreateAssetArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				Name:   "test asset",
				Symbol: "TEST",
				MinterSets: []Owners{
					{
						Threshold: 1,
						Minters: []string{
							minterAddrStr,
						},
					},
				},
			}, &reply))
			require.Equal(changeAddrStr, reply.ChangeAddr)

			buildAndAccept(require, env.vm, env.issuer, reply.AssetID)

			createdAssetID := reply.AssetID.String()
			// Test minting of the created variable cap asset
			mintArgs := &MintArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				Amount:  200,
				AssetID: createdAssetID,
				To:      minterAddrStr, // Send newly minted tokens to this address
			}
			mintReply := &api.JSONTxIDChangeAddr{}
			require.NoError(env.service.Mint(nil, mintArgs, mintReply))
			require.Equal(changeAddrStr, mintReply.ChangeAddr)

			buildAndAccept(require, env.vm, env.issuer, mintReply.TxID)

			sendArgs := &SendArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: []string{minterAddrStr}},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				SendOutput: SendOutput{
					Amount:  200,
					AssetID: createdAssetID,
					To:      fromAddrsStr[0],
				},
			}
			sendReply := &api.JSONTxIDChangeAddr{}
			require.NoError(env.service.Send(nil, sendArgs, sendReply))
			require.Equal(changeAddrStr, sendReply.ChangeAddr)
		})
	}
}

func TestNFTWorkflow(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require := require.New(t)

			env := setup(t, &envConfig{
				fork:             eUpgrade,
				isCustomFeeAsset: !tc.avaxAsset,
				keystoreUsers: []*user{{
					username:    username,
					password:    password,
					initialKeys: keys,
				}},
			})
			env.vm.ctx.Lock.Unlock()

			defer func() {
				env.vm.ctx.Lock.Lock()
				require.NoError(env.vm.Shutdown(context.Background()))
				env.vm.ctx.Lock.Unlock()
			}()

			_, fromAddrsStr := sampleAddrs(t, env.vm.AddressManager, addrs)

			// Test minting of the created variable cap asset
			addrStr, err := env.vm.FormatLocalAddress(keys[0].PublicKey().Address())
			require.NoError(err)

			createArgs := &CreateNFTAssetArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: fromAddrsStr[0]},
				},
				Name:   "BIG COIN",
				Symbol: "COIN",
				MinterSets: []Owners{
					{
						Threshold: 1,
						Minters: []string{
							addrStr,
						},
					},
				},
			}
			createReply := &AssetIDChangeAddr{}
			require.NoError(env.service.CreateNFTAsset(nil, createArgs, createReply))
			require.Equal(fromAddrsStr[0], createReply.ChangeAddr)

			buildAndAccept(require, env.vm, env.issuer, createReply.AssetID)

			assetID := createReply.AssetID
			payload, err := formatting.Encode(formatting.Hex, []byte{1, 2, 3, 4, 5})
			require.NoError(err)
			mintArgs := &MintNFTArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: fromAddrsStr[0]},
				},
				AssetID:  assetID.String(),
				Payload:  payload,
				To:       addrStr,
				Encoding: formatting.Hex,
			}
			mintReply := &api.JSONTxIDChangeAddr{}

			require.NoError(env.service.MintNFT(nil, mintArgs, mintReply))
			require.Equal(fromAddrsStr[0], createReply.ChangeAddr)

			// Accept the transaction so that we can send the newly minted NFT
			buildAndAccept(require, env.vm, env.issuer, mintReply.TxID)

			sendArgs := &SendNFTArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: fromAddrsStr[0]},
				},
				AssetID: assetID.String(),
				GroupID: 0,
				To:      addrStr,
			}
			sendReply := &api.JSONTxIDChangeAddr{}
			require.NoError(env.service.SendNFT(nil, sendArgs, sendReply))
			require.Equal(fromAddrsStr[0], sendReply.ChangeAddr)
		})
	}
}

func TestImportExportKey(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		keystoreUsers: []*user{{
			username: username,
			password: password,
		}},
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	sk, err := secp256k1.NewPrivateKey()
	require.NoError(err)

	importArgs := &ImportKeyArgs{
		UserPass: api.UserPass{
			Username: username,
			Password: password,
		},
		PrivateKey: sk,
	}
	importReply := &api.JSONAddress{}
	require.NoError(env.service.ImportKey(nil, importArgs, importReply))

	addrStr, err := env.vm.FormatLocalAddress(sk.PublicKey().Address())
	require.NoError(err)
	exportArgs := &ExportKeyArgs{
		UserPass: api.UserPass{
			Username: username,
			Password: password,
		},
		Address: addrStr,
	}
	exportReply := &ExportKeyReply{}
	require.NoError(env.service.ExportKey(nil, exportArgs, exportReply))
	require.Equal(sk.Bytes(), exportReply.PrivateKey.Bytes())
}

func TestImportAVMKeyNoDuplicates(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		keystoreUsers: []*user{{
			username: username,
			password: password,
		}},
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	sk, err := secp256k1.NewPrivateKey()
	require.NoError(err)
	args := ImportKeyArgs{
		UserPass: api.UserPass{
			Username: username,
			Password: password,
		},
		PrivateKey: sk,
	}
	reply := api.JSONAddress{}
	require.NoError(env.service.ImportKey(nil, &args, &reply))

	expectedAddress, err := env.vm.FormatLocalAddress(sk.PublicKey().Address())
	require.NoError(err)

	require.Equal(expectedAddress, reply.Address)

	reply2 := api.JSONAddress{}
	require.NoError(env.service.ImportKey(nil, &args, &reply2))

	require.Equal(expectedAddress, reply2.Address)

	addrsArgs := api.UserPass{
		Username: username,
		Password: password,
	}
	addrsReply := api.JSONAddresses{}
	require.NoError(env.service.ListAddresses(nil, &addrsArgs, &addrsReply))

	require.Len(addrsReply.Addresses, 1)
	require.Equal(expectedAddress, addrsReply.Addresses[0])
}

func TestSend(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		keystoreUsers: []*user{{
			username:    username,
			password:    password,
			initialKeys: keys,
		}},
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	assetID := env.genesisTx.ID()
	addr := keys[0].PublicKey().Address()

	addrStr, err := env.vm.FormatLocalAddress(addr)
	require.NoError(err)
	changeAddrStr, err := env.vm.FormatLocalAddress(testChangeAddr)
	require.NoError(err)
	_, fromAddrsStr := sampleAddrs(t, env.vm.AddressManager, addrs)

	args := &SendArgs{
		JSONSpendHeader: api.JSONSpendHeader{
			UserPass: api.UserPass{
				Username: username,
				Password: password,
			},
			JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
			JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
		},
		SendOutput: SendOutput{
			Amount:  500,
			AssetID: assetID.String(),
			To:      addrStr,
		},
	}
	reply := &api.JSONTxIDChangeAddr{}
	require.NoError(env.service.Send(nil, args, reply))
	require.Equal(changeAddrStr, reply.ChangeAddr)

	buildAndAccept(require, env.vm, env.issuer, reply.TxID)
}

func TestSendMultiple(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require := require.New(t)

			env := setup(t, &envConfig{
				isCustomFeeAsset: !tc.avaxAsset,
				keystoreUsers: []*user{{
					username:    username,
					password:    password,
					initialKeys: keys,
				}},
				vmStaticConfig: &config.Config{
					EUpgradeTime: time.Time{},
				},
			})
			env.vm.ctx.Lock.Unlock()

			defer func() {
				env.vm.ctx.Lock.Lock()
				require.NoError(env.vm.Shutdown(context.Background()))
				env.vm.ctx.Lock.Unlock()
			}()

			assetID := env.genesisTx.ID()
			addr := keys[0].PublicKey().Address()

			addrStr, err := env.vm.FormatLocalAddress(addr)
			require.NoError(err)
			changeAddrStr, err := env.vm.FormatLocalAddress(testChangeAddr)
			require.NoError(err)
			_, fromAddrsStr := sampleAddrs(t, env.vm.AddressManager, addrs)

			args := &SendMultipleArgs{
				JSONSpendHeader: api.JSONSpendHeader{
					UserPass: api.UserPass{
						Username: username,
						Password: password,
					},
					JSONFromAddrs:  api.JSONFromAddrs{From: fromAddrsStr},
					JSONChangeAddr: api.JSONChangeAddr{ChangeAddr: changeAddrStr},
				},
				Outputs: []SendOutput{
					{
						Amount:  500,
						AssetID: assetID.String(),
						To:      addrStr,
					},
					{
						Amount:  1000,
						AssetID: assetID.String(),
						To:      addrStr,
					},
				},
			}
			reply := &api.JSONTxIDChangeAddr{}
			require.NoError(env.service.SendMultiple(nil, args, reply))
			require.Equal(changeAddrStr, reply.ChangeAddr)

			buildAndAccept(require, env.vm, env.issuer, reply.TxID)
		})
	}
}

func TestCreateAndListAddresses(t *testing.T) {
	require := require.New(t)

	env := setup(t, &envConfig{
		keystoreUsers: []*user{{
			username: username,
			password: password,
		}},
	})
	env.vm.ctx.Lock.Unlock()

	defer func() {
		env.vm.ctx.Lock.Lock()
		require.NoError(env.vm.Shutdown(context.Background()))
		env.vm.ctx.Lock.Unlock()
	}()

	createArgs := &api.UserPass{
		Username: username,
		Password: password,
	}
	createReply := &api.JSONAddress{}

	require.NoError(env.service.CreateAddress(nil, createArgs, createReply))

	newAddr := createReply.Address

	listArgs := &api.UserPass{
		Username: username,
		Password: password,
	}
	listReply := &api.JSONAddresses{}

	require.NoError(env.service.ListAddresses(nil, listArgs, listReply))
	require.Contains(listReply.Addresses, newAddr)
}

func TestImport(t *testing.T) {
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require := require.New(t)

			env := setup(t, &envConfig{
				isCustomFeeAsset: !tc.avaxAsset,
				keystoreUsers: []*user{{
					username:    username,
					password:    password,
					initialKeys: keys,
				}},
			})

			defer func() {
				env.vm.ctx.Lock.Lock()
				require.NoError(env.vm.Shutdown(context.Background()))
				env.vm.ctx.Lock.Unlock()
			}()
			assetID := env.genesisTx.ID()
			addr0 := keys[0].PublicKey().Address()

			utxo := &avax.UTXO{
				UTXOID: avax.UTXOID{TxID: ids.Empty},
				Asset:  avax.Asset{ID: assetID},
				Out: &secp256k1fx.TransferOutput{
					Amt: 7,
					OutputOwners: secp256k1fx.OutputOwners{
						Threshold: 1,
						Addrs:     []ids.ShortID{addr0},
					},
				},
			}
			utxoBytes, err := env.vm.parser.Codec().Marshal(txs.CodecVersion, utxo)
			require.NoError(err)

			peerSharedMemory := env.sharedMemory.NewSharedMemory(constants.PlatformChainID)
			utxoID := utxo.InputID()
			require.NoError(peerSharedMemory.Apply(map[ids.ID]*atomic.Requests{
				env.vm.ctx.ChainID: {
					PutRequests: []*atomic.Element{{
						Key:   utxoID[:],
						Value: utxoBytes,
						Traits: [][]byte{
							addr0.Bytes(),
						},
					}},
				},
			}))

			env.vm.ctx.Lock.Unlock()

			addrStr, err := env.vm.FormatLocalAddress(keys[0].PublicKey().Address())
			require.NoError(err)
			args := &ImportArgs{
				UserPass: api.UserPass{
					Username: username,
					Password: password,
				},
				SourceChain: "P",
				To:          addrStr,
			}
			reply := &api.JSONTxID{}
			require.NoError(env.service.Import(nil, args, reply))
		})
	}
}

func TestServiceGetBlock(t *testing.T) {
	ctrl := gomock.NewController(t)

	blockID := ids.GenerateTestID()

	type test struct {
		name                        string
		serviceAndExpectedBlockFunc func(t *testing.T, ctrl *gomock.Controller) (*Service, interface{})
		encoding                    formatting.Encoding
		expectedErr                 error
	}

	tests := []test{
		{
			name: "chain not linearized",
			serviceAndExpectedBlockFunc: func(*testing.T, *gomock.Controller) (*Service, interface{}) {
				return &Service{
					vm: &VM{
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: errNotLinearized,
		},
		{
			name: "block not found",
			serviceAndExpectedBlockFunc: func(_ *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(nil, database.ErrNotFound)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: database.ErrNotFound,
		},
		{
			name: "JSON format",
			serviceAndExpectedBlockFunc: func(_ *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				block := block.NewMockBlock(ctrl)
				block.EXPECT().InitCtx(gomock.Any())
				block.EXPECT().Txs().Return(nil)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, block
			},
			encoding:    formatting.JSON,
			expectedErr: nil,
		},
		{
			name: "hex format",
			serviceAndExpectedBlockFunc: func(t *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				block := block.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				expected, err := formatting.Encode(formatting.Hex, blockBytes)
				require.NoError(t, err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.Hex,
			expectedErr: nil,
		},
		{
			name: "hexc format",
			serviceAndExpectedBlockFunc: func(t *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				block := block.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				expected, err := formatting.Encode(formatting.HexC, blockBytes)
				require.NoError(t, err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.HexC,
			expectedErr: nil,
		},
		{
			name: "hexnc format",
			serviceAndExpectedBlockFunc: func(t *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				block := block.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				expected, err := formatting.Encode(formatting.HexNC, blockBytes)
				require.NoError(t, err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.HexNC,
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			service, expected := tt.serviceAndExpectedBlockFunc(t, ctrl)

			args := &api.GetBlockArgs{
				BlockID:  blockID,
				Encoding: tt.encoding,
			}
			reply := &api.GetBlockResponse{}
			err := service.GetBlock(nil, args, reply)
			require.ErrorIs(err, tt.expectedErr)
			if tt.expectedErr != nil {
				return
			}
			require.Equal(tt.encoding, reply.Encoding)

			expectedJSON, err := json.Marshal(expected)
			require.NoError(err)

			require.Equal(json.RawMessage(expectedJSON), reply.Block)
		})
	}
}

func TestServiceGetBlockByHeight(t *testing.T) {
	ctrl := gomock.NewController(t)

	blockID := ids.GenerateTestID()
	blockHeight := uint64(1337)

	type test struct {
		name                        string
		serviceAndExpectedBlockFunc func(t *testing.T, ctrl *gomock.Controller) (*Service, interface{})
		encoding                    formatting.Encoding
		expectedErr                 error
	}

	tests := []test{
		{
			name: "chain not linearized",
			serviceAndExpectedBlockFunc: func(*testing.T, *gomock.Controller) (*Service, interface{}) {
				return &Service{
					vm: &VM{
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: errNotLinearized,
		},
		{
			name: "block height not found",
			serviceAndExpectedBlockFunc: func(_ *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				state := state.NewMockState(ctrl)
				state.EXPECT().GetBlockIDAtHeight(blockHeight).Return(ids.Empty, database.ErrNotFound)

				manager := executor.NewMockManager(ctrl)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: database.ErrNotFound,
		},
		{
			name: "block not found",
			serviceAndExpectedBlockFunc: func(_ *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				state := state.NewMockState(ctrl)
				state.EXPECT().GetBlockIDAtHeight(blockHeight).Return(blockID, nil)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(nil, database.ErrNotFound)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, nil
			},
			encoding:    formatting.Hex,
			expectedErr: database.ErrNotFound,
		},
		{
			name: "JSON format",
			serviceAndExpectedBlockFunc: func(_ *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				block := block.NewMockBlock(ctrl)
				block.EXPECT().InitCtx(gomock.Any())
				block.EXPECT().Txs().Return(nil)

				state := state.NewMockState(ctrl)
				state.EXPECT().GetBlockIDAtHeight(blockHeight).Return(blockID, nil)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, block
			},
			encoding:    formatting.JSON,
			expectedErr: nil,
		},
		{
			name: "hex format",
			serviceAndExpectedBlockFunc: func(t *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				block := block.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				state := state.NewMockState(ctrl)
				state.EXPECT().GetBlockIDAtHeight(blockHeight).Return(blockID, nil)

				expected, err := formatting.Encode(formatting.Hex, blockBytes)
				require.NoError(t, err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.Hex,
			expectedErr: nil,
		},
		{
			name: "hexc format",
			serviceAndExpectedBlockFunc: func(t *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				block := block.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				state := state.NewMockState(ctrl)
				state.EXPECT().GetBlockIDAtHeight(blockHeight).Return(blockID, nil)

				expected, err := formatting.Encode(formatting.HexC, blockBytes)
				require.NoError(t, err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.HexC,
			expectedErr: nil,
		},
		{
			name: "hexnc format",
			serviceAndExpectedBlockFunc: func(t *testing.T, ctrl *gomock.Controller) (*Service, interface{}) {
				block := block.NewMockBlock(ctrl)
				blockBytes := []byte("hi mom")
				block.EXPECT().Bytes().Return(blockBytes)

				state := state.NewMockState(ctrl)
				state.EXPECT().GetBlockIDAtHeight(blockHeight).Return(blockID, nil)

				expected, err := formatting.Encode(formatting.HexNC, blockBytes)
				require.NoError(t, err)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}, expected
			},
			encoding:    formatting.HexNC,
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)

			service, expected := tt.serviceAndExpectedBlockFunc(t, ctrl)

			args := &api.GetBlockByHeightArgs{
				Height:   avajson.Uint64(blockHeight),
				Encoding: tt.encoding,
			}
			reply := &api.GetBlockResponse{}
			err := service.GetBlockByHeight(nil, args, reply)
			require.ErrorIs(err, tt.expectedErr)
			if tt.expectedErr != nil {
				return
			}
			require.Equal(tt.encoding, reply.Encoding)

			expectedJSON, err := json.Marshal(expected)
			require.NoError(err)

			require.Equal(json.RawMessage(expectedJSON), reply.Block)
		})
	}
}

func TestServiceGetHeight(t *testing.T) {
	ctrl := gomock.NewController(t)

	blockID := ids.GenerateTestID()
	blockHeight := uint64(1337)

	type test struct {
		name        string
		serviceFunc func(ctrl *gomock.Controller) *Service
		expectedErr error
	}

	tests := []test{
		{
			name: "chain not linearized",
			serviceFunc: func(*gomock.Controller) *Service {
				return &Service{
					vm: &VM{
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}
			},
			expectedErr: errNotLinearized,
		},
		{
			name: "block not found",
			serviceFunc: func(ctrl *gomock.Controller) *Service {
				state := state.NewMockState(ctrl)
				state.EXPECT().GetLastAccepted().Return(blockID)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(nil, database.ErrNotFound)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}
			},
			expectedErr: database.ErrNotFound,
		},
		{
			name: "happy path",
			serviceFunc: func(ctrl *gomock.Controller) *Service {
				state := state.NewMockState(ctrl)
				state.EXPECT().GetLastAccepted().Return(blockID)

				block := block.NewMockBlock(ctrl)
				block.EXPECT().Height().Return(blockHeight)

				manager := executor.NewMockManager(ctrl)
				manager.EXPECT().GetStatelessBlock(blockID).Return(block, nil)
				return &Service{
					vm: &VM{
						state:        state,
						chainManager: manager,
						ctx: &snow.Context{
							Log: logging.NoLog{},
						},
					},
				}
			},
			expectedErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			service := tt.serviceFunc(ctrl)

			reply := &api.GetHeightResponse{}
			err := service.GetHeight(nil, nil, reply)
			require.ErrorIs(err, tt.expectedErr)
			if tt.expectedErr != nil {
				return
			}
			require.Equal(avajson.Uint64(blockHeight), reply.Height)
		})
	}
}
