//go:build !dcrlive

// Tests for the items of docs/var-port/invariants.md that the server asset
// guards. Each one fails on the code it guards with the bug the item names.

package mon

import (
	"bytes"
	"encoding/binary"
	"math/big"
	"testing"
	"time"

	dexmon "decred.org/dcrdex/dex/networks/mon"
	"decred.org/dcrdex/server/account"
	"github.com/decred/base58"
	decredcfg "github.com/decred/dcrd/chaincfg/v3"
	decredhd "github.com/decred/dcrd/hdkeychain/v3"
	"github.com/monetarium/monetarium-node/blockchain/stake"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/cointype"
	"github.com/monetarium/monetarium-node/dcrec"
	"github.com/monetarium/monetarium-node/dcrec/secp256k1"
	"github.com/monetarium/monetarium-node/dcrutil"
	"github.com/monetarium/monetarium-node/hdkeychain"
	"github.com/monetarium/monetarium-node/txscript"
	"github.com/monetarium/monetarium-node/txscript/stdaddr"
	"github.com/monetarium/monetarium-node/wire"
)

// Item 1 fixtures: the expected VAR atoms, and the two SKA1 values that a
// port reading SKAValue would take for them. 1e8 × 1e10 still fits an int64,
// so TxOut.GetValue returns it rather than 0.
const varAtoms = 1e8

var skaVariants = []struct {
	name string
	val  *big.Int
}{
	{"ska equal to var atoms", big.NewInt(varAtoms)},
	{"ska scaled by 1e10", new(big.Int).Mul(big.NewInt(varAtoms), big.NewInt(1e10))},
}

const ska1 = cointype.CoinType(1)

// toSKA1 makes every output of msgTx an SKA1 output with the given value for
// output vout and 0 for the others, since a transaction is single-coin. It
// checks that the transaction decodes as the node sees it: coin type 1,
// SKAValue set, Value 0.
func toSKA1(t *testing.T, msgTx *wire.MsgTx, vout int, val *big.Int) {
	t.Helper()
	for i, out := range msgTx.TxOut {
		v := new(big.Int)
		if i == vout {
			v.Set(val)
		}
		msgTx.TxOut[i] = wire.NewTxOutSKA(v, ska1, out.PkScript)
	}
	raw, err := msgTx.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded := wire.NewMsgTx()
	if err := decoded.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	out := decoded.TxOut[vout]
	if out.CoinType != ska1 || out.SKAValue == nil || out.SKAValue.Cmp(val) != 0 || out.Value != 0 {
		t.Fatalf("SKA1 fixture decodes as coin type %d, SKAValue %v, Value %d", out.CoinType, out.SKAValue, out.Value)
	}
	if out.GetValue() != val.Int64() {
		t.Fatalf("GetValue = %d, want %d", out.GetValue(), val)
	}
}

// useParams sets the package chain parameters for one test.
func useParams(t *testing.T, p *chaincfg.Params) {
	t.Helper()
	old := chainParams
	chainParams = p
	t.Cleanup(func() { chainParams = old })
}

// TestCheckSwapAddressDecred is item 10: on mainnet, where Monetarium's
// address prefixes differ from Decred's, a Decred address must be refused.
func TestCheckSwapAddressDecred(t *testing.T) {
	useParams(t, chaincfg.MainNetParams())
	be, shutdown := testBackend()
	defer shutdown()

	const decredAddr = "DsYXjAK3UiTVN9js8v9G21iRbr2wPty7f12" // Decred mainnet P2PKH
	if be.CheckSwapAddress(decredAddr) {
		t.Fatalf("CheckSwapAddress accepted the Decred mainnet address %s", decredAddr)
	}

	// Control: the same key hash as a Monetarium mainnet address is accepted.
	pkh, _, err := base58.CheckDecode(decredAddr)
	if err != nil {
		t.Fatal(err)
	}
	addr, err := stdaddr.NewAddressPubKeyHashEcdsaSecp256k1V0(pkh, chainParams)
	if err != nil {
		t.Fatal(err)
	}
	if !be.CheckSwapAddress(addr.String()) {
		t.Fatalf("CheckSwapAddress refused the Monetarium mainnet address %s", addr)
	}
}

// TestValidateXPubDecred is item 10: on mainnet a Decred extended public key
// must be refused.
func TestValidateXPubDecred(t *testing.T) {
	useParams(t, chaincfg.MainNetParams())
	seed := randomBytes(hdkeychain.MinSeedBytes)

	decredMaster, err := decredhd.NewMaster(seed, decredcfg.MainNetParams())
	if err != nil {
		t.Fatal(err)
	}
	decredXPub := decredMaster.Neuter().String()
	if err := ValidateXPub(decredXPub); err == nil {
		t.Fatalf("ValidateXPub accepted the Decred mainnet key %s", decredXPub)
	}

	// Control: the same seed's Monetarium mainnet key is accepted.
	master, err := hdkeychain.NewMaster(seed, chainParams)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateXPub(master.Neuter().String()); err != nil {
		t.Fatalf("ValidateXPub refused a Monetarium mainnet key: %v", err)
	}
}

// TestContractSKAOutput is item 1: a swap contract output paid in SKA1 must
// make Contract return an error, whatever its SKAValue.
func TestContractSKAOutput(t *testing.T) {
	be, shutdown := testBackend()
	defer shutdown()

	const height = 32
	addContract := func(swap *testMsgTxSwap) []byte {
		cleanTestChain()
		txHash := swap.tx.TxHash()
		blockHash := testAddBlockVerbose(nil, 1, height, 1)
		testAddTxVerbose(swap.tx, &txHash, blockHash, height, 1)
		return toCoinID(&txHash, 0)
	}

	// Control: the same contract paid in VAR is accepted at its value.
	swap := testMsgTxSwapInit(varAtoms)
	c, err := be.Contract(addContract(swap), swap.contract)
	if err != nil {
		t.Fatalf("VAR contract refused: %v", err)
	}
	if c.Value() != varAtoms {
		t.Fatalf("VAR contract value %d, want %d", c.Value(), uint64(varAtoms))
	}

	for _, v := range skaVariants {
		swap := testMsgTxSwapInit(varAtoms)
		toSKA1(t, swap.tx, 0, v.val)
		c, err := be.Contract(addContract(swap), swap.contract)
		if err == nil {
			t.Errorf("%s: Contract accepted an SKA1 output, value %d", v.name, c.Value())
		}
	}
}

// TestFundingCoinSKAOutput is item 1: a funding coin paid in SKA1 must make
// FundingCoin return an error. gettxout reports such an output's value as 0
// and gives no coin type.
func TestFundingCoinSKAOutput(t *testing.T) {
	be, shutdown := testBackend()
	defer shutdown()

	const height = 32
	addCoin := func(msg *testMsgTx) []byte {
		cleanTestChain()
		txHash := msg.tx.TxHash()
		blockHash := testAddBlockVerbose(nil, 1, height, 1)
		testAddTxOut(msg.tx, msg.vout, &txHash, blockHash, height, 1)
		return toCoinID(&txHash, msg.vout)
	}

	// Control: the same output paid in VAR is accepted at its value.
	msg := testMsgTxRegular(dcrec.STEcdsaSecp256k1)
	msg.tx.TxOut[0].Value = varAtoms
	coin, err := be.FundingCoin(be.ctx, addCoin(msg), nil)
	if err != nil {
		t.Fatalf("VAR funding coin refused: %v", err)
	}
	if coin.Coin().Value() != varAtoms {
		t.Fatalf("VAR funding coin value %d, want %d", coin.Coin().Value(), uint64(varAtoms))
	}

	for _, v := range skaVariants {
		msg := testMsgTxRegular(dcrec.STEcdsaSecp256k1)
		toSKA1(t, msg.tx, int(msg.vout), v.val)
		coin, err := be.FundingCoin(be.ctx, addCoin(msg), nil)
		if err == nil {
			t.Errorf("%s: FundingCoin accepted an SKA1 output, value %d", v.name, coin.Coin().Value())
		}
	}
}

// testBondTx builds a version 0 bond transaction like the client's: the bond
// output at 0 and the account commitment at 1.
func testBondTx(t *testing.T) *wire.MsgTx {
	t.Helper()
	tx := wire.NewMsgTx()
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, 1, []byte{2, 2, 3})) // junk input

	bondPriv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	pkh := stdaddr.Hash160(bondPriv.PubKey().SerializeCompressed())
	lockTime := uint32(time.Now().Add(time.Hour).Unix())
	bondScript, err := dexmon.MakeBondScript(0, lockTime, pkh)
	if err != nil {
		t.Fatal(err)
	}
	bondAddr, err := stdaddr.NewAddressScriptHashV0(bondScript, chainParams)
	if err != nil {
		t.Fatal(err)
	}
	_, bondPkScript := bondAddr.PaymentScript()
	tx.AddTxOut(wire.NewTxOut(varAtoms, bondPkScript))

	acct, err := account.NewAccountFromPubKey(randomPubKey(t))
	if err != nil {
		t.Fatal(err)
	}
	pushData := make([]byte, 2+len(acct.ID)+4+20)
	copy(pushData[2:], acct.ID[:])
	binary.BigEndian.PutUint32(pushData[2+len(acct.ID):], lockTime)
	copy(pushData[2+len(acct.ID)+4:], pkh)
	commitPkScript, err := txscript.NewScriptBuilder().AddOp(txscript.OP_RETURN).AddData(pushData).Script()
	if err != nil {
		t.Fatal(err)
	}
	tx.AddTxOut(wire.NewTxOut(0, commitPkScript))
	return tx
}

func randomPubKey(t *testing.T) []byte {
	t.Helper()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	return priv.PubKey().SerializeCompressed()
}

// TestRedemptionSKA is item 1: a redemption is a VAR transaction, so one
// paying SKA1 must make Redemption return an error.
func TestRedemptionSKA(t *testing.T) {
	be, shutdown := testBackend()
	defer shutdown()

	spentHash := randomHash()
	spentID := toCoinID(spentHash, 0)
	for _, v := range skaVariants {
		cleanTestChain()
		msg := testMsgTxRegular(dcrec.STEcdsaSecp256k1)
		toSKA1(t, msg.tx, 0, v.val)
		txHash := msg.tx.TxHash()
		verboseTx := testAddTxVerbose(msg.tx, &txHash, nil, 0, 0)
		verboseTx.Vin = append(verboseTx.Vin, testVin(spentHash, 0))
		if _, err := be.Redemption(toCoinID(&txHash, 0), spentID, nil); err == nil {
			t.Errorf("%s: Redemption accepted an SKA1 transaction", v.name)
		}
	}
}

// TestParseBondTxSKA is item 1: a bond output paid in SKA1 must make
// ParseBondTx return an error, not an amount the caller has to check.
func TestParseBondTxSKA(t *testing.T) {
	// Control: the same bond paid in VAR parses with its amount.
	raw, err := testBondTx(t).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, amt, _, _, _, _, err := ParseBondTx(0, raw)
	if err != nil {
		t.Fatalf("VAR bond refused: %v", err)
	}
	if amt != varAtoms {
		t.Fatalf("VAR bond amount %d, want %d", amt, int64(varAtoms))
	}

	for _, v := range skaVariants {
		tx := testBondTx(t)
		toSKA1(t, tx, 0, v.val)
		raw, err := tx.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		_, amt, _, _, _, _, err := ParseBondTx(0, raw)
		if err == nil {
			t.Errorf("%s: ParseBondTx accepted an SKA1 bond, amount %d", v.name, amt)
		}
	}
}

// TestFeeRateUnits is item 7: estimatesmartfee answers in atoms/kB as an
// integer string. FeeRate rounds up to atoms/B and never returns less than
// the node's 10 atoms/B minimum.
func TestFeeRateUnits(t *testing.T) {
	be, shutdown := testBackend()
	defer shutdown()
	node := be.node.(*testNode)

	for _, tt := range []struct {
		smartFee string
		want     uint64
	}{
		{"10000", 10},
		{"10001", 11},
		{"15500", 16},
		{"1000000", 1000},
		{"9999", 10},
		{"1000", 10},
		{"0", 10},
	} {
		node.smartFee = tt.smartFee
		got, err := be.FeeRate(be.ctx)
		if err != nil {
			t.Errorf("estimatesmartfee %q: %v", tt.smartFee, err)
			continue
		}
		if got != tt.want {
			t.Errorf("estimatesmartfee %q: FeeRate = %d atoms/B, want %d", tt.smartFee, got, tt.want)
		}
	}
}

// testMsgTxSSFee builds a staker-fee transaction the way the node's block
// template does: version 3, a null input, the SF marker, and one VAR payout to
// an OP_SSGEN-tagged P2PKH script.
func testMsgTxSSFee(t *testing.T) *testMsgTx {
	t.Helper()
	pkh := dcrutil.Hash160(randomPubKey(t))
	payout, err := stake.ConsolidationAddrToPkScript(pkh)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx()
	tx.Version = wire.TxVersionTreasury
	tx.AddTxIn(&wire.TxIn{
		PreviousOutPoint: wire.OutPoint{Index: wire.MaxPrevOutIndex},
		Sequence:         wire.MaxTxInSequenceNum,
		BlockHeight:      wire.NullBlockHeight,
		BlockIndex:       wire.NullBlockIndex,
	})
	tx.AddTxOut(&wire.TxOut{PkScript: stake.CreateStakerSSFeeMarker(100, 0)})
	tx.AddTxOut(wire.NewTxOut(varAtoms, payout))
	if got := stake.DetermineTxType(tx); got != stake.TxTypeSSFee {
		t.Fatalf("staker-fee fixture is a %v: %v", got, stake.CheckSSFee(tx))
	}
	return &testMsgTx{tx: tx, vout: 1}
}

// TestFundingCoinStakerFee is item 8: an output of a staker-fee transaction
// must be refused as a funding coin on every network, however old. A miner
// can respend it in a block without the owner's key.
func TestFundingCoinStakerFee(t *testing.T) {
	for _, params := range []*chaincfg.Params{chaincfg.MainNetParams(), chaincfg.TestNet3Params(), chaincfg.SimNetParams()} {
		t.Run(params.Name, func(t *testing.T) {
			useParams(t, params)
			be, shutdown := testBackend()
			defer shutdown()

			// At the stake-output maturity the server already checks, and long
			// after it.
			const height = 32
			for _, confs := range []int64{int64(params.CoinbaseMaturity), 100 * int64(params.CoinbaseMaturity)} {
				addCoin := func(msg *testMsgTx) []byte {
					cleanTestChain()
					txHash := msg.tx.TxHash()
					blockHash := testAddBlockVerbose(nil, confs, height, 1)
					testAddTxOut(msg.tx, msg.vout, &txHash, blockHash, height, confs)
					return toCoinID(&txHash, msg.vout)
				}

				// Control: a vote output of the same age is accepted.
				if _, err := be.FundingCoin(be.ctx, addCoin(testMsgTxVote()), nil); err != nil {
					t.Fatalf("%d confirmations: vote output refused: %v", confs, err)
				}

				if _, err := be.FundingCoin(be.ctx, addCoin(testMsgTxSSFee(t)), nil); err == nil {
					t.Fatalf("%d confirmations: FundingCoin accepted a staker-fee output", confs)
				}
			}
		})
	}
}
