// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package importall

import (
	"testing"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/dex"
)

// TestVARRegistered checks that importing importall registers the VAR driver
// next to DCR. The drivers are looked up by symbol, not imported here, so the
// test fails if the blank import goes away.
func TestVARRegistered(t *testing.T) {
	for _, symbol := range []string{"var", "dcr"} {
		id, ok := dex.BipSymbolID(symbol)
		if !ok {
			t.Fatalf("no BIP id for %s", symbol)
		}
		info, err := asset.Info(id)
		if err != nil {
			t.Fatalf("%s: %v", symbol, err)
		}
		if symbol == "var" && info.Name != "Monetarium" {
			t.Fatalf("%s wallet is named %q", symbol, info.Name)
		}
	}
}
