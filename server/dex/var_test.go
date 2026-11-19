// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package dex

import (
	"fmt"
	"strings"
	"testing"

	"decred.org/dcrdex/dex"
)

// TestVarMaxFeeRateBound is item 3 of docs/var-port/invariants.md. The node
// refuses a VAR transaction whose fee exceeds max(size × 1000, 1 000 000)
// atoms unless high fees are allowed, and the DEX does not allow them. With the
// dust change a swap leaves in its fee, a VAR maxFeeRate above 600 atoms/B can
// cross that ceiling, so it must be refused when the config loads.
func TestVarMaxFeeRateBound(t *testing.T) {
	conf := func(varRate, btcRate uint64) string {
		return fmt.Sprintf(`{
	"markets": [{"base": "VAR_simnet", "quote": "BTC_simnet", "lotSize": 100000000,
		"parcelSize": 1, "rateStep": 100, "epochDuration": 6000}],
	"assets": {
		"VAR_simnet": {"bip44symbol": "var", "network": "simnet", "maxFeeRate": %d, "swapConf": 1},
		"BTC_simnet": {"bip44symbol": "btc", "network": "simnet", "maxFeeRate": %d, "swapConf": 1}
	}
}`, varRate, btcRate)
	}
	load := func(varRate, btcRate uint64) error {
		_, _, err := loadMarketConf(dex.Simnet, strings.NewReader(conf(varRate, btcRate)))
		return err
	}

	if err := load(600, 1001); err != nil {
		t.Fatalf("VAR at 600 atoms/B (BTC at 1001) refused: %v", err)
	}
	if err := load(601, 100); err == nil {
		t.Fatal("VAR maxFeeRate of 601 atoms/B accepted")
	}
}
