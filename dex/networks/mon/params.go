// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package mon

import "decred.org/dcrdex/dex"

var UnitInfo = dex.UnitInfo{
	AtomicUnit: "atoms",
	Conventional: dex.Denomination{
		Unit:             "VAR",
		ConversionFactor: 1e8,
	},
	Alternatives: []dex.Denomination{
		{
			Unit:             "mVAR",
			ConversionFactor: 1e5,
		},
		{
			Unit:             "µVAR",
			ConversionFactor: 1e2,
		},
	},
	FeeRateDenom: "B",
}
