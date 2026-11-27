//go:build !harness && !botlive

package core

import "testing"

// TestBondReservesEachAsset checks that a wallet that can bond but is not a
// bond asset does not end updateBondReserves before the bond asset's wallet is
// reached. Core ranges over its wallets map, whose order Go randomizes: with
// two wallets the other comes first in about 1 pass in 8 (measured on Go
// 1.25), so 100 passes put it first in all but about 1 run in 600 000.
func TestBondReservesEachAsset(t *testing.T) {
	const feeRate = 50

	rig := newTestRig()
	defer rig.shutdown()
	acct := rig.dc.acct
	acct.targetTier = 1
	acct.bondAsset = tUTXOAssetA.ID

	dcrWallet, tDcrWallet := newTWallet(tUTXOAssetA.ID)
	dcrWallet.Wallet = &TFeeRater{tDcrWallet, feeRate}
	rig.core.wallets[tUTXOAssetA.ID] = dcrWallet
	btcWallet, tBtcWallet := newTWallet(tUTXOAssetB.ID)
	rig.core.wallets[tUTXOAssetB.ID] = btcWallet

	// Double the bond: core reserves for the bond about to be posted too.
	want := dcrBondAsset.Amt*acct.targetTier*2 + tDcrWallet.BondsFeeBuffer(feeRate)
	for i := 0; i < 100; i++ {
		tDcrWallet.reserves.Store(0)
		tBtcWallet.reserves.Store(1)
		rig.core.updateBondReserves()
		if got := tDcrWallet.reserves.Load(); got != want {
			t.Fatalf("pass %d: bond asset's reserves %d, want %d", i, got, want)
		}
		if got := tBtcWallet.reserves.Load(); got != 0 {
			t.Fatalf("pass %d: other bonder's reserves %d, want 0", i, got)
		}
	}
}
