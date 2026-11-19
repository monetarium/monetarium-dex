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
// 608 (1 000 000 / 1643); 600 leaves a little margin. The node sets the ceiling at 100 times its
// --minrelaytxfee, so this holds at the default of 10 atoms/B.
const varMaxFeeRate = 600

// checkVarMaxFeeRate refuses a VAR asset whose maxFeeRate could take a swap
// over the node's fee ceiling.
func checkVarMaxFeeRate(assetID uint32, maxFeeRate uint64) error {
	if varID, _ := dex.BipSymbolID("var"); assetID == varID && maxFeeRate > varMaxFeeRate {
		return fmt.Errorf("max fee rate of %d atoms/B for VAR is above %d, where a swap can exceed the node's fee ceiling", maxFeeRate, varMaxFeeRate)
	}
	return nil
}
