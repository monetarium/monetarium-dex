// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package importall

import (
	"testing"

	"decred.org/dcrdex/dex"
	"decred.org/dcrdex/server/asset"
)

// TestVARRegistered checks that importing importall registers the VAR backend
// next to DCR, so dcrdex can serve VAR markets. The test does not import the
// drivers itself, so it fails if the blank import goes away.
func TestVARRegistered(t *testing.T) {
	for _, symbol := range []string{"var", "dcr"} {
		id, ok := dex.BipSymbolID(symbol)
		if !ok {
			t.Fatalf("no BIP id for %s", symbol)
		}
		if _, err := asset.UnitInfo(id); err != nil {
			t.Fatalf("%s: %v", symbol, err)
		}
	}
}
