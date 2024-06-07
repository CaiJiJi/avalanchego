// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package p

import (
	"fmt"
	"math"

	"github.com/stretchr/testify/require"

	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/tests"
	"github.com/ava-labs/avalanchego/tests/fixture/e2e"
	"github.com/ava-labs/avalanchego/tests/fixture/tmpnet"
	"github.com/ava-labs/avalanchego/vms/platformvm"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"

	commonfees "github.com/ava-labs/avalanchego/vms/components/fees"
	ginkgo "github.com/onsi/ginkgo/v2"
)

var _ = ginkgo.Describe("[Dynamic Fees]", func() {
	require := require.New(ginkgo.GinkgoT())

	ginkgo.It("should ensure that the dynamic multifees are affected by load", func() {
		customDynamicFeesConfig := commonfees.DynamicFeesConfig{
			MinFeeRate:         commonfees.Dimensions{1, 1, 1, 1},
			UpdateDenominators: commonfees.Dimensions{5, 2, 2, 3},
			BlockMaxComplexity: commonfees.Dimensions{math.MaxUint64, math.MaxUint64, math.MaxUint64, math.MaxUint64},

			// BlockUnitsTarget are set to cause an increase of fees while simple transactions are issued
			BlockTargetComplexityRate: commonfees.Dimensions{120, 30, 60, 300},
		}

		ginkgo.By("creating a new private network to ensure isolation from other tests")
		privateNetwork := &tmpnet.Network{
			Owner: "avalanchego-e2e-dynamic-fees",
			ChainConfigs: map[string]tmpnet.FlagsMap{
				"P": {
					"dynamic-fees-config": customDynamicFeesConfig,
				},
			},
		}
		e2e.Env.StartPrivateNetwork(privateNetwork)

		ginkgo.By("setup a wallet and a P-chain client")
		node := privateNetwork.Nodes[0]
		nodeURI := tmpnet.NodeURI{
			NodeID: node.NodeID,
			URI:    node.URI,
		}
		keychain := secp256k1fx.NewKeychain(privateNetwork.PreFundedKeys...)
		baseWallet := e2e.NewWallet(keychain, nodeURI)
		pWallet := baseWallet.P()
		pChainClient := platformvm.NewClient(nodeURI.URI)

		// retrieve initial balances
		pBuilder := pWallet.Builder()
		pContext := pBuilder.Context()
		avaxAssetID := pContext.AVAXAssetID
		pBalances, err := pWallet.Builder().GetBalance()
		require.NoError(err)
		pStartBalance := pBalances[avaxAssetID]
		tests.Outf("{{blue}} P-chain balance before P->X export: %d {{/}}\n", pStartBalance)

		ginkgo.By("checking that initial fee values match with configured ones", func() {
			nextFeeRates, err := pChainClient.GetNextFeeRates(e2e.DefaultContext())
			require.NoError(err)
			require.Equal(customDynamicFeesConfig.MinFeeRate, nextFeeRates)
		})

		ginkgo.By("issue expensive transactions so to increase the fee rates to be paid for accepting the transactons",
			func() {
				initialOwner := &secp256k1fx.OutputOwners{
					Threshold: 1,
					Addrs: []ids.ShortID{
						keychain.Keys[0].Address(),
					},
				}

				var subnetID ids.ID
				ginkgo.By("create a permissioned subnet", func() {
					subnetTx, err := pWallet.IssueCreateSubnetTx(
						initialOwner,
						e2e.WithDefaultContext(),
					)
					require.NoError(err)

					subnetID = subnetTx.ID()
				})

				nextFeeRates, err := pChainClient.GetNextFeeRates(e2e.DefaultContext())
				require.NoError(err)
				tests.Outf("{{blue}} next fee rates: %v {{/}}\n", nextFeeRates)

				ginkgo.By("repeatedly change the permissioned subnet owner to increase fee rates", func() {
					txsCount := 10
					for i := 0; i < txsCount; i++ {
						nextOwner := &secp256k1fx.OutputOwners{
							Threshold: 1,
							Addrs: []ids.ShortID{
								keychain.Keys[1].Address(),
							},
						}

						_, err = pWallet.IssueTransferSubnetOwnershipTx(
							subnetID,
							nextOwner,
							e2e.WithDefaultContext(),
						)
						require.NoError(err)

						updatedFeeRates, err := pChainClient.GetNextFeeRates(e2e.DefaultContext())
						require.NoError(err)
						tests.Outf("{{blue}} current fee rates: %v {{/}}\n", updatedFeeRates)

						ginkgo.By("check that fee rates components have increased")
						require.True(
							commonfees.Compare(nextFeeRates, updatedFeeRates),
							fmt.Sprintf("previous fee rates %v, current fee rates %v", nextFeeRates, updatedFeeRates),
						)
						nextFeeRates = updatedFeeRates
					}
				})

				ginkgo.By("wait for the fee rates to decrease", func() {
					initialFeeRates := nextFeeRates
					e2e.Eventually(func() bool {
						var err error
						nextFeeRates, err = pChainClient.GetNextFeeRates(e2e.DefaultContext())
						require.NoError(err)
						tests.Outf("{{blue}} next fee rates: %v {{/}}\n", nextFeeRates)

						ratesStrictlyLower := false
						for i := 0; i < len(initialFeeRates); i++ {
							if nextFeeRates[i] < initialFeeRates[i] {
								ratesStrictlyLower = true
								break
							}
						}

						return ratesStrictlyLower
					}, e2e.DefaultTimeout, e2e.DefaultPollingInterval, "failed to see gas price decrease before timeout")
					tests.Outf("\n{{blue}}fee rates have decreased to %v{{/}}\n", nextFeeRates)
				})
			},
		)
	})
})
