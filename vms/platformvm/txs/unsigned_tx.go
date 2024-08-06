// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package txs

import (
	"github.com/CaiJiJi/avalanchego/ids"
	"github.com/CaiJiJi/avalanchego/snow"
	"github.com/CaiJiJi/avalanchego/utils/set"
	"github.com/CaiJiJi/avalanchego/vms/components/avax"
	"github.com/CaiJiJi/avalanchego/vms/secp256k1fx"
)

// UnsignedTx is an unsigned transaction
type UnsignedTx interface {
	// TODO: Remove this initialization pattern from both the platformvm and the
	// avm.
	snow.ContextInitializable
	secp256k1fx.UnsignedTx
	SetBytes(unsignedBytes []byte)

	// InputIDs returns the set of inputs this transaction consumes
	InputIDs() set.Set[ids.ID]

	Outputs() []*avax.TransferableOutput

	// Attempts to verify this transaction without any provided state.
	SyntacticVerify(ctx *snow.Context) error

	// Visit calls [visitor] with this transaction's concrete type
	Visit(visitor Visitor) error
}
