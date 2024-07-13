// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package fee

import (
	"errors"
	"fmt"
	"time"
)

var (
	errZeroLeakGasCoeff     = errors.New("zero leak gas coefficient")
	errUnexpectedBlockTimes = errors.New("unexpected block times")
)

type DynamicFeesConfig struct {
	// At this state this is the fixed gas price applied to each block
	// In the next PRs, gas price will float and this will become the
	// minimum gas price
	GasPrice GasPrice `json:"gas-price"`

	// weights to merge fees dimensions complexities into a single gas value
	FeeDimensionWeights Dimensions `json:"fee-dimension-weights"`

	// Leaky bucket parameters to calculate gas cap
	MaxGasPerSecond Gas // techically the unit of measure is Gas/sec, but picking Gas reduces casts needed
	LeakGasCoeff    Gas // techically the unit of measure is sec^{-1}, but picking Gas reduces casts needed
}

func (c *DynamicFeesConfig) Validate() error {
	if c.LeakGasCoeff == 0 {
		return errZeroLeakGasCoeff
	}

	return nil
}

// We cap the maximum gas consumed by time with a leaky bucket approach
// GasCap = min (GasCap + MaxGasPerSecond/LeakGasCoeff*ElapsedTime, MaxGasPerSecond)
func GasCap(cfg DynamicFeesConfig, currentGasCapacity Gas, parentBlkTime, childBlkTime time.Time) (Gas, error) {
	if parentBlkTime.Compare(childBlkTime) > 0 {
		return ZeroGas, fmt.Errorf("%w, parentBlkTim %v, childBlkTime %v", errUnexpectedBlockTimes, parentBlkTime, childBlkTime)
	}

	elapsedTime := uint64(childBlkTime.Unix() - parentBlkTime.Unix())
	if elapsedTime > uint64(cfg.LeakGasCoeff) {
		return cfg.MaxGasPerSecond, nil
	}

	return min(cfg.MaxGasPerSecond, currentGasCapacity+cfg.MaxGasPerSecond*Gas(elapsedTime)/cfg.LeakGasCoeff), nil
}

func UpdateGasCap(currentGasCap, blkGas Gas) Gas {
	nextGasCap := Gas(0)
	if currentGasCap > blkGas {
		nextGasCap = currentGasCap - blkGas
	}
	return nextGasCap
}
