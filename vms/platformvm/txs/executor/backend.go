// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package executor

import (
	"github.com/CaiJiJi/avalanchego/snow"
	"github.com/CaiJiJi/avalanchego/snow/uptime"
	"github.com/CaiJiJi/avalanchego/utils"
	"github.com/CaiJiJi/avalanchego/utils/timer/mockable"
	"github.com/CaiJiJi/avalanchego/vms/platformvm/config"
	"github.com/CaiJiJi/avalanchego/vms/platformvm/fx"
	"github.com/CaiJiJi/avalanchego/vms/platformvm/reward"
	"github.com/CaiJiJi/avalanchego/vms/platformvm/utxo"
)

type Backend struct {
	Config       *config.Config
	Ctx          *snow.Context
	Clk          *mockable.Clock
	Fx           fx.Fx
	FlowChecker  utxo.Verifier
	Uptimes      uptime.Calculator
	Rewards      reward.Calculator
	Bootstrapped *utils.Atomic[bool]
}
