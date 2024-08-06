// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"time"

	"github.com/CaiJiJi/avalanchego/chains/atomic"
	"github.com/CaiJiJi/avalanchego/ids"
	"github.com/CaiJiJi/avalanchego/utils/set"
	"github.com/CaiJiJi/avalanchego/vms/platformvm/block"
	"github.com/CaiJiJi/avalanchego/vms/platformvm/state"
)

type proposalBlockState struct {
	onDecisionState state.Diff
	onCommitState   state.Diff
	onAbortState    state.Diff
}

// The state of a block.
// Note that not all fields will be set for a given block.
type blockState struct {
	proposalBlockState
	statelessBlock block.Block

	onAcceptState state.Diff
	onAcceptFunc  func()

	inputs          set.Set[ids.ID]
	timestamp       time.Time
	atomicRequests  map[ids.ID]*atomic.Requests
	verifiedHeights set.Set[uint64]
}
