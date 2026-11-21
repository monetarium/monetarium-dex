//go:build harness

package mon

// Tests against the running VAR simnet harness (dex/testing/var/harness.sh):
// docs/var-port/plan.md, step 7. Each runs with the harness's trading1 wallet
// over RPC and with a native SPV wallet the test creates, unless noted. The
// harness's alpha node runs with --rejectnonstd and serves every check of what
// the network accepted.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/dex"
	"decred.org/dcrdex/dex/config"
	"decred.org/dcrdex/dex/encode"
	dexmon "decred.org/dcrdex/dex/networks/mon"
	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"github.com/monetarium/monetarium-node/dcrec"
	"github.com/monetarium/monetarium-node/dcrec/secp256k1"
	"github.com/monetarium/monetarium-node/dcrutil"
	"github.com/monetarium/monetarium-node/txscript"
	"github.com/monetarium/monetarium-node/txscript/sign"
	"github.com/monetarium/monetarium-node/txscript/stdaddr"
	"github.com/monetarium/monetarium-node/wire"
	"github.com/monetarium/monetarium-wallet/wallet"
)

const (
	// alphaAddress belongs to the harness's alpha wallet (the harness seed).
	alphaAddress = "SsWKp7wtdTZYabYFYSc9cnxhwFEjA5g4pFc"
	tFeeRate     = 10 // the VAR markets' maxFeeRate (plan.md, step 8)
)

var (
	tLogger    = dex.StdOutLogger("TEST", dex.LevelInfo)
	tCtx       = context.Background()
	walletPass = []byte("abc") // the harness's, and the native wallet's
)

func harnessDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "dextest", "var")
}

// ctl runs a harness-ctl script (alpha, alpha_node, mine-alpha, ...), with
// stdin if given, and returns its trimmed output.
func ctl(t *testing.T, stdin string, script string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(harnessDir(), "harness-ctl", script), args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustCtl(t *testing.T, script string, args ...string) string {
	t.Helper()
	out, err := ctl(t, "", script, args...)
	if err != nil {
		t.Fatalf("%s %v: %v: %s", script, args, err, out)
	}
	return out
}

func mine(t *testing.T, n int) { t.Helper(); mustCtl(t, "mine-alpha", strconv.Itoa(n)) }

// accepted waits up to 10 s for alpha's node to know txid, in its mempool or a
// block (it runs --txindex), checks that the node's bytes hash to txid by
// Monetarium's wire, and returns the node's transaction.
func accepted(t *testing.T, kind string, txid *chainhash.Hash) *wire.MsgTx {
	t.Helper()
	for i := 0; i < 20; i++ {
		if out, err := ctl(t, "", "alpha_node", "getrawtransaction", txid.String()); err == nil {
			raw, err := hex.DecodeString(out)
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			tx, err := msgTxFromBytes(raw)
			if err != nil || tx.TxHash() != *txid {
				t.Fatalf("%s: the node's bytes for %s do not hash to it (%v)", kind, txid, err)
			}
			return tx
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("%s: alpha's node does not have %s", kind, txid)
	return nil
}

// pay sends atoms from alpha to addr, unmined, and returns the output paying it.
func pay(t *testing.T, addr stdaddr.Address, atoms int64) *output {
	t.Helper()
	txid := mustCtl(t, "alpha", "sendtoaddress", addr.String(),
		strconv.FormatFloat(dcrutil.Amount(atoms).ToCoin(), 'f', 8, 64))
	h, err := chainhash.NewHashFromStr(txid)
	if err != nil {
		t.Fatal(err)
	}
	_, pkScript := addr.PaymentScript()
	for i, out := range accepted(t, "payment", h).TxOut {
		if string(out.PkScript) == string(pkScript) {
			return newOutput(h, uint32(i), uint64(out.Value), wire.TxTreeRegular)
		}
	}
	t.Fatalf("%s pays nothing to %s", txid, addr)
	return nil
}

func coinTxid(t *testing.T, kind string, coinID []byte) *chainhash.Hash {
	t.Helper()
	h, _, err := decodeCoinID(coinID)
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return h
}

// sentAccepted sends raw through w and checks that the txid w returns is the
// local one and that alpha's node has it.
func sentAccepted(t *testing.T, w *ExchangeWallet, kind string, raw []byte) {
	t.Helper()
	msgTx, err := msgTxFromBytes(raw)
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	coinID, err := w.SendTransaction(raw)
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	h := coinTxid(t, kind, coinID)
	if *h != msgTx.TxHash() {
		t.Fatalf("%s: sent %s, local txid %s", kind, h, msgTx.TxHash())
	}
	accepted(t, kind, h)
}

type tWallet struct {
	name string
	w    *ExchangeWallet
	cm   *dex.ConnectionMaster
}

// tRPCWallet connects to the harness's trading1 wallet.
func tRPCWallet(t *testing.T) *tWallet {
	t.Helper()
	settings, err := config.Parse(filepath.Join(harnessDir(), "trading1", "trading1.conf"))
	if err != nil {
		t.Fatal(err)
	}
	return connectWallet(t, "rpc", &asset.WalletConfig{Settings: settings, DataDir: t.TempDir()})
}

// tNativeWallet creates a native SPV wallet from a new seed and funds it from
// alpha with five outputs of 10 VAR.
func tNativeWallet(t *testing.T) *tWallet {
	t.Helper()
	dataDir := t.TempDir()
	if err := createSPVWallet(walletPass, encode.RandomBytes(32), dataDir, 0, 0, wallet.DefaultGapLimit,
		chaincfg.SimNetParams()); err != nil {
		t.Fatal(err)
	}
	tw := connectWallet(t, "native", &asset.WalletConfig{Type: walletTypeSPV, DataDir: dataDir,
		Settings: map[string]string{}})
	synced := func() bool {
		ss, err := tw.w.SyncStatus()
		return err == nil && ss.Synced
	}
	waitFor(t, "native wallet synced", 60*time.Second, synced)
	outs := make([]string, 5)
	for i := range outs {
		addr, err := tw.w.DepositAddress()
		if err != nil {
			t.Fatal(err)
		}
		outs[i] = strconv.Quote(addr) + `:"10"`
	}
	mustCtl(t, "alpha", "sendmany", "default", "{"+strings.Join(outs, ",")+"}")
	mine(t, 1)
	waitFor(t, "native wallet funded", 30*time.Second, func() bool {
		bal, err := tw.w.Balance()
		return err == nil && bal.Available >= 50e8
	})
	return tw
}

func connectWallet(t *testing.T, name string, cfg *asset.WalletConfig) *tWallet {
	t.Helper()
	cfg.Emit = asset.NewWalletEmitter(make(chan asset.WalletNotification, 128), BipID, tLogger)
	cfg.PeersChange = func(uint32, error) {}
	backend, err := NewWallet(cfg, tLogger.SubLogger(name), dex.Simnet)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	cm := dex.NewConnectionMaster(backend)
	if err := cm.Connect(tCtx); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	t.Cleanup(cm.Disconnect)
	w, is := backend.(*ExchangeWallet)
	if nw, isNative := backend.(*NativeWallet); isNative {
		w, is = nw.ExchangeWallet, true
	}
	if !is {
		t.Fatalf("%s: wallet type %T", name, backend)
	}
	if err := w.Unlock(walletPass); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &tWallet{name, w, cm}
}

func waitFor(t *testing.T, what string, d time.Duration, f func() bool) {
	t.Helper()
	for end := time.Now().Add(d); time.Now().Before(end); time.Sleep(time.Second) {
		if f() {
			return
		}
	}
	t.Fatalf("timeout: %s", what)
}

// medianTime is the median time of recent blocks at alpha's tip.
func medianTime(t *testing.T) int64 {
	t.Helper()
	var hdr struct {
		MedianTime int64 `json:"mediantime"`
	}
	tip := mustCtl(t, "alpha_node", "getbestblockhash")
	if err := json.Unmarshal([]byte(mustCtl(t, "alpha_node", "getblockheader", tip)), &hdr); err != nil {
		t.Fatal(err)
	}
	return hdr.MedianTime
}

// TestBroadcastEachKind is item 2 on the network: each kind step 5's
// TestSignVerify checks is sent to alpha, which must accept it under
// --rejectnonstd. The refunds wait for the median time of recent blocks to pass
// a 1-minute lock time, as the trade tests do.
func TestBroadcastEachKind(t *testing.T) {
	wallets := []*tWallet{tRPCWallet(t), tNativeWallet(t)}
	var lockTime time.Time // the last wallet's, the latest

	type pending struct {
		tw       *tWallet
		receipts []asset.Receipt
		bonds    []*asset.Bond
		bondKey  *dcrsecp256k1.PrivateKey
	}
	var refunds []*pending

	for _, tw := range wallets {
		w, name := tw.w, tw.name
		kind := func(k string) string { return name + " " + k }
		// Per wallet: MakeBondTx refuses a lock time already past.
		lockTime = time.Now().Add(time.Minute).Truncate(time.Second)
		self, err := w.DepositAddress()
		if err != nil {
			t.Fatal(err)
		}

		// Swap: one transaction, three contracts to self. The first is redeemed
		// now, the second refunded by Refund, the third by the refund Swap
		// signed in advance.
		coins, _, _, err := w.FundOrder(&asset.Order{Version: version, Value: 3e8, MaxSwapCount: 3,
			MaxFeeRate: tFeeRate, FeeSuggestion: tFeeRate})
		if err != nil {
			t.Fatalf("%s: %v", kind("fund"), err)
		}
		t.Cleanup(func() { w.ReturnCoins(coins) }) // trading1 outlives the test; spent coins error
		var secrets [3][32]byte
		contracts := make([]*asset.Contract, 3)
		for i := range contracts {
			secrets[i] = sha256.Sum256(encode.RandomBytes(32))
			secretHash := sha256.Sum256(secrets[i][:])
			contracts[i] = &asset.Contract{Address: self, Value: 1e8, SecretHash: secretHash[:],
				LockTime: uint64(lockTime.Unix())}
		}
		receipts, _, _, err := w.Swap(&asset.Swaps{Version: version, Inputs: coins, Contracts: contracts,
			FeeRate: tFeeRate, LockChange: false})
		if err != nil {
			t.Fatalf("%s: %v", kind("swap"), err)
		}
		swapHash := coinTxid(t, kind("swap"), receipts[0].Coin().ID())
		accepted(t, kind("swap"), swapHash)
		// Before the lock time the node refuses a refund as non-final. Only the
		// RPC wallet hears the node's refusal; an SPV publish gets no answer.
		if name == "rpc" {
			if _, err := w.SendTransaction(receipts[2].SignedRefund()); err == nil ||
				!strings.Contains(err.Error(), "not finalized") {
				t.Fatalf("%s: want the node's non-final error, got %v", kind("early refund"), err)
			}
		}

		// Redeem the first contract.
		swapTx, err := w.wallet.GetTransaction(tCtx, swapHash)
		if err != nil {
			t.Fatalf("%s: %v", kind("swap lookup"), err)
		}
		txData, err := swapTx.MsgTx.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		ci, err := w.AuditContract(receipts[0].Coin().ID(), receipts[0].Contract(), txData, false)
		if err != nil {
			t.Fatalf("%s: %v", kind("audit"), err)
		}
		redeemIDs, _, _, err := w.Redeem(&asset.RedeemForm{FeeSuggestion: tFeeRate,
			Redemptions: []*asset.Redemption{{Spends: ci, Secret: secrets[0][:]}}})
		if err != nil {
			t.Fatalf("%s: %v", kind("redeem"), err)
		}
		accepted(t, kind("redeem"), coinTxid(t, kind("redeem"), redeemIDs[0]))

		// Split: FundOrder reports the split's fee, 0 when it skips the split.
		// A split output an earlier run left fits its own order exactly, and
		// the driver rightly skips the split then: each run's value is random.
		splitVal := 1e8 + rand.Uint64N(1e7)
		splitCoins, _, splitFee, err := w.FundOrder(&asset.Order{Version: version, Value: splitVal, MaxSwapCount: 1,
			MaxFeeRate: tFeeRate, FeeSuggestion: tFeeRate, Options: map[string]string{"swapsplit": "true"}})
		if err != nil {
			t.Fatalf("%s: %v", kind("split"), err)
		}
		if splitFee == 0 || len(splitCoins) != 1 {
			t.Fatalf("%s: fee %d, %d funding coins: no split built", kind("split"), splitFee, len(splitCoins))
		}
		accepted(t, kind("split"), coinTxid(t, kind("split"), splitCoins[0].ID()))
		if err := w.ReturnCoins(splitCoins); err != nil {
			t.Fatal(err)
		}

		// Send and withdraw.
		coin, err := w.Send(alphaAddress, 1e8, tFeeRate)
		if err != nil {
			t.Fatalf("%s: %v", kind("send"), err)
		}
		accepted(t, kind("send"), coinTxid(t, kind("send"), coin.ID()))
		coin, err = w.Withdraw(alphaAddress, 5e7, tFeeRate)
		if err != nil {
			t.Fatalf("%s: %v", kind("withdraw"), err)
		}
		accepted(t, kind("withdraw"), coinTxid(t, kind("withdraw"), coin.ID()))

		// Two bonds: the first is refunded by the refund MakeBondTx signs in
		// advance, the second by RefundBond.
		bondKey, err := dcrsecp256k1.GeneratePrivateKey()
		if err != nil {
			t.Fatal(err)
		}
		p := &pending{tw: tw, receipts: receipts[1:], bondKey: bondKey}
		for i := 0; i < 2; i++ {
			bond, _, err := w.MakeBondTx(0, 1e8, tFeeRate, lockTime, bondKey, encode.RandomBytes(32))
			if err != nil {
				t.Fatalf("%s: %v", kind("bond"), err)
			}
			sentAccepted(t, w, kind("bond"), bond.SignedTx)
			p.bonds = append(p.bonds, bond)
		}
		refunds = append(refunds, p)
	}

	// The refunds: past the lock time, with the swaps and bonds mined.
	waitFor(t, "median time past the lock time", 3*time.Minute, func() bool {
		mine(t, 1)
		return medianTime(t) > lockTime.Unix()
	})
	for _, p := range refunds {
		w, name := p.tw.w, p.tw.name
		r := p.receipts[0]
		refundID, err := w.Refund(r.Coin().ID(), r.Contract(), tFeeRate)
		if err != nil {
			t.Fatalf("%s refund: %v", name, err)
		}
		accepted(t, name+" refund", coinTxid(t, name+" refund", refundID))
		sentAccepted(t, w, name+" signed refund", p.receipts[1].SignedRefund())
		sentAccepted(t, w, name+" signed bond refund", p.bonds[0].RedeemTx)
		b := p.bonds[1]
		coin, err := w.RefundBond(tCtx, 0, b.CoinID, b.Data, b.Amount, p.bondKey)
		if err != nil {
			t.Fatalf("%s bond refund: %v", name, err)
		}
		accepted(t, name+" bond refund", coinTxid(t, name+" bond refund", coin.ID()))
	}
}

// TestFeeCeilingNode is item 3 on the node: a transaction of at least 1000
// bytes, built with the port's helpers rather than the driver (whose clamp
// would stop it), sent with high fees not allowed, is accepted at 1000
// atoms/B and refused at 1001. Step 5's TestFeeRateCeiling relies on this
// bound. No wallet type applies.
func TestFeeCeilingNode(t *testing.T) {
	params := chaincfg.SimNetParams()
	priv, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	addr, err := stdaddr.NewAddressPubKeyHashEcdsaSecp256k1V0(dcrutil.Hash160(priv.PubKey().SerializeCompressed()), params)
	if err != nil {
		t.Fatal(err)
	}
	_, pkScript := addr.PaymentScript()

	// A 2 VAR output paying the key, from alpha.
	fund := pay(t, addr, 2e8)
	in := int64(fund.value)

	// signed is the 30-output spend paying fee, at lock time lt.
	const nOut = 30
	signed := func(fee int64, lt uint32) *wire.MsgTx {
		tx := wire.NewMsgTx()
		tx.LockTime = lt
		tx.AddTxIn(wire.NewTxIn(fund.wireOutPoint(), in, nil))
		each := (in - fee) / nOut
		for i := 0; i < nOut; i++ {
			v := each
			if i == 0 {
				v = in - fee - each*(nOut-1)
			}
			tx.AddTxOut(newTxOut(v, 0, pkScript))
		}
		tx.TxIn[0].SignatureScript, err = sign.SignatureScript(tx, 0, pkScript, txscript.SigHashAll,
			priv.Serialize(), dcrec.STEcdsaSecp256k1, true)
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	// build pays rate times the size. A new fee gives a new signature, whose
	// length can differ by a byte, so it tries past timestamp lock times, which
	// change only the signature, until one gives the size paid for.
	size := int64(signed(0, 500000001).SerializeSize())
	build := func(rate int64) *wire.MsgTx {
		for lt := uint32(500000001); lt <= 500000400; lt++ {
			if tx := signed(size*rate, lt); int64(tx.SerializeSize()) == size {
				return tx
			}
		}
		t.Fatalf("no signature gives a %d-byte transaction at %d atoms/B", size, rate)
		return nil
	}
	send := func(tx *wire.MsgTx) (string, error) {
		raw, err := tx.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		return ctl(t, hex.EncodeToString(raw), "alpha_node", "sendrawtransaction", "-")
	}

	over := build(1001)
	if size := over.SerializeSize(); size < 1000 {
		t.Fatalf("%d bytes, want at least 1000", size)
	}
	if out, err := send(over); err == nil || !strings.Contains(out, "fee too high for coin type 0") {
		t.Fatalf("at 1001 atoms/B: want the node's fee-too-high error, got %v: %s", err, out)
	}
	at := build(1000)
	out, err := send(at)
	if err != nil {
		t.Fatalf("at 1000 atoms/B (%d bytes) refused: %s", at.SerializeSize(), out)
	}
	if out != at.TxHash().String() {
		t.Fatalf("node returned %s, local txid %s", out, at.TxHash())
	}
}

// TestDustChangeNode is item 4 on the network: step 5's TestDustChange, a swap
// whose change output would hold 6040 atoms after paying its own fee at 10
// atoms/B, is sent to alpha, which refuses an output under its dust threshold
// of 6060 under --rejectnonstd.
func TestDustChangeNode(t *testing.T) {
	const contractVal, change = 1e7, 6040
	for _, tw := range []*tWallet{tRPCWallet(t), tNativeWallet(t)} {
		w, name := tw.w, tw.name
		self, err := w.DepositAddress()
		if err != nil {
			t.Fatal(err)
		}
		addr, err := stdaddr.DecodeAddress(self, w.chainParams)
		if err != nil {
			t.Fatal(err)
		}
		// InitTxSize counts a 108-byte signature script; a real one is 107
		// bytes, or 106 with a 32-byte R, which leaves 6050 atoms. A shorter R
		// leaves 6060, not dust, and the swap is tried again.
		for try := 1; ; try++ {
			in := pay(t, addr, contractVal+(dexmon.InitTxSize-1)*tFeeRate+change)
			waitFor(t, name+" knows its input", 30*time.Second, func() bool {
				mine(t, 1) // the node's template lags its mempool
				_, err := w.wallet.UnspentOutput(tCtx, in.txHash(), in.vout(), in.tree)
				return err == nil
			})
			secretHash := sha256.Sum256(encode.RandomBytes(32))
			receipts, _, _, err := w.Swap(&asset.Swaps{Version: version, Inputs: asset.Coins{in},
				Contracts: []*asset.Contract{{Address: self, Value: contractVal, SecretHash: secretHash[:],
					LockTime: uint64(time.Now().Add(time.Hour).Unix())}},
				FeeRate: tFeeRate})
			if err != nil {
				t.Fatalf("%s swap: %v", name, err)
			}
			tx := accepted(t, name+" swap", coinTxid(t, name+" swap", receipts[0].Coin().ID()))
			if len(tx.TxOut) == 1 {
				break // the would-be change went to the fee
			}
			if try == 3 {
				t.Fatalf("%s: a change output on %d tries, want a swap whose change is dust", name, try)
			}
		}
	}
}

// TestChainedSwapsReserve is item 4 in practice: a 20-lot split-funded order,
// funded at maxFeeRate 10, is swapped one lot at a time, each swap spending the
// last one's change. Every swap must be accepted, and all 20 must confirm.
// Simnet cannot force the 33-byte R values the reserve is sized for (step 3's
// TestReservePerLot checks that bound), so this checks it holds in practice.
func TestChainedSwapsReserve(t *testing.T) {
	const lots = 20
	for _, tw := range []*tWallet{tRPCWallet(t), tNativeWallet(t)} {
		w, name := tw.w, tw.name
		self, err := w.DepositAddress()
		if err != nil {
			t.Fatal(err)
		}
		// A random lot: a split output an earlier run left would fit a fixed
		// order exactly, and the driver would rightly skip the split.
		lotSize := 1e7 + rand.Uint64N(1e6)
		coins, _, splitFee, err := w.FundOrder(&asset.Order{Version: version, Value: lots * lotSize,
			MaxSwapCount: lots, MaxFeeRate: tFeeRate, FeeSuggestion: tFeeRate,
			Options: map[string]string{"swapsplit": "true"}})
		if err != nil {
			t.Fatalf("%s fund: %v", name, err)
		}
		if splitFee == 0 || len(coins) != 1 {
			t.Fatalf("%s: fee %d, %d funding coins: no split built", name, splitFee, len(coins))
		}
		accepted(t, name+" split", coinTxid(t, name+" split", coins[0].ID()))
		t.Cleanup(func() { // the last change, if any: nil would return every locked coin
			if len(coins) > 0 {
				w.ReturnCoins(coins)
			}
		})

		txids := make([]string, 0, lots)
		for i := 1; i <= lots; i++ {
			kind := name + " swap " + strconv.Itoa(i)
			secretHash := sha256.Sum256(encode.RandomBytes(32))
			receipts, change, _, err := w.Swap(&asset.Swaps{Version: version, Inputs: coins,
				Contracts: []*asset.Contract{{Address: self, Value: lotSize, SecretHash: secretHash[:],
					LockTime: uint64(time.Now().Add(time.Hour).Unix())}},
				FeeRate: tFeeRate, LockChange: i < lots}) // as core: the last swap's change may be dust, nil
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			// Each before the next: a child sent before alpha has its parent is
			// an orphan, and a native SPV publish gets no answer.
			h := coinTxid(t, kind, receipts[0].Coin().ID())
			accepted(t, kind, h)
			txids = append(txids, h.String())
			coins = nil
			if change != nil {
				coins = asset.Coins{change}
			}
		}
		// A block mined right after the last swap may not hold it yet: the
		// node's template lags its mempool.
		waitFor(t, name+" swaps mined", 2*time.Minute, func() bool {
			mine(t, 1)
			for _, txid := range txids {
				var res struct {
					Confirmations int64 `json:"confirmations"`
				}
				if err := json.Unmarshal([]byte(mustCtl(t, "alpha_node", "getrawtransaction", txid, "1")), &res); err != nil {
					t.Fatal(err)
				}
				if res.Confirmations < 1 {
					return false
				}
			}
			return true
		})
	}
}
