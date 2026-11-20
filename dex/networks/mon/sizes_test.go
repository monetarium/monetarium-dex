// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package mon

import (
	"bytes"
	"testing"

	"decred.org/dcrdex/dex/calc"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/txscript"
	"github.com/monetarium/monetarium-node/txscript/stdaddr"
	"github.com/monetarium/monetarium-node/wire"
)

// The tests in this file take every expected size from a transaction built
// with Monetarium's wire, never from the constants under test. A Monetarium
// output is 1 byte larger than Decred's (coin type) and so is each input
// witness, so Decred's constants undercount every DEX transaction.

// minRelayRate is the node's default minimum relay fee and the harness
// markets' MaxFeeRate, in atoms/B.
const minRelayRate = 10

// sizeFixtures holds worst-case scripts: every signature is 73 bytes (a 72-byte
// DER signature and the hash type) and every pubkey is compressed.
type sizeFixtures struct {
	p2pkhOut, p2shOut       *wire.TxOut
	contract, bondScript    []byte
	p2pkhSigScript          []byte
	redeemSig, refundSig    []byte
	bondRefundSig           []byte
	redeemTx, refundTx      *wire.MsgTx
	bondRefundTx            *wire.MsgTx
	swapTx, lastSwapTx      *wire.MsgTx // one P2PKH input; with and without change
	swapTxNoInputs, emptyTx *wire.MsgTx
}

func newSizeFixtures(t *testing.T) *sizeFixtures {
	t.Helper()
	params := chaincfg.SimNetParams()
	sig := bytes.Repeat([]byte{0x30}, 73)
	pubKey := newPubKey()
	pkh := bytes.Repeat([]byte{0x11}, 20)

	addr, err := stdaddr.NewAddressPubKeyHashEcdsaSecp256k1V0(pkh, params)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := MakeContract(addr.String(), addr.String(), bytes.Repeat([]byte{0x22}, SecretHashSize), 1_700_000_000, params)
	if err != nil {
		t.Fatal(err)
	}
	// A lock time of 2^31 or more takes the 5-byte push BondScriptSize allows for.
	bondScript, err := MakeBondScript(0, 1<<31, pkh)
	if err != nil {
		t.Fatal(err)
	}
	p2sh, err := stdaddr.NewAddressScriptHashV0(contract, params)
	if err != nil {
		t.Fatal(err)
	}
	_, p2pkhScript := addr.PaymentScript()
	_, p2shScript := p2sh.PaymentScript()

	f := &sizeFixtures{
		p2pkhOut:   wire.NewTxOut(1, p2pkhScript),
		p2shOut:    wire.NewTxOut(1, p2shScript),
		contract:   contract,
		bondScript: bondScript,
	}
	mustScript := func(s []byte, err error) []byte {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	f.p2pkhSigScript = mustScript(txscript.NewScriptBuilder().AddData(sig).AddData(pubKey).Script())
	f.redeemSig = mustScript(RedeemP2SHContract(contract, sig, pubKey, bytes.Repeat([]byte{0x33}, SecretKeySize)))
	f.refundSig = mustScript(RefundP2SHContract(contract, sig, pubKey))
	f.bondRefundSig = mustScript(RefundBondScript(bondScript, sig, pubKey))

	f.redeemTx = buildTx([][]byte{f.redeemSig}, f.p2pkhOut)
	f.refundTx = buildTx([][]byte{f.refundSig}, f.p2pkhOut)
	f.bondRefundTx = buildTx([][]byte{f.bondRefundSig}, f.p2pkhOut)
	f.swapTx = buildTx([][]byte{f.p2pkhSigScript}, f.p2shOut, f.p2pkhOut)
	f.lastSwapTx = buildTx([][]byte{f.p2pkhSigScript}, f.p2shOut)
	f.swapTxNoInputs = buildTx(nil, f.p2shOut, f.p2pkhOut)
	f.emptyTx = buildTx(nil)
	return f
}

func buildTx(sigScripts [][]byte, outs ...*wire.TxOut) *wire.MsgTx {
	tx := wire.NewMsgTx()
	for _, s := range sigScripts {
		tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, 0, s))
	}
	for _, out := range outs {
		tx.AddTxOut(out)
	}
	return tx
}

func txInSize(sigScript []byte) int {
	in := wire.NewTxIn(&wire.OutPoint{}, 0, sigScript)
	return in.SerializeSizePrefix() + in.SerializeSizeWitness()
}

// nodeIsDust is the node's isDust (internal/mempool/policy.go) for an output
// of outSize bytes, with the min relay fee in atoms/kB as the node takes it.
func nodeIsDust(outSize, value, rate int64) bool {
	return value*1000/(3*(outSize+165)) < rate*1000
}

func TestVarSizes(t *testing.T) {
	f := newSizeFixtures(t)
	p2pkhSpend, err := InputInfo(0, f.p2pkhOut.PkScript, nil, chaincfg.SimNetParams())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		got, want int
	}{
		{"P2PKHOutputSize", P2PKHOutputSize, f.p2pkhOut.SerializeSize()},
		{"P2SHOutputSize", P2SHOutputSize, f.p2shOut.SerializeSize()},
		{"TxInOverhead", TxInOverhead, txInSize(nil) - 1}, // without the sig script length byte
		{"P2PKHInputSize", P2PKHInputSize, txInSize(f.p2pkhSigScript)},
		{"InputInfo(P2PKH).Size", int(p2pkhSpend.Size()), txInSize(f.p2pkhSigScript)},
		{"MsgTxOverhead", MsgTxOverhead, f.emptyTx.SerializeSize()},
		{"InitTxSizeBase", InitTxSizeBase, f.swapTxNoInputs.SerializeSize()},
		{"InitTxSize", InitTxSize, f.swapTx.SerializeSize()},
		{"SwapContractSize", SwapContractSize, len(f.contract)},
		{"BondScriptSize", BondScriptSize, len(f.bondScript)},
		{"P2PKHSigScriptSize", P2PKHSigScriptSize, len(f.p2pkhSigScript)},
		{"RedeemSwapSigScriptSize", RedeemSwapSigScriptSize, len(f.redeemSig)},
		{"RefundSigScriptSize", RefundSigScriptSize, len(f.refundSig)},
		{"RedeemBondSigScriptSize", RedeemBondSigScriptSize, len(f.bondRefundSig)},
		{"redeemSwapTxSize", int(redeemSwapTxSize()), f.redeemTx.SerializeSize()},
		{"refund tx size", int(redeemP2SHTxSize(RefundSigScriptSize)), f.refundTx.SerializeSize()},
		{"refundBondTxSize", int(refundBondTxSize()), f.bondRefundTx.SerializeSize()},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %d, Monetarium wire says %d", tt.name, tt.got, tt.want)
		}
	}
}

func TestDustThreshold(t *testing.T) {
	f := newSizeFixtures(t)
	outSize := int64(f.p2pkhOut.SerializeSize())
	threshold := (outSize + 165) * 3 * minRelayRate
	if outSize != 37 || threshold != 6060 {
		t.Fatalf("P2PKH output is %d bytes, threshold %d; want 37 and 6060", outSize, threshold)
	}
	if !nodeIsDust(outSize, threshold-1, minRelayRate) || nodeIsDust(outSize, threshold, minRelayRate) {
		t.Fatalf("node formula does not put the threshold at %d", threshold)
	}
	for v := threshold - 100; v <= threshold+100; v++ {
		want := nodeIsDust(outSize, v, minRelayRate)
		if got := IsDustVal(P2PKHOutputSize, uint64(v), minRelayRate); got != want {
			t.Fatalf("IsDustVal(P2PKHOutputSize, %d, %d) = %v, the node says %v", v, minRelayRate, got, want)
		}
	}
}

// TestMinLotBondSize checks that a lot or bond at the server's minimum still
// leaves an output the node relays after the redeem or refund fee is paid.
func TestMinLotBondSize(t *testing.T) {
	f := newSizeFixtures(t)
	outSize := int64(f.p2pkhOut.SerializeSize())
	tests := []struct {
		name  string
		amt   uint64
		spend *wire.MsgTx
	}{
		{"MinLotSize, redeem", MinLotSize(minRelayRate), f.redeemTx},
		{"MinLotSize, refund", MinLotSize(minRelayRate), f.refundTx},
		{"MinBondSize, bond refund", MinBondSize(minRelayRate), f.bondRefundTx},
	}
	for _, tt := range tests {
		left := int64(tt.amt) - int64(tt.spend.SerializeSize())*minRelayRate
		if nodeIsDust(outSize, left, minRelayRate) {
			t.Errorf("%s: %d atoms leave %d after a %d-byte spend, which the node calls dust",
				tt.name, tt.amt, left, tt.spend.SerializeSize())
		}
	}
}

// TestReservePerLot checks the funds reserved for a split-funded order against
// the fees of its chained swaps at MaxFeeRate. The split output funds the first
// swap; each swap spends the previous swap's change. At the worst-case rate the
// chain spends the reserve down to the last contract, so the last swap has no
// change output.
func TestReservePerLot(t *testing.T) {
	f := newSizeFixtures(t)
	withChange := uint64(f.swapTx.SerializeSize())
	last := uint64(f.lastSwapTx.SerializeSize())
	lot := MinLotSize(minRelayRate)
	for n := uint64(1); n <= 30; n++ {
		swapVal := n * lot
		reserved := calc.RequiredOrderFunds(swapVal, P2PKHInputSize, n, InitTxSizeBase, InitTxSize, minRelayRate)
		need := swapVal + ((n-1)*withChange+last)*minRelayRate
		if reserved < need {
			t.Fatalf("%d one-lot swaps: reserved %d, the chain needs %d (%d short)", n, reserved, need, need-reserved)
		}
	}
}

// TestFeeCaps checks MaxFee and MaxFeeRate against the node's fee ceiling,
// max(size × 1000, 1 000 000) atoms, at every standard size. A transaction
// that may get change pays at most size × rate, plus the change it leaves in
// the fee as dust: the largest value IsDustVal calls dust, together with the
// fee the 37-byte change output would have cost.
func TestFeeCaps(t *testing.T) {
	ceiling := func(size uint64) uint64 { return max(size*1000, 1_000_000) }
	// server/dex varMaxFeeRate copies MaxFeeRate; the two must stay equal.
	if MaxFeeRate != 600 {
		t.Fatalf("MaxFeeRate = %d, want 600, server/dex varMaxFeeRate", MaxFeeRate)
	}
	if MaxFee > ceiling(0) {
		t.Fatalf("MaxFee %d is above the node's flat ceiling", MaxFee)
	}
	dust := uint64(0) // the largest change IsDustVal calls dust at MaxFeeRate
	for IsDustVal(P2PKHOutputSize, dust+1, MaxFeeRate) {
		dust++
	}
	for size := uint64(1); size <= 100_000; size++ {
		if worst := (size+P2PKHOutputSize)*MaxFeeRate + dust; worst > ceiling(size) {
			t.Fatalf("at %d bytes a fee can reach %d atoms, above the node's ceiling %d", size, worst, ceiling(size))
		}
		// A transaction with no change pays MaxNoChangeFee of an estimated size
		// that overstates the real one by a byte or two per ~300-byte input.
		if real := size * 99 / 100; MaxNoChangeFee(size) > ceiling(real) {
			t.Fatalf("estimated at %d bytes, %d real, MaxNoChangeFee is %d, above the node's ceiling %d",
				size, real, MaxNoChangeFee(size), ceiling(real))
		}
	}
}
