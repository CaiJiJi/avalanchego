// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package subprocess

import (
	"context"
	"fmt"
	"sync"

	"github.com/ava-labs/avalanchego/version"
	"github.com/ava-labs/avalanchego/vms/rpcchainvm/runtime"
)

var _ runtime.Initializer = (*initializer)(nil)

// Subprocess VM Runtime intializer.
type initializer struct {
	path string

	once sync.Once
	// Address of the RPC Chain VM server
	vmAddr string
	// Error, if one occurred, during Initialization
	err error
	// Initialized is closed once Initialize is called
	initialized chan struct{}
}

func newInitializer(path string) *initializer {
	return &initializer{
		path:        path,
		initialized: make(chan struct{}),
	}
}

func (i *initializer) Initialize(_ context.Context, protocolVersion uint, vmAddr string) error {
	i.once.Do(func() {
		if version.RPCChainVMProtocol != protocolVersion {
			i.err = &errProtocolVersionMismatchDetails{
				current:                         version.Current,
				rpcChainVMProtocolVer:           version.RPCChainVMProtocol,
				vmLocation:                      i.path,
				vmLocationRpcChainVMProtocolVer: protocolVersion,
			}
		}
		i.vmAddr = vmAddr
		close(i.initialized)
	})
	return i.err
}

type errProtocolVersionMismatchDetails struct {
	current                         *version.Semantic
	rpcChainVMProtocolVer           uint
	vmLocation                      string
	vmLocationRpcChainVMProtocolVer uint
}

func (e *errProtocolVersionMismatchDetails) Unwrap() error {
	return runtime.ErrProtocolVersionMismatch
}

func (e *errProtocolVersionMismatchDetails) Error() string {
	return fmt.Sprintf("%q. AvalancheGo version %s implements RPCChainVM protocol version %d. The VM located at %q implements RPCChainVM protocol version %d. Please make sure that there is an exact match of the protocol versions. This can be achieved by updating your VM or running an older/newer version of AvalancheGo. Please be advised that some virtual machines may not yet support the latest RPCChainVM protocol version",
		runtime.ErrProtocolVersionMismatch,
		e.current,
		e.rpcChainVMProtocolVer,
		e.vmLocation,
		e.vmLocationRpcChainVMProtocolVer,
	)
}
