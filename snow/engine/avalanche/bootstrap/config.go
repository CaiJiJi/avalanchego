// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package bootstrap

import (
	"github.com/CaiJiJi/avalanchego/ids"
	"github.com/CaiJiJi/avalanchego/network/p2p"
	"github.com/CaiJiJi/avalanchego/snow"
	"github.com/CaiJiJi/avalanchego/snow/engine/avalanche/bootstrap/queue"
	"github.com/CaiJiJi/avalanchego/snow/engine/avalanche/vertex"
	"github.com/CaiJiJi/avalanchego/snow/engine/common"
	"github.com/CaiJiJi/avalanchego/snow/engine/common/tracker"
)

type Config struct {
	common.AllGetsServer

	Ctx *snow.ConsensusContext

	StartupTracker tracker.Startup
	Sender         common.Sender

	// PeerTracker manages the set of nodes that we fetch the next block from.
	PeerTracker *p2p.PeerTracker

	// This node will only consider the first [AncestorsMaxContainersReceived]
	// containers in an ancestors message it receives.
	AncestorsMaxContainersReceived int

	// VtxBlocked tracks operations that are blocked on vertices
	VtxBlocked *queue.JobsWithMissing
	// TxBlocked tracks operations that are blocked on transactions
	TxBlocked *queue.Jobs

	Manager vertex.Manager
	VM      vertex.LinearizableVM

	// If StopVertexID is empty, the engine will generate the stop vertex based
	// on the current state.
	StopVertexID ids.ID
}
