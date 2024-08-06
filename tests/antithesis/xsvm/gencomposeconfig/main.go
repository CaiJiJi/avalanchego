// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"log"

	"github.com/CaiJiJi/avalanchego/genesis"
	"github.com/CaiJiJi/avalanchego/tests/antithesis"
	"github.com/CaiJiJi/avalanchego/tests/fixture/subnet"
	"github.com/CaiJiJi/avalanchego/tests/fixture/tmpnet"
)

const baseImageName = "antithesis-xsvm"

// Creates docker-compose.yml and its associated volumes in the target path.
func main() {
	network := tmpnet.LocalNetworkOrPanic()
	network.Subnets = []*tmpnet.Subnet{
		subnet.NewXSVMOrPanic("xsvm", genesis.VMRQKey, network.Nodes...),
	}
	if err := antithesis.GenerateComposeConfig(network, baseImageName); err != nil {
		log.Fatalf("failed to generate compose config: %v", err)
	}
}
