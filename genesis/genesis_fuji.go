// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package genesis

import (
	"time"

	_ "embed"

	"github.com/CaiJiJi/avalanchego/utils/units"
	"github.com/CaiJiJi/avalanchego/vms/platformvm/reward"

	feecomponent "github.com/CaiJiJi/avalanchego/vms/components/fee"
	txfee "github.com/CaiJiJi/avalanchego/vms/platformvm/txs/fee"
)

var (
	//go:embed genesis_fuji.json
	fujiGenesisConfigJSON []byte

	// FujiParams are the params used for the fuji testnet
	FujiParams = Params{
		TxFeeConfig: TxFeeConfig{
			CreateAssetTxFee: 10 * units.MilliAvax,
			StaticFeeConfig: txfee.StaticConfig{
				TxFee:                         units.MilliAvax,
				CreateSubnetTxFee:             100 * units.MilliAvax,
				TransformSubnetTxFee:          1 * units.Avax,
				CreateBlockchainTxFee:         100 * units.MilliAvax,
				AddPrimaryNetworkValidatorFee: 0,
				AddPrimaryNetworkDelegatorFee: 0,
				AddSubnetValidatorFee:         units.MilliAvax,
				AddSubnetDelegatorFee:         units.MilliAvax,
			},
			// TODO: Set these values to something more reasonable
			DynamicFeeConfig: feecomponent.Config{
				Weights: feecomponent.Dimensions{
					feecomponent.Bandwidth: 1,
					feecomponent.DBRead:    1,
					feecomponent.DBWrite:   1,
					feecomponent.Compute:   1,
				},
				MaxGasCapacity:           1_000_000,
				MaxGasPerSecond:          1_000,
				TargetGasPerSecond:       500,
				MinGasPrice:              1,
				ExcessConversionConstant: 1,
			},
		},
		StakingConfig: StakingConfig{
			UptimeRequirement: .8, // 80%
			MinValidatorStake: 1 * units.Avax,
			MaxValidatorStake: 3 * units.MegaAvax,
			MinDelegatorStake: 1 * units.Avax,
			MinDelegationFee:  20000, // 2%
			MinStakeDuration:  24 * time.Hour,
			MaxStakeDuration:  365 * 24 * time.Hour,
			RewardConfig: reward.Config{
				MaxConsumptionRate: .12 * reward.PercentDenominator,
				MinConsumptionRate: .10 * reward.PercentDenominator,
				MintingPeriod:      365 * 24 * time.Hour,
				SupplyCap:          720 * units.MegaAvax,
			},
		},
	}
)
