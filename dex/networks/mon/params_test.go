// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package mon

import "testing"

func TestUnitInfo(t *testing.T) {
	if u := UnitInfo.Conventional.Unit; u != "VAR" {
		t.Errorf("conventional unit %q, want VAR", u)
	}
	for _, d := range UnitInfo.Alternatives {
		if len(d.Unit) < 3 || d.Unit[len(d.Unit)-3:] != "VAR" {
			t.Errorf("alternative unit %q is not a VAR denomination", d.Unit)
		}
	}
}
