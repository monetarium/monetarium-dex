// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package dex

import (
	"fmt"

	"decred.org/dcrdex/dex"
)

// varMaxFeeRate is the highest maxFeeRate, in atoms/B, a VAR asset may set.
// The Monetarium node refuses a fee above max(size × 1000, 1 000 000) atoms
// unless high fees are allowed, and the DEX does not allow them. A swap pays
// more than size × rate: change the client would call dust (under 606 × rate
// for a 37-byte output) is left in the fee instead of a 37-byte change output,
// so the fee can reach (size + 643) × rate − 1. The tightest size is 1000
// bytes, where the ceiling is still the flat 1 000 000, which allows at most
// 608 (1 000 000 / 1643); 600 leaves a little margin. The node sets the
// ceiling at 100 times its --minrelaytxfee, so this holds at the default of 10
// atoms/B.
const varMaxFeeRate = 600

// varMinFeeRate is the lowest maxFeeRate, in atoms/B, a VAR asset may set: the
// node's default minimum relay fee. The market caps each swap's fee rate at
// maxFeeRate, so below it a swap at the client's default fee bump pays under
// the node's minimum and is refused. It copies dex/networks/mon
// MinRelayFeeRate: importing that package here would put the node's modules
// into the go.mod and go.sum of every module that builds server/dex
// (dex/testing/loadbot, client/cmd/bisonw-desktop), and even a test-only import
// adds their checksums to go.sum.
const varMinFeeRate = 10

// checkVarMaxFeeRate refuses a VAR asset whose maxFeeRate could take a swap
// over the node's fee ceiling or under its minimum relay fee.
func checkVarMaxFeeRate(assetID uint32, maxFeeRate uint64) error {
	if varID, _ := dex.BipSymbolID("var"); assetID != varID {
		return nil
	}
	switch {
	case maxFeeRate > varMaxFeeRate:
		return fmt.Errorf("max fee rate of %d atoms/B for VAR is above %d, where a swap can exceed the node's fee ceiling", maxFeeRate, varMaxFeeRate)
	case maxFeeRate < varMinFeeRate:
		return fmt.Errorf("max fee rate of %d atoms/B for VAR is below the node's minimum relay fee of %d", maxFeeRate, varMinFeeRate)
	}
	return nil
}
