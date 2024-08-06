// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/snow/snowtest"
	"github.com/ava-labs/avalanchego/upgrade"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/timer/mockable"
	"github.com/ava-labs/avalanchego/vms/avm/config"
	"github.com/ava-labs/avalanchego/vms/avm/fxs"
	"github.com/ava-labs/avalanchego/vms/avm/txs"
	"github.com/ava-labs/avalanchego/vms/avm/txs/fee"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/components/verify"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"
)

var (
	keys      = secp256k1.TestKeys()
	feeConfig = config.Config{
		StaticConfig: fee.StaticConfig{
			TxFee:            2,
			CreateAssetTxFee: 3,
		},
		Upgrades: upgrade.Config{
			EtnaTime: mockable.MaxTime,
		},
	}
)

func TestSyntacticVerifierBaseTx(t *testing.T) {
	ctx := snowtest.Context(t, snowtest.XChainID)

	fx := &secp256k1fx.Fx{}
	parser, err := txs.NewParser(
		[]fxs.Fx{
			fx,
		},
	)
	require.NoError(t, err)

	feeAssetID := ids.GenerateTestID()
	asset := avax.Asset{
		ID: feeAssetID,
	}
	outputOwners := secp256k1fx.OutputOwners{
		Threshold: 1,
		Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
	}
	fxOutput := secp256k1fx.TransferOutput{
		Amt:          12345,
		OutputOwners: outputOwners,
	}
	output := avax.TransferableOutput{
		Asset: asset,
		Out:   &fxOutput,
	}
	inputTxID := ids.GenerateTestID()
	utxoID := avax.UTXOID{
		TxID:        inputTxID,
		OutputIndex: 0,
	}
	inputSigners := secp256k1fx.Input{
		SigIndices: []uint32{2},
	}
	fxInput := secp256k1fx.TransferInput{
		Amt:   54321,
		Input: inputSigners,
	}
	input := avax.TransferableInput{
		UTXOID: utxoID,
		Asset:  asset,
		In:     &fxInput,
	}
	baseTx := avax.BaseTx{
		NetworkID:    constants.UnitTestID,
		BlockchainID: ctx.ChainID,
		Outs: []*avax.TransferableOutput{
			&output,
		},
		Ins: []*avax.TransferableInput{
			&input,
		},
	}
	cred := fxs.FxCredential{
		Credential: &secp256k1fx.Credential{},
	}
	creds := []*fxs.FxCredential{
		&cred,
	}

	codec := parser.Codec()
	backend := &Backend{
		Ctx:    ctx,
		Config: &feeConfig,
		Fxs: []*fxs.ParsedFx{
			{
				ID: secp256k1fx.ID,
				Fx: fx,
			},
		},
		Codec:      codec,
		FeeAssetID: feeAssetID,
	}

	tests := []struct {
		name   string
		txFunc func() *txs.Tx
		err    error
	}{
		{
			name: "valid",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &txs.BaseTx{BaseTx: baseTx},
					Creds:    creds,
				}
			},
			err: nil,
		},
		{
			name: "wrong networkID",
			txFunc: func() *txs.Tx {
				baseTx := baseTx
				baseTx.NetworkID++
				return &txs.Tx{
					Unsigned: &txs.BaseTx{BaseTx: baseTx},
					Creds:    creds,
				}
			},
			err: avax.ErrWrongNetworkID,
		},
		{
			name: "wrong chainID",
			txFunc: func() *txs.Tx {
				baseTx := baseTx
				baseTx.BlockchainID = ids.GenerateTestID()
				return &txs.Tx{
					Unsigned: &txs.BaseTx{BaseTx: baseTx},
					Creds:    creds,
				}
			},
			err: avax.ErrWrongChainID,
		},
		{
			name: "memo too large",
			txFunc: func() *txs.Tx {
				baseTx := baseTx
				baseTx.Memo = make([]byte, avax.MaxMemoSize+1)
				return &txs.Tx{
					Unsigned: &txs.BaseTx{BaseTx: baseTx},
					Creds:    creds,
				}
			},
			err: avax.ErrMemoTooLarge,
		},
		{
			name: "invalid credential",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &txs.BaseTx{BaseTx: baseTx},
					Creds: []*fxs.FxCredential{{
						Credential: (*secp256k1fx.Credential)(nil),
					}},
				}
			},
			err: secp256k1fx.ErrNilCredential,
		},
		{
			name: "wrong number of credentials",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &txs.BaseTx{BaseTx: baseTx},
				}
			},
			err: errWrongNumberOfCredentials,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := test.txFunc()
			verifier := &SyntacticVerifier{
				Backend: backend,
				Tx:      tx,
			}
			err := tx.Unsigned.Visit(verifier)
			require.ErrorIs(t, err, test.err)
		})
	}
}

func TestSyntacticVerifierCreateAssetTx(t *testing.T) {
	ctx := snowtest.Context(t, snowtest.XChainID)

	fx := &secp256k1fx.Fx{}
	parser, err := txs.NewParser(
		[]fxs.Fx{
			fx,
		},
	)
	require.NoError(t, err)

	feeAssetID := ids.GenerateTestID()
	asset := avax.Asset{
		ID: feeAssetID,
	}
	outputOwners := secp256k1fx.OutputOwners{
		Threshold: 1,
		Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
	}
	fxOutput := secp256k1fx.TransferOutput{
		Amt:          12345,
		OutputOwners: outputOwners,
	}
	output := avax.TransferableOutput{
		Asset: asset,
		Out:   &fxOutput,
	}
	inputTxID := ids.GenerateTestID()
	utxoID := avax.UTXOID{
		TxID:        inputTxID,
		OutputIndex: 0,
	}
	inputSigners := secp256k1fx.Input{
		SigIndices: []uint32{2},
	}
	fxInput := secp256k1fx.TransferInput{
		Amt:   54321,
		Input: inputSigners,
	}
	input := avax.TransferableInput{
		UTXOID: utxoID,
		Asset:  asset,
		In:     &fxInput,
	}
	baseTx := avax.BaseTx{
		NetworkID:    constants.UnitTestID,
		BlockchainID: ctx.ChainID,
		Outs: []*avax.TransferableOutput{
			&output,
		},
		Ins: []*avax.TransferableInput{
			&input,
		},
	}
	initialState := txs.InitialState{
		FxIndex: 0,
		Outs: []verify.State{
			&fxOutput,
		},
	}
	tx := txs.CreateAssetTx{
		BaseTx:       txs.BaseTx{BaseTx: baseTx},
		Name:         "NormalName",
		Symbol:       "TICK",
		Denomination: byte(2),
		States: []*txs.InitialState{
			&initialState,
		},
	}
	cred := fxs.FxCredential{
		Credential: &secp256k1fx.Credential{},
	}
	creds := []*fxs.FxCredential{
		&cred,
	}

	codec := parser.Codec()
	backend := &Backend{
		Ctx:    ctx,
		Config: &feeConfig,
		Fxs: []*fxs.ParsedFx{
			{
				ID: secp256k1fx.ID,
				Fx: fx,
			},
		},
		Codec:      codec,
		FeeAssetID: feeAssetID,
	}

	tests := []struct {
		name   string
		txFunc func() *txs.Tx
		err    error
	}{
		{
			name: "valid",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: nil,
		},
		{
			name: "name too short",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Name = ""
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errNameTooShort,
		},
		{
			name: "name too long",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Name = strings.Repeat("X", maxNameLen+1)
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errNameTooLong,
		},
		{
			name: "symbol too short",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Symbol = ""
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errSymbolTooShort,
		},
		{
			name: "symbol too long",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Symbol = strings.Repeat("X", maxSymbolLen+1)
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errSymbolTooLong,
		},
		{
			name: "no feature extensions",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.States = nil
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errNoFxs,
		},
		{
			name: "denomination too large",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Denomination = maxDenomination + 1
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errDenominationTooLarge,
		},
		{
			name: "bounding whitespace in name",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Name = " AVAX"
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errUnexpectedWhitespace,
		},
		{
			name: "illegal character in name",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Name = "h8*32"
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errIllegalNameCharacter,
		},
		{
			name: "illegal character in ticker",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Symbol = "H I"
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errIllegalSymbolCharacter,
		},
		{
			name: "wrong networkID",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.NetworkID++
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrWrongNetworkID,
		},
		{
			name: "wrong chainID",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.BlockchainID = ids.GenerateTestID()
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrWrongChainID,
		},
		{
			name: "memo too large",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Memo = make([]byte, avax.MaxMemoSize+1)
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrMemoTooLarge,
		},
		{
			name: "invalid nil state",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.States = []*txs.InitialState{
					nil,
				}
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: txs.ErrNilInitialState,
		},
		{
			name: "invalid fx",
			txFunc: func() *txs.Tx {
				initialState := initialState
				initialState.FxIndex = 1

				tx := tx
				tx.States = []*txs.InitialState{
					&initialState,
				}
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: txs.ErrUnknownFx,
		},
		{
			name: "invalid nil state output",
			txFunc: func() *txs.Tx {
				initialState := initialState
				initialState.Outs = []verify.State{
					nil,
				}

				tx := tx
				tx.States = []*txs.InitialState{
					&initialState,
				}
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: txs.ErrNilFxOutput,
		},
		{
			name: "invalid state output",
			txFunc: func() *txs.Tx {
				fxOutput := fxOutput
				fxOutput.Amt = 0

				initialState := initialState
				initialState.Outs = []verify.State{
					&fxOutput,
				}

				tx := tx
				tx.States = []*txs.InitialState{
					&initialState,
				}
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: secp256k1fx.ErrNoValueOutput,
		},
		{
			name: "unsorted initial state",
			txFunc: func() *txs.Tx {
				fxOutput0 := fxOutput

				fxOutput1 := fxOutput
				fxOutput1.Amt++

				initialState := initialState
				initialState.Outs = []verify.State{
					&fxOutput0,
					&fxOutput1,
				}
				initialState.Sort(codec)
				initialState.Outs[0], initialState.Outs[1] = initialState.Outs[1], initialState.Outs[0]

				tx := tx
				tx.States = []*txs.InitialState{
					&initialState,
				}
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: txs.ErrOutputsNotSorted,
		},
		{
			name: "non-unique initial states",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.States = []*txs.InitialState{
					&initialState,
					&initialState,
				}
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errInitialStatesNotSortedUnique,
		},
		{
			name: "invalid credential",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
					Creds: []*fxs.FxCredential{{
						Credential: (*secp256k1fx.Credential)(nil),
					}},
				}
			},
			err: secp256k1fx.ErrNilCredential,
		},
		{
			name: "wrong number of credentials",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
				}
			},
			err: errWrongNumberOfCredentials,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := test.txFunc()
			verifier := &SyntacticVerifier{
				Backend: backend,
				Tx:      tx,
			}
			err := tx.Unsigned.Visit(verifier)
			require.ErrorIs(t, err, test.err)
		})
	}
}

func TestSyntacticVerifierOperationTx(t *testing.T) {
	ctx := snowtest.Context(t, snowtest.XChainID)

	fx := &secp256k1fx.Fx{}
	parser, err := txs.NewParser(
		[]fxs.Fx{
			fx,
		},
	)
	require.NoError(t, err)

	feeAssetID := ids.GenerateTestID()
	asset := avax.Asset{
		ID: feeAssetID,
	}
	outputOwners := secp256k1fx.OutputOwners{
		Threshold: 1,
		Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
	}
	fxOutput := secp256k1fx.TransferOutput{
		Amt:          12345,
		OutputOwners: outputOwners,
	}
	output := avax.TransferableOutput{
		Asset: asset,
		Out:   &fxOutput,
	}
	inputTxID := ids.GenerateTestID()
	utxoID := avax.UTXOID{
		TxID:        inputTxID,
		OutputIndex: 0,
	}
	inputSigners := secp256k1fx.Input{
		SigIndices: []uint32{2},
	}
	fxInput := secp256k1fx.TransferInput{
		Amt:   54321,
		Input: inputSigners,
	}
	input := avax.TransferableInput{
		UTXOID: utxoID,
		Asset:  asset,
		In:     &fxInput,
	}
	baseTx := avax.BaseTx{
		NetworkID:    constants.UnitTestID,
		BlockchainID: ctx.ChainID,
		Ins: []*avax.TransferableInput{
			&input,
		},
		Outs: []*avax.TransferableOutput{
			&output,
		},
	}
	opUTXOID := utxoID
	opUTXOID.OutputIndex++
	fxOp := secp256k1fx.MintOperation{
		MintInput: inputSigners,
		MintOutput: secp256k1fx.MintOutput{
			OutputOwners: outputOwners,
		},
		TransferOutput: fxOutput,
	}
	op := txs.Operation{
		Asset: asset,
		UTXOIDs: []*avax.UTXOID{
			&opUTXOID,
		},
		Op: &fxOp,
	}
	tx := txs.OperationTx{
		BaseTx: txs.BaseTx{BaseTx: baseTx},
		Ops: []*txs.Operation{
			&op,
		},
	}
	cred := fxs.FxCredential{
		Credential: &secp256k1fx.Credential{},
	}
	creds := []*fxs.FxCredential{
		&cred,
		&cred,
	}

	codec := parser.Codec()
	backend := &Backend{
		Ctx:    ctx,
		Config: &feeConfig,
		Fxs: []*fxs.ParsedFx{
			{
				ID: secp256k1fx.ID,
				Fx: fx,
			},
		},
		Codec:      codec,
		FeeAssetID: feeAssetID,
	}

	tests := []struct {
		name   string
		txFunc func() *txs.Tx
		err    error
	}{
		{
			name: "valid",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: nil,
		},
		{
			name: "no operation",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Ops = nil
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errNoOperations,
		},
		{
			name: "wrong networkID",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.NetworkID++
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrWrongNetworkID,
		},
		{
			name: "wrong chainID",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.BlockchainID = ids.GenerateTestID()
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrWrongChainID,
		},
		{
			name: "memo too large",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Memo = make([]byte, avax.MaxMemoSize+1)
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrMemoTooLarge,
		},
		{
			name: "invalid nil op",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Ops = []*txs.Operation{
					nil,
				}
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: txs.ErrNilOperation,
		},
		{
			name: "invalid nil fx op",
			txFunc: func() *txs.Tx {
				op := op
				op.Op = nil

				tx := tx
				tx.Ops = []*txs.Operation{
					&op,
				}
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: txs.ErrNilFxOperation,
		},
		{
			name: "invalid duplicated op UTXOs",
			txFunc: func() *txs.Tx {
				op := op
				op.UTXOIDs = []*avax.UTXOID{
					&opUTXOID,
					&opUTXOID,
				}

				tx := tx
				tx.Ops = []*txs.Operation{
					&op,
				}
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: txs.ErrNotSortedAndUniqueUTXOIDs,
		},
		{
			name: "invalid duplicated UTXOs across ops",
			txFunc: func() *txs.Tx {
				newOp := op
				op.Asset.ID = ids.GenerateTestID()

				tx := tx
				tx.Ops = []*txs.Operation{
					&op,
					&newOp,
				}
				txs.SortOperations(tx.Ops, codec)
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errDoubleSpend,
		},
		{
			name: "invalid duplicated op",
			txFunc: func() *txs.Tx {
				op := op
				op.UTXOIDs = nil

				tx := tx
				tx.Ops = []*txs.Operation{
					&op,
					&op,
				}
				txs.SortOperations(tx.Ops, codec)
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errOperationsNotSortedUnique,
		},
		{
			name: "invalid credential",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
					Creds: []*fxs.FxCredential{{
						Credential: (*secp256k1fx.Credential)(nil),
					}},
				}
			},
			err: secp256k1fx.ErrNilCredential,
		},
		{
			name: "wrong number of credentials",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
				}
			},
			err: errWrongNumberOfCredentials,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := test.txFunc()
			verifier := &SyntacticVerifier{
				Backend: backend,
				Tx:      tx,
			}
			err := tx.Unsigned.Visit(verifier)
			require.ErrorIs(t, err, test.err)
		})
	}
}

func TestSyntacticVerifierImportTx(t *testing.T) {
	ctx := snowtest.Context(t, snowtest.XChainID)

	fx := &secp256k1fx.Fx{}
	parser, err := txs.NewParser(
		[]fxs.Fx{
			fx,
		},
	)
	require.NoError(t, err)

	feeAssetID := ids.GenerateTestID()
	asset := avax.Asset{
		ID: feeAssetID,
	}
	outputOwners := secp256k1fx.OutputOwners{
		Threshold: 1,
		Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
	}
	fxOutput := secp256k1fx.TransferOutput{
		Amt:          12345,
		OutputOwners: outputOwners,
	}
	output := avax.TransferableOutput{
		Asset: asset,
		Out:   &fxOutput,
	}
	inputTxID := ids.GenerateTestID()
	utxoID := avax.UTXOID{
		TxID:        inputTxID,
		OutputIndex: 0,
	}
	inputSigners := secp256k1fx.Input{
		SigIndices: []uint32{2},
	}
	fxInput := secp256k1fx.TransferInput{
		Amt:   54321,
		Input: inputSigners,
	}
	input := avax.TransferableInput{
		UTXOID: utxoID,
		Asset:  asset,
		In:     &fxInput,
	}
	baseTx := avax.BaseTx{
		NetworkID:    constants.UnitTestID,
		BlockchainID: ctx.ChainID,
		Outs: []*avax.TransferableOutput{
			&output,
		},
	}
	tx := txs.ImportTx{
		BaseTx:      txs.BaseTx{BaseTx: baseTx},
		SourceChain: ctx.CChainID,
		ImportedIns: []*avax.TransferableInput{
			&input,
		},
	}
	cred := fxs.FxCredential{
		Credential: &secp256k1fx.Credential{},
	}
	creds := []*fxs.FxCredential{
		&cred,
	}

	codec := parser.Codec()
	backend := &Backend{
		Ctx:    ctx,
		Config: &feeConfig,
		Fxs: []*fxs.ParsedFx{
			{
				ID: secp256k1fx.ID,
				Fx: fx,
			},
		},
		Codec:      codec,
		FeeAssetID: feeAssetID,
	}

	tests := []struct {
		name   string
		txFunc func() *txs.Tx
		err    error
	}{
		{
			name: "valid",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: nil,
		},
		{
			name: "no imported inputs",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.ImportedIns = nil
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errNoImportInputs,
		},
		{
			name: "wrong networkID",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.NetworkID++
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrWrongNetworkID,
		},
		{
			name: "wrong chainID",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.BlockchainID = ids.GenerateTestID()
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrWrongChainID,
		},
		{
			name: "memo too large",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Memo = make([]byte, avax.MaxMemoSize+1)
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrMemoTooLarge,
		},
		{
			name: "invalid credential",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
					Creds: []*fxs.FxCredential{{
						Credential: (*secp256k1fx.Credential)(nil),
					}},
				}
			},
			err: secp256k1fx.ErrNilCredential,
		},
		{
			name: "wrong number of credentials",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
				}
			},
			err: errWrongNumberOfCredentials,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := test.txFunc()
			verifier := &SyntacticVerifier{
				Backend: backend,
				Tx:      tx,
			}
			err := tx.Unsigned.Visit(verifier)
			require.ErrorIs(t, err, test.err)
		})
	}
}

func TestSyntacticVerifierExportTx(t *testing.T) {
	ctx := snowtest.Context(t, snowtest.XChainID)

	fx := &secp256k1fx.Fx{}
	parser, err := txs.NewParser(
		[]fxs.Fx{
			fx,
		},
	)
	require.NoError(t, err)

	feeAssetID := ids.GenerateTestID()
	asset := avax.Asset{
		ID: feeAssetID,
	}
	outputOwners := secp256k1fx.OutputOwners{
		Threshold: 1,
		Addrs:     []ids.ShortID{keys[0].PublicKey().Address()},
	}
	fxOutput := secp256k1fx.TransferOutput{
		Amt:          12345,
		OutputOwners: outputOwners,
	}
	output := avax.TransferableOutput{
		Asset: asset,
		Out:   &fxOutput,
	}
	inputTxID := ids.GenerateTestID()
	utxoID := avax.UTXOID{
		TxID:        inputTxID,
		OutputIndex: 0,
	}
	inputSigners := secp256k1fx.Input{
		SigIndices: []uint32{2},
	}
	fxInput := secp256k1fx.TransferInput{
		Amt:   54321,
		Input: inputSigners,
	}
	input := avax.TransferableInput{
		UTXOID: utxoID,
		Asset:  asset,
		In:     &fxInput,
	}
	baseTx := avax.BaseTx{
		NetworkID:    constants.UnitTestID,
		BlockchainID: ctx.ChainID,
		Ins: []*avax.TransferableInput{
			&input,
		},
	}
	tx := txs.ExportTx{
		BaseTx:           txs.BaseTx{BaseTx: baseTx},
		DestinationChain: ctx.CChainID,
		ExportedOuts: []*avax.TransferableOutput{
			&output,
		},
	}
	cred := fxs.FxCredential{
		Credential: &secp256k1fx.Credential{},
	}
	creds := []*fxs.FxCredential{
		&cred,
	}

	codec := parser.Codec()
	backend := &Backend{
		Ctx:    ctx,
		Config: &feeConfig,
		Fxs: []*fxs.ParsedFx{
			{
				ID: secp256k1fx.ID,
				Fx: fx,
			},
		},
		Codec:      codec,
		FeeAssetID: feeAssetID,
	}

	tests := []struct {
		name   string
		txFunc func() *txs.Tx
		err    error
	}{
		{
			name: "valid",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: nil,
		},
		{
			name: "no exported outputs",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.ExportedOuts = nil
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: errNoExportOutputs,
		},
		{
			name: "wrong networkID",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.NetworkID++
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrWrongNetworkID,
		},
		{
			name: "wrong chainID",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.BlockchainID = ids.GenerateTestID()
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrWrongChainID,
		},
		{
			name: "memo too large",
			txFunc: func() *txs.Tx {
				tx := tx
				tx.Memo = make([]byte, avax.MaxMemoSize+1)
				return &txs.Tx{
					Unsigned: &tx,
					Creds:    creds,
				}
			},
			err: avax.ErrMemoTooLarge,
		},
		{
			name: "invalid credential",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
					Creds: []*fxs.FxCredential{{
						Credential: (*secp256k1fx.Credential)(nil),
					}},
				}
			},
			err: secp256k1fx.ErrNilCredential,
		},
		{
			name: "wrong number of credentials",
			txFunc: func() *txs.Tx {
				return &txs.Tx{
					Unsigned: &tx,
				}
			},
			err: errWrongNumberOfCredentials,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tx := test.txFunc()
			verifier := &SyntacticVerifier{
				Backend: backend,
				Tx:      tx,
			}
			err := tx.Unsigned.Visit(verifier)
			require.ErrorIs(t, err, test.err)
		})
	}
}
