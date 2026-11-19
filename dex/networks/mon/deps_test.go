// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package mon_test

import (
	"os/exec"
	"sort"
	"strings"
	"testing"

	// Each root is linked in, so that a change to it invalidates go test's
	// cached result; the go list child process is invisible to the cache.
	_ "decred.org/dcrdex/dex/networks/mon"
	_ "decred.org/dcrdex/server/asset/mon"
)

// TestNoDecredTxCode checks that no DCRDEX package in the VAR port's build
// imports Decred's wire or txscript. Monetarium outputs carry a coin type and
// its sighash commits to it, so a VAR transaction built or signed with Decred's
// packages gets another txid or an invalid signature.
//
// The forbidden packages are still in the transitive set, through the importers
// listed below, so the check is on who imports them.
func TestNoDecredTxCode(t *testing.T) {
	// The VAR packages. client/asset/mon joins when it lands, also
	// blank-imported above.
	roots := []string{
		"decred.org/dcrdex/dex/networks/mon",
		"decred.org/dcrdex/server/asset/mon",
	}
	forbidden := map[string]bool{
		"github.com/decred/dcrd/wire":             true,
		"github.com/decred/dcrd/txscript/v4":      true,
		"github.com/decred/dcrd/txscript/v4/sign": true,
	}
	// DCRDEX packages allowed to import them.
	allowedDEX := map[string]string{
		// Its own script helpers; the client driver reaches it through
		// client/asset/btc, which the DCR driver imports for its tx database.
		"decred.org/dcrdex/dex/networks/btc": "BTC script helpers, not VAR transactions",
	}
	// Non-DCRDEX importers seen so far. A new one fails the test, so that it is
	// read before it is added here.
	knownOther := map[string]bool{}

	args := append([]string{"list", "-deps", "-f", "{{.ImportPath}}{{range .Imports}} {{.}}{{end}}"}, roots...)
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("go list: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("go list: %v", err)
	}

	var bad, newOther []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		pkg, imports := fields[0], fields[1:]
		for _, imp := range imports {
			if !forbidden[imp] {
				continue
			}
			switch {
			case strings.HasPrefix(pkg, "decred.org/dcrdex/"):
				if _, ok := allowedDEX[pkg]; !ok {
					bad = append(bad, pkg+" imports "+imp)
				}
			case !knownOther[pkg]:
				newOther = append(newOther, pkg+" imports "+imp)
			}
		}
	}
	sort.Strings(bad)
	sort.Strings(newOther)
	for _, s := range bad {
		t.Errorf("DCRDEX package on Decred tx code: %s", s)
	}
	for _, s := range newOther {
		t.Errorf("new non-DCRDEX importer, check it and add it to knownOther: %s", s)
	}
}
