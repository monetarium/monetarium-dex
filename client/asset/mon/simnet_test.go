//go:build harness

package mon

// Tests against the running VAR simnet harness (dex/testing/var/harness.sh):
// docs/var-port/plan.md, step 7. Each runs with the harness's trading1 wallet
// over RPC and with a native SPV wallet the test creates, unless noted. The
// harness's alpha node runs with --rejectnonstd and serves every check of what
// the network accepted.

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/dex"
	"decred.org/dcrdex/dex/calc"
	"decred.org/dcrdex/dex/config"
	"decred.org/dcrdex/dex/encode"
	dexmon "decred.org/dcrdex/dex/networks/mon"
	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/monetarium/monetarium-node/addrmgr"
	"github.com/monetarium/monetarium-node/blockchain/stake"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"github.com/monetarium/monetarium-node/cointype"
	"github.com/monetarium/monetarium-node/dcrec"
	"github.com/monetarium/monetarium-node/dcrec/secp256k1"
	"github.com/monetarium/monetarium-node/dcrutil"
	"github.com/monetarium/monetarium-node/txscript"
	"github.com/monetarium/monetarium-node/txscript/sign"
	"github.com/monetarium/monetarium-node/txscript/stdaddr"
	"github.com/monetarium/monetarium-node/wire"
	"github.com/monetarium/monetarium-wallet/p2p"
	walletjson "github.com/monetarium/monetarium-wallet/rpc/jsonrpc/types"
	"github.com/monetarium/monetarium-wallet/spv"
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
	nw   *NativeWallet // nil for an RPC wallet
	cm   *dex.ConnectionMaster
}

// tRPCWallet connects to the harness's trading1 wallet.
func tRPCWallet(t *testing.T) *tWallet { t.Helper(); return tRPCWalletNamed(t, "rpc", "trading1") }

// tRPCWalletNamed connects to the harness wallet dir (trading1, spvwallet).
func tRPCWalletNamed(t *testing.T, name, dir string) *tWallet {
	t.Helper()
	settings, err := config.Parse(filepath.Join(harnessDir(), dir, dir+".conf"))
	if err != nil {
		t.Fatal(err)
	}
	return connectWallet(t, name, &asset.WalletConfig{Settings: settings, DataDir: t.TempDir()})
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

// hostAcctPass is the host type's trading account's own passphrase, unlike the
// wallet's.
var hostAcctPass = []byte("trading account")

// tHostWallet opens an in-process wallet as a host does: passphrase walletPass,
// a default account with no passphrase of its own, and an account "trading",
// number 1, with hostAcctPass, synced by the test's own syncer. It hands that
// account over, connects the host type and funds the account from alpha with
// five outputs of 10 VAR, and the default account with one of 100 VAR.
func tHostWallet(t *testing.T) (*tWallet, *wallet.Wallet, uint32) {
	t.Helper()
	params := chaincfg.SimNetParams()
	dir := t.TempDir()
	db, err := wallet.CreateDB(dbDriver, filepath.Join(dir, walletDbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := wallet.Create(tCtx, db, nil, walletPass, encode.RandomBytes(32), params); err != nil {
		t.Fatal(err)
	}
	hw, err := wallet.Open(tCtx, newWalletConfig(db, params, wallet.DefaultGapLimit))
	if err != nil {
		t.Fatal(err)
	}
	if err := hw.Unlock(tCtx, walletPass, nil); err != nil {
		t.Fatal(err)
	}
	acct, err := hw.NextAccount(tCtx, "trading")
	if err != nil {
		t.Fatal(err)
	}
	if err := hw.SetAccountPassphrase(tCtx, acct, hostAcctPass); err != nil {
		t.Fatal(err)
	}
	hw.Lock()

	ctx, cancel := context.WithCancel(tCtx)
	syncer := spv.NewSyncer(hw, p2p.NewLocalPeer(params, &net.TCPAddr{IP: net.ParseIP("::1")}, addrmgr.New(dir)))
	syncer.SetPersistentPeers([]string{simnetSPVPeer})
	hw.SetNetworkBackend(syncer)
	done := make(chan struct{})
	go func() { syncer.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done }) // after the DEX wallet's disconnect, before the db's close

	SetHostWallet(hw, syncer, acct)
	tw := connectWallet(t, "host", &asset.WalletConfig{Type: WalletTypeHost, DataDir: t.TempDir(),
		Settings: map[string]string{}})
	waitFor(t, "host wallet synced", 60*time.Second, func() bool {
		ss, err := tw.w.SyncStatus()
		return err == nil && ss.Synced
	})
	outs := make([]string, 6)
	for i := range outs[:5] {
		addr, err := tw.w.DepositAddress()
		if err != nil {
			t.Fatal(err)
		}
		outs[i] = strconv.Quote(addr) + `:"10"`
	}
	defaultAddr, err := hw.NewExternalAddress(tCtx, 0)
	if err != nil {
		t.Fatal(err)
	}
	outs[5] = strconv.Quote(defaultAddr.String()) + `:"100"`
	mustCtl(t, "alpha", "sendmany", "default", "{"+strings.Join(outs, ",")+"}")
	mine(t, 1)
	waitFor(t, "host wallet funded", 30*time.Second, func() bool {
		bal, err := tw.w.Balance()
		return err == nil && bal.Available >= 50e8
	})
	return tw, hw, acct
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
	nw, isNative := backend.(*NativeWallet)
	if isNative {
		w, is = nw.ExchangeWallet, true
	}
	if !is {
		t.Fatalf("%s: wallet type %T", name, backend)
	}
	pw := walletPass
	if cfg.Type == WalletTypeHost {
		pw = hostAcctPass
	}
	if err := w.Unlock(pw); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return &tWallet{name, w, nw, cm}
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

// minedAt is the height of the block on alpha's chain holding txid, 0 while it
// is unmined.
func minedAt(t *testing.T, txid string) int64 {
	t.Helper()
	var res struct {
		Confirmations int64 `json:"confirmations"`
		BlockHeight   int64 `json:"blockheight"`
	}
	if err := json.Unmarshal([]byte(mustCtl(t, "alpha_node", "getrawtransaction", txid, "1")), &res); err != nil {
		t.Fatal(err)
	}
	if res.Confirmations < 1 {
		return 0
	}
	return res.BlockHeight
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
	host, _, _ := tHostWallet(t)
	wallets := []*tWallet{tRPCWallet(t), tNativeWallet(t), host}
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
	host, _, _ := tHostWallet(t)
	for _, tw := range []*tWallet{tRPCWallet(t), tNativeWallet(t), host} {
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
	host, _, _ := tHostWallet(t)
	for _, tw := range []*tWallet{tRPCWallet(t), tNativeWallet(t), host} {
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
				if minedAt(t, txid) == 0 {
					return false
				}
			}
			return true
		})
	}
}

const ska1 = cointype.CoinType(1)

// unspents lists tw's outputs of coin type ct in every account, as the wallet
// reports them: the driver's Unspents keeps only VAR. For the host type, only
// the account it trades from.
func unspents(t *testing.T, tw *tWallet, ct cointype.CoinType) []*walletjson.ListUnspentResult {
	t.Helper()
	var res []*walletjson.ListUnspentResult
	var err error
	switch w := tw.w.wallet.(type) {
	case *rpcWallet:
		err = w.rpcClientRawRequest(tCtx, methodListUnspent, anylist{0, 9999999, nil, "", uint8(ct)}, &res)
	case *spvWallet:
		res, err = w.dcrWallet.ListUnspent(tCtx, 0, math.MaxInt32, nil, "", &ct)
	case *hostSPVWallet:
		res, err = w.dcrWallet.ListUnspent(tCtx, 0, math.MaxInt32, nil, w.Accounts().PrimaryAccount, &ct)
	default:
		t.Fatalf("%s: wallet %T", tw.name, w)
	}
	if err != nil {
		t.Fatalf("%s: %v", tw.name, err)
	}
	return res
}

// skaOutputs is tw's SKA1 outputs and their amounts. Each must be confirmed and
// spendable, as a VAR output must be to fund anything.
func skaOutputs(t *testing.T, tw *tWallet) map[outPoint]string {
	t.Helper()
	m := make(map[outPoint]string)
	for _, u := range unspents(t, tw, ska1) {
		if u.Confirmations < 1 || !u.Spendable {
			t.Fatalf("%s: SKA1 output %s:%d unconfirmed or unspendable", tw.name, u.TxID, u.Vout)
		}
		h, err := chainhash.NewHashFromStr(u.TxID)
		if err != nil {
			t.Fatal(err)
		}
		m[newOutPoint(h, u.Vout)] = u.Amount
	}
	return m
}

// sendSKA1 sends tw's account acct one SKA1 output of each amount from alpha,
// in one sendmany (alpha's SKA1 change is unconfirmed until mined), and waits
// for tw to hold them confirmed.
func sendSKA1(t *testing.T, tw *tWallet, acct string, amts ...string) {
	t.Helper()
	outs := make([]string, len(amts))
	for i, amt := range amts {
		addr, err := tw.w.wallet.ExternalAddress(tCtx, acct)
		if err != nil {
			t.Fatal(err)
		}
		outs[i] = strconv.Quote(addr.String()) + ":" + strconv.Quote(amt)
	}
	txid := mustCtl(t, "alpha", "sendmany", "default", "{"+strings.Join(outs, ",")+"}", "1", "", "1")
	waitFor(t, "SKA1 payment mined", 2*time.Minute, func() bool { mine(t, 1); return minedAt(t, txid) > 0 })
	waitFor(t, tw.name+" holds the SKA1", 30*time.Second, func() bool {
		n := 0
		for _, u := range unspents(t, tw, ska1) {
			if u.TxID == txid && u.Confirmations > 0 {
				n++
			}
		}
		return n == len(amts)
	})
}

// TestSPVMinedSKAContract is item 1 over the native SPV wallet's block filter
// search: with no transaction data, a contract paid in SKA1 and mined within
// ContractSearchLimit must fail the audit as paying no VAR. The same contract
// paid in VAR and found the same way must pass, so the search itself works.
// The host type searches the same way.
func TestSPVMinedSKAContract(t *testing.T) {
	host, _, _ := tHostWallet(t)
	for _, tw := range []*tWallet{tNativeWallet(t), host} {
		testMinedSKAContract(t, tw)
	}
}

func testMinedSKAContract(t *testing.T, tw *tWallet) {
	t.Helper()
	w := tw.w
	self, err := w.DepositAddress()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ coin, coinType string }{{"VAR", "0"}, {"SKA1", "1"}} {
		kind := tw.name + " " + c.coin + " contract"
		secretHash := sha256.Sum256(encode.RandomBytes(32))
		contract, err := dexmon.MakeContract(self, alphaAddress, secretHash[:], time.Now().Add(time.Hour).Unix(),
			w.chainParams)
		if err != nil {
			t.Fatal(err)
		}
		scriptAddr, err := stdaddr.NewAddressScriptHashV0(contract, w.chainParams)
		if err != nil {
			t.Fatal(err)
		}
		_, pkScript := scriptAddr.PaymentScript()
		txid := mustCtl(t, "alpha", "sendtoaddress", scriptAddr.String(), "1", "", "", c.coinType)
		h, err := chainhash.NewHashFromStr(txid)
		if err != nil {
			t.Fatal(err)
		}
		vout := -1
		for i, out := range accepted(t, kind, h).TxOut {
			if string(out.PkScript) == string(pkScript) {
				vout = i
			}
		}
		if vout < 0 {
			t.Fatalf("%s: %s pays nothing to the contract", kind, txid)
		}
		var height int64
		waitFor(t, kind+" mined", 2*time.Minute, func() bool { mine(t, 1); height = minedAt(t, txid); return height > 0 })
		waitFor(t, tw.name+" wallet at the contract's block", 30*time.Second, func() bool {
			_, tip, err := w.wallet.GetBestBlock(tCtx)
			return err == nil && tip >= height
		})
		_, err = w.AuditContract(toCoinID(h, uint32(vout)), contract, nil, false)
		switch {
		case c.coin == "VAR" && err != nil:
			t.Fatalf("%s: %v", kind, err)
		case c.coin != "VAR" && (err == nil || !strings.Contains(err.Error(), "pays no VAR")):
			t.Fatalf("%s: want the audit's pays-no-VAR error, got %v", kind, err)
		}
	}
}

// TestSKAWalletFunding is item 6 on the network: the trading1 and spvwallet
// RPC wallets hold step 1's SKA1 outputs, and the native SPV wallet and the host
// type's trading account are sent the same five. Each amount funded, sent or withdrawn is above the wallet's largest
// VAR output, so a misread SKA1 output would be picked before any VAR one. Each
// operation must succeed, and its transaction must be on alpha (spvwallet is the
// external wallet's own SPV publish path). After each, no SKA1 output may be
// among the coins returned, the outputs locked or any input, and the wallet's
// SKA1 outputs must be unchanged. The native wallet's account transfer sweeps
// the mixed account, which then holds SKA1 too.
func TestSKAWalletFunding(t *testing.T) {
	harnessSKA1 := []string{"1000", "1000", "1000", "1000", "5"} // harness.sh's SKA1_OUTPUTS
	native := tNativeWallet(t)
	sendSKA1(t, native, defaultAccountName, harnessSKA1...)
	host, _, _ := tHostWallet(t)
	sendSKA1(t, host, host.w.wallet.Accounts().PrimaryAccount, harnessSKA1...)
	for _, tw := range []*tWallet{tRPCWallet(t), tRPCWalletNamed(t, "spvwallet", "spvwallet"), native, host} {
		w, name := tw.w, tw.name
		ska := skaOutputs(t, tw)
		if got := slices.Sorted(maps.Values(ska)); !slices.Equal(got, harnessSKA1) {
			t.Fatalf("%s: SKA1 outputs %v, want %v", name, got, harnessSKA1)
		}
		noSKA := func(kind string, coins asset.Coins, txs ...*wire.MsgTx) {
			t.Helper()
			var ops []outPoint
			for _, c := range coins {
				h, v, err := decodeCoinID(c.ID())
				if err != nil {
					t.Fatal(err)
				}
				ops = append(ops, newOutPoint(h, v))
			}
			for _, tx := range txs {
				for _, in := range tx.TxIn {
					ops = append(ops, newOutPoint(&in.PreviousOutPoint.Hash, in.PreviousOutPoint.Index))
				}
			}
			locked, err := w.wallet.LockedOutputs(tCtx, "")
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			for _, l := range locked {
				h, err := chainhash.NewHashFromStr(l.Txid)
				if err != nil {
					t.Fatal(err)
				}
				ops = append(ops, newOutPoint(h, l.Vout))
			}
			for _, op := range ops {
				if _, is := ska[op]; is {
					t.Fatalf("%s: SKA1 output %s funded, spent or locked", kind, op)
				}
			}
			if now := skaOutputs(t, tw); !maps.Equal(now, ska) {
				t.Fatalf("%s: SKA1 outputs %v, were %v", kind, now, ska)
			}
		}
		// amount is above the largest VAR output, which grows across runs.
		amount := func() uint64 {
			var largest uint64
			for _, u := range unspents(t, tw, cointype.CoinTypeVAR) {
				v, err := coinStrToAtoms(u.Amount)
				if err != nil {
					t.Fatal(err)
				}
				largest = max(largest, v)
			}
			return largest + 1e8
		}
		order := func(opts map[string]string) *asset.Order {
			return &asset.Order{Version: version, Value: amount(), MaxSwapCount: 1, MaxFeeRate: tFeeRate,
				FeeSuggestion: tFeeRate, Options: opts}
		}

		coins, _, _, err := w.FundOrder(order(nil))
		if err != nil {
			t.Fatalf("%s fund: %v", name, err)
		}
		t.Cleanup(func() { w.ReturnCoins(coins) })
		noSKA(name+" fund", coins)
		if err := w.ReturnCoins(coins); err != nil {
			t.Fatal(err)
		}

		coin, err := w.Send(alphaAddress, amount(), tFeeRate)
		if err != nil {
			t.Fatalf("%s send: %v", name, err)
		}
		noSKA(name+" send", nil, accepted(t, name+" send", coinTxid(t, name+" send", coin.ID())))
		coin, err = w.Withdraw(alphaAddress, amount(), tFeeRate)
		if err != nil {
			t.Fatalf("%s withdraw: %v", name, err)
		}
		noSKA(name+" withdraw", nil, accepted(t, name+" withdraw", coinTxid(t, name+" withdraw", coin.ID())))

		splitCoins, _, splitFee, err := w.FundOrder(order(map[string]string{"swapsplit": "true"}))
		if err != nil {
			t.Fatalf("%s split: %v", name, err)
		}
		t.Cleanup(func() { w.ReturnCoins(splitCoins) })
		if splitFee == 0 || len(splitCoins) != 1 {
			t.Fatalf("%s: fee %d, %d funding coins: no split built", name, splitFee, len(splitCoins))
		}
		noSKA(name+" split", splitCoins, accepted(t, name+" split", coinTxid(t, name+" split", splitCoins[0].ID())))
		if err := w.ReturnCoins(splitCoins); err != nil {
			t.Fatal(err)
		}

		if tw.nw == nil {
			continue
		}
		// The transfer sweeps the mixed account into default: VAR and SKA1 there.
		mixedAddr, err := w.wallet.ExternalAddress(tCtx, mixedAccountName)
		if err != nil {
			t.Fatal(err)
		}
		mixedVAR := pay(t, mixedAddr, 2e8)
		sendSKA1(t, tw, mixedAccountName, "1000", "5")
		waitFor(t, "native wallet holds the mixed VAR", 30*time.Second, func() bool {
			uns, err := w.wallet.Unspents(tCtx, mixedAccountName)
			return err == nil && slices.ContainsFunc(uns, func(u *walletjson.ListUnspentResult) bool {
				return u.TxID == mixedVAR.txHash().String() && u.Vout == mixedVAR.vout()
			})
		})
		ska = skaOutputs(t, tw)
		defaultTxids := make(map[string]bool)
		uns, err := w.wallet.Unspents(tCtx, defaultAccountName)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range uns {
			defaultTxids[u.TxID] = true
		}
		if err := tw.nw.transferAccount(tCtx, defaultAccountName, mixedAccountName); err != nil {
			t.Fatalf("%s transfer: %v", name, err)
		}
		// The transfer's output is the default account's one new output.
		var transfer *wire.MsgTx
		uns, err = w.wallet.Unspents(tCtx, defaultAccountName)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range uns {
			if !defaultTxids[u.TxID] {
				h, err := chainhash.NewHashFromStr(u.TxID)
				if err != nil {
					t.Fatal(err)
				}
				transfer = accepted(t, name+" transfer", h)
			}
		}
		if transfer == nil || !slices.ContainsFunc(transfer.TxIn, func(in *wire.TxIn) bool {
			return in.PreviousOutPoint == *mixedVAR.wireOutPoint()
		}) {
			t.Fatalf("%s: no transfer of the mixed account's VAR", name)
		}
		noSKA(name+" transfer", nil, transfer)
	}
}

// TestHostWalletAccount is item 12 for the host type's account: the default
// account of the host's wallet holds 100 VAR, the trading account 50 in outputs
// of 10. An order, send or withdrawal of 60 VAR must be refused, and one of 15
// VAR, which takes two outputs, must spend and lock none of the default
// account's. Disconnecting leaves the host's wallet open.
func TestHostWalletAccount(t *testing.T) {
	tw, hw, acct := tHostWallet(t)
	w := tw.w
	ct := cointype.CoinTypeVAR
	defaults, err := hw.ListUnspent(tCtx, 0, math.MaxInt32, nil, defaultAccountName, &ct)
	if err != nil || len(defaults) == 0 {
		t.Fatalf("default account outputs %v: %v", defaults, err)
	}
	isDefault := func(op wire.OutPoint) bool {
		return slices.ContainsFunc(defaults, func(u *walletjson.ListUnspentResult) bool {
			return u.TxID == op.Hash.String() && u.Vout == op.Index
		})
	}
	order := func(val uint64) *asset.Order {
		return &asset.Order{Version: version, Value: val, MaxSwapCount: 1, MaxFeeRate: tFeeRate, FeeSuggestion: tFeeRate}
	}

	// Refused for want of funds, not at signing: the default account is locked.
	short := func(kind string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "insufficient") && !strings.Contains(err.Error(), "not enough") {
			t.Errorf("%s of 60 VAR: %v, want a shortage of funds", kind, err)
		}
	}
	_, _, _, err = w.FundOrder(order(60e8))
	short("order", err)
	_, err = w.Send(alphaAddress, 60e8, tFeeRate)
	short("send", err)
	_, err = w.Withdraw(alphaAddress, 60e8, tFeeRate)
	short("withdrawal", err)

	coins, _, _, err := w.FundOrder(order(15e8))
	if err != nil {
		t.Fatalf("fund: %v", err)
	}
	t.Cleanup(func() { w.ReturnCoins(coins) })
	locked, err := w.wallet.LockedOutputs(tCtx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range locked {
		h, err := chainhash.NewHashFromStr(l.Txid)
		if err != nil {
			t.Fatal(err)
		}
		if isDefault(wire.OutPoint{Hash: *h, Index: l.Vout}) {
			t.Errorf("fund locked the default account's %s:%d", l.Txid, l.Vout)
		}
	}
	if err := w.ReturnCoins(coins); err != nil {
		t.Fatal(err)
	}
	for kind, send := range map[string]func() (asset.Coin, error){
		"send":     func() (asset.Coin, error) { return w.Send(alphaAddress, 15e8, tFeeRate) },
		"withdraw": func() (asset.Coin, error) { return w.Withdraw(alphaAddress, 15e8, tFeeRate) },
	} {
		coin, err := send()
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		for _, in := range accepted(t, kind, coinTxid(t, kind, coin.ID())).TxIn {
			if isDefault(in.PreviousOutPoint) {
				t.Errorf("%s spent the default account's %s", kind, in.PreviousOutPoint)
			}
		}
	}

	tw.cm.Disconnect()
	if _, err := hw.AccountBalance(tCtx, acct, 0); err != nil {
		t.Errorf("host wallet after the DEX disconnected: %v", err)
	}
}

// TestLargeSwapUnderLoad is item 5 under step 9's load (plan.md, step 9): orders
// in 0.1 VAR lots funded from 0.01 VAR outputs, with the wallet's other VAR
// outputs locked, so coin selection cannot take a few big ones. Step 5's 2.1 VAR
// order, which the outputs cover, must be refused at funding: its swap of about
// 35 700 bytes would never confirm. The largest order that funds, 0.01 VAR at a
// time below it, must be within two inputs of the cap, and its swap must confirm
// within 3 blocks of its broadcast, the step's bound, every block until then
// mined under load. RPC wallet only: the step's trade tests run RPC wallets.
func TestLargeSwapUnderLoad(t *testing.T) {
	const (
		bigVal, lotSize = 2.1e8, 1e7
		nOuts           = 240 // 2.1 VAR and its fees take 215 at maxFeeRate
		maxFeeRate      = 100 // the VAR market's under load (genmarkets-var.sh's VAR_MAX_FEE_RATE)
		maxBlocks       = 3
	)
	if _, err := os.Stat(filepath.Join(harnessDir(), "load", "on")); err != nil {
		t.Skip("the load is off (harness-ctl/load on): the CI harness job runs without it")
	}
	tw := tRPCWallet(t)
	w, rpcw := tw.w, tw.w.wallet.(*rpcWallet)

	// trading1 pays itself nOuts outputs of 0.01 VAR, each to its own address:
	// sendmany's amounts are keyed by address, and DepositAddress wraps past the
	// gap limit. At maxFeeRate: the VAR load pays the node's estimate, which it
	// keeps raising, and alpha's 10 atoms/B waited over 40 blocks unmined.
	outs := make([]string, nOuts)
	for i := range outs {
		var addr string
		if err := rpcw.rpcClientRawRequest(tCtx, "getnewaddress", anylist{w.depositAccount(), "ignore"}, &addr); err != nil {
			t.Fatal(err)
		}
		outs[i] = strconv.Quote(addr) + `:"0.01"`
	}
	mustCtl(t, "trading1", "settxfee", strconv.FormatFloat(maxFeeRate*1000/1e8, 'f', 8, 64)) // VAR/kB
	t.Cleanup(func() { ctl(t, "", "trading1", "settxfee", "0") })                            // dynamic fees again
	fundTxid := mustCtl(t, "trading1", "sendmany", w.depositAccount(), "{"+strings.Join(outs, ",")+"}")
	mustCtl(t, "trading1", "settxfee", "0")
	waitFor(t, "funding mined", 2*time.Minute, func() bool { mine(t, 1); return minedAt(t, fundTxid) > 0 })

	// Lock every other VAR output, in every account, the funding's change too.
	var others []*wire.OutPoint
	var held int
	waitFor(t, "trading1 holds the outputs", 30*time.Second, func() bool {
		others, held = nil, 0
		for _, u := range unspents(t, tw, cointype.CoinTypeVAR) {
			if u.TxID == fundTxid && u.Amount == "0.01" && u.Confirmations > 0 {
				held++
				continue
			}
			h, err := chainhash.NewHashFromStr(u.TxID)
			if err != nil {
				t.Fatal(err)
			}
			others = append(others, wire.NewOutPoint(h, u.Vout, u.Tree))
		}
		return held == nOuts
	})
	if len(others) > 0 {
		if err := w.wallet.LockUnspent(tCtx, false, others); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.wallet.LockUnspent(tCtx, true, others) })
	}

	// The DEX's swap rate: the load's rate as the last step read it, rounded up as the
	// server rounds the estimate, capped at the market's maxFeeRate. Not w.FeeRate():
	// read right after a block, the estimate jumps to about 40 atoms/B for a few seconds.
	est := w.FeeRate()
	b, err := os.ReadFile(filepath.Join(harnessDir(), "load", "estimates"))
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b)) // rows of "height atoms/kB": the last field is the last step's
	if len(f) == 0 {
		t.Fatal("no load rate logged yet")
	}
	loadRate, err := strconv.ParseUint(f[len(f)-1], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	rate := min((loadRate+999)/1000, maxFeeRate)
	var coins asset.Coins
	val := uint64(bigVal)
	for ; val > 0; val -= 1e6 {
		coins, _, _, err = w.FundOrder(&asset.Order{Version: version, Value: val, MaxSwapCount: val / lotSize,
			MaxFeeRate: maxFeeRate, FeeSuggestion: rate})
		if err == nil {
			t.Cleanup(func() { w.ReturnCoins(coins) }) // spent coins error
			break
		}
		if val == bigVal {
			t.Logf("%d atoms refused: %v", val, err)
		}
	}
	if val == bigVal || val == 0 {
		t.Fatalf("funded %d atoms, want 2.1 VAR refused and a smaller order funded", val)
	}
	for _, c := range coins {
		if h := coinTxid(t, "fund", c.ID()); h.String() != fundTxid || c.Value() != 1e6 {
			t.Fatalf("funded from %s (%d atoms), not the 0.01 VAR outputs", h, c.Value())
		}
	}

	self, err := w.DepositAddress()
	if err != nil {
		t.Fatal(err)
	}
	from, err := strconv.ParseInt(mustCtl(t, "alpha_node", "getblockcount"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	secretHash := sha256.Sum256(encode.RandomBytes(32))
	receipts, _, _, err := w.Swap(&asset.Swaps{Version: version, Inputs: coins,
		Contracts: []*asset.Contract{{Address: self, Value: val, SecretHash: secretHash[:],
			LockTime: uint64(time.Now().Add(time.Hour).Unix())}},
		FeeRate: rate})
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	h := coinTxid(t, "swap", receipts[0].Coin().ID())
	size := accepted(t, "swap", h).SerializeSize()
	// The cap counts the driver's estimate, a few hundred bytes over the real size.
	if size > dexmon.MaxTxSize || !swapTxTooLarge(uint32(len(coins)+2)*dexmon.P2PKHInputSize) {
		t.Fatalf("swap %s of %d atoms: %d inputs, %d bytes, want within two inputs of the cap", h, val, len(coins), size)
	}

	var at int64
	for {
		if at = minedAt(t, h.String()); at > 0 {
			break
		}
		tip, err := strconv.ParseInt(mustCtl(t, "alpha_node", "getblockcount"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if tip >= from+maxBlocks {
			t.Fatalf("swap %s (%d inputs, %d bytes, %d atoms/B, estimate %d) broadcast at %d, unmined at %d",
				h, len(coins), size, rate, est, from, tip)
		}
		mine(t, 1)
	}
	t.Logf("swap %s: %d inputs, %d bytes at %d atoms/B (estimate %d), broadcast at %d, mined at %d: %d blocks",
		h, len(coins), size, rate, est, from, at, at-from)
	if at-from > maxBlocks {
		t.Fatalf("mined %d blocks after its broadcast, want at most %d", at-from, maxBlocks)
	}
	mine(t, 1) // the step before the next block records the last one's load
	mustCtl(t, "load", "check", strconv.FormatInt(from+1, 10), strconv.FormatInt(at, 10))
}

// TestStakerFeeRespend is item 8 on the network. The staker wallet votes its own
// tickets, so each of its votes pays a staker fee to one address of the account
// the DEX funds from. Any miner can respend such an output in a block, without
// the owner's key; respends are rare on the harness, so none is required here.
// With every other output locked, funding can reach the staker-fee outputs; over
// 20 blocks that hold staker's votes, at each one:
//   - an order U2 alone cannot fund is refused, though U2 and the staker-fee
//     outputs could fund it;
//   - a smaller order funds from U2 alone;
//   - Available is U2's value, the staker-fee outputs left out;
//   - the coin a funded order holds, U1, is unspent at the node.
//
// Then the funded order's swap must be mined within 3 blocks. The refused order
// and Available are the checks that fail when funding or Balance takes a
// staker-fee output; while U1 covers its order, funding never reaches one.
func TestStakerFeeRespend(t *testing.T) {
	const (
		votedBlocks = 20
		maxFeeRate  = 10 // the VAR market's, with the load off (genmarkets-var.sh)
		maxBlocks   = 3
	)
	// mine-alpha buys no staker tickets while the file exists: a purchase
	// spends any output it can, the ones this test holds included.
	hold := filepath.Join(harnessDir(), "staker", "hold")
	if err := os.WriteFile(hold, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(hold) })
	tw := tRPCWalletNamed(t, "staker", "staker")
	w := tw.w

	var locked []*wire.OutPoint
	t.Cleanup(func() {
		if len(locked) > 0 { // an empty list unlocks every output
			w.wallet.LockUnspent(tCtx, true, locked)
		}
	})
	atoms := func(u *walletjson.ListUnspentResult) uint64 {
		v, err := coinStrToAtoms(u.Amount)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	opOf := func(u *walletjson.ListUnspentResult) outPoint {
		h, err := chainhash.NewHashFromStr(u.TxID)
		if err != nil {
			t.Fatal(err)
		}
		return newOutPoint(h, u.Vout)
	}
	// lockAllBut locks every spendable VAR output but keep and the staker-fee
	// ones, and returns those staker-fee outputs.
	lockAllBut := func(keep outPoint) (fees []*walletjson.ListUnspentResult) {
		var ops []*wire.OutPoint
		for _, u := range unspents(t, tw, cointype.CoinTypeVAR) {
			switch op := opOf(u); {
			case !u.Spendable || op == keep:
			case isStakerFee(u):
				fees = append(fees, u)
			default:
				ops = append(ops, wire.NewOutPoint(&op.txHash, op.vout, u.Tree))
			}
		}
		if len(ops) > 0 {
			if err := w.wallet.LockUnspent(tCtx, false, ops); err != nil {
				t.Fatal(err)
			}
			locked = append(locked, ops...)
		}
		return fees
	}
	// reaches is whether u2 and the staker-fee outputs, largest first as funding
	// takes them, cover an order of u2's whole value.
	reaches := func(u2 uint64, fees []*walletjson.ListUnspentResult) bool {
		vals := make([]uint64, len(fees))
		for i, u := range fees {
			vals[i] = atoms(u)
		}
		slices.Sort(vals)
		sum := u2
		for n := uint64(1); len(vals) > 0; n++ {
			sum, vals = sum+vals[len(vals)-1], vals[:len(vals)-1]
			if sum >= calc.RequiredOrderFunds(u2, (n+1)*dexmon.P2PKHInputSize, 1, dexmon.InitTxSizeBase, dexmon.InitTxSize, maxFeeRate) {
				return true
			}
		}
		return false
	}
	tip := func() string { return mustCtl(t, "alpha_node", "getbestblockhash") }
	onTip := func(at string) func() bool {
		return func() bool { return strings.Contains(mustCtl(t, "staker", "getbestblock"), at) }
	}
	order := func(val uint64) *asset.Order {
		return &asset.Order{Version: version, Value: val, MaxSwapCount: 1, MaxFeeRate: maxFeeRate, FeeSuggestion: maxFeeRate}
	}

	// A purchase that started before the hold file is mined before the test reads the outputs.
	waitFor(t, "staker's purchases mined", 2*time.Minute, func() bool {
		for _, u := range unspents(t, tw, cointype.CoinTypeVAR) {
			if u.Confirmations == 0 {
				mine(t, 1)
				return false
			}
		}
		return true
	})
	var regular []*walletjson.ListUnspentResult
	for _, u := range unspents(t, tw, cointype.CoinTypeVAR) {
		if u.Spendable && u.TxType == int(stake.TxTypeRegular) {
			regular = append(regular, u)
		}
	}
	if len(regular) < 2 {
		t.Fatalf("staker has %d regular VAR outputs, want 2", len(regular))
	}
	slices.SortFunc(regular, func(a, b *walletjson.ListUnspentResult) int { return cmp.Compare(atoms(b), atoms(a)) })
	u1, u2 := regular[0], regular[1]
	u1Val, u2Val, u2Op := atoms(u1), atoms(u2), opOf(u2)

	// The funded order, on U1 alone: an order the DEX holds while votes come in.
	lockAllBut(opOf(u1))
	booked, _, _, err := w.FundOrder(order(u1Val / 2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.ReturnCoins(booked) }) // spent coins error
	if len(booked) != 1 || booked[0].Value() != u1Val {
		t.Fatalf("order funded from %v, want U1 alone (%d atoms)", booked, u1Val)
	}
	u2Wire := wire.NewOutPoint(&u2Op.txHash, u2Op.vout, u2.Tree)
	if err := w.wallet.LockUnspent(tCtx, true, []*wire.OutPoint{u2Wire}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "staker-fee outputs that reach an order of U2's value", 2*time.Minute, func() bool {
		if reaches(u2Val, lockAllBut(u2Op)) {
			return true
		}
		mine(t, 1)
		return false
	})

	voted := func() int64 {
		var si struct {
			Voted int64 `json:"voted"`
		}
		if err := json.Unmarshal([]byte(mustCtl(t, "staker", "getstakeinfo")), &si); err != nil {
			t.Fatal(err)
		}
		return si.Voted
	}
	seen := make(map[outPoint]*walletjson.ListUnspentResult) // staker-fee outputs listed so far
	var checked, blocks, skipped, respent int
	for lastVoted := voted(); checked < votedBlocks; {
		if blocks++; blocks > 4*votedBlocks {
			t.Fatalf("%d blocks checked with staker's votes in %d (%d more skipped: a block came mid-check, or the staker-fee outputs were respent)",
				checked, blocks, skipped)
		}
		mine(t, 1)
		at := tip()
		waitFor(t, "staker on tip", 30*time.Second, onTip(at))
		if v := voted(); v == lastVoted {
			continue
		} else {
			lastVoted = v
		}
		// A block mined during the checks matures more outputs: run them again then.
		fees := lockAllBut(u2Op)
		var fail string
		if !reaches(u2Val, fees) { // the fees were respent into an output not yet matured
			skipped++
			continue
		}
		if coins, _, _, err := w.FundOrder(order(u2Val)); err == nil {
			w.ReturnCoins(coins)
			fail = fmt.Sprintf("order of U2's value funded from %v", coins)
		} else if coins, _, _, err := w.FundOrder(order(u2Val / 2)); err != nil {
			fail = fmt.Sprintf("order of half U2's value: %v", err)
		} else if w.ReturnCoins(coins); len(coins) != 1 || !bytes.Equal(coins[0].ID(), toCoinID(&u2Op.txHash, u2Op.vout)) {
			fail = fmt.Sprintf("order of half U2's value funded from %v, want U2 alone", coins)
		} else if bal, err := w.Balance(); err != nil || bal.Available != u2Val {
			fail = fmt.Sprintf("Available %+v (%v), want U2's %d atoms", bal, err, u2Val)
		} else if out := mustCtl(t, "alpha_node", "gettxout", u1.TxID, strconv.Itoa(int(u1.Vout)), strconv.Itoa(int(u1.Tree)), "true"); out == "" || out == "null" {
			fail = "the funded order's U1 is spent"
		}
		if tip() != at {
			skipped++
			continue
		}
		if fail != "" {
			t.Fatalf("block %s: %s", at, fail)
		}
		for op, u := range seen {
			if out := mustCtl(t, "alpha_node", "gettxout", u.TxID, strconv.Itoa(int(u.Vout)), strconv.Itoa(int(u.Tree)), "true"); out == "" || out == "null" {
				respent++ // nothing of staker's spends them while its purchases are held
				delete(seen, op)
			}
		}
		for _, u := range fees {
			seen[opOf(u)] = u
		}
		checked++
	}

	self, err := w.DepositAddress()
	if err != nil {
		t.Fatal(err)
	}
	from, err := strconv.ParseInt(mustCtl(t, "alpha_node", "getblockcount"), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	secretHash := sha256.Sum256(encode.RandomBytes(32))
	receipts, _, _, err := w.Swap(&asset.Swaps{Version: version, Inputs: booked,
		Contracts: []*asset.Contract{{Address: self, Value: u1Val / 2, SecretHash: secretHash[:],
			LockTime: uint64(time.Now().Add(time.Hour).Unix())}},
		FeeRate: maxFeeRate})
	if err != nil {
		t.Fatalf("swap: %v", err)
	}
	h := coinTxid(t, "swap", receipts[0].Coin().ID())
	accepted(t, "swap", h)
	var at int64
	for at = minedAt(t, h.String()); at == 0; at = minedAt(t, h.String()) {
		tipH, err := strconv.ParseInt(mustCtl(t, "alpha_node", "getblockcount"), 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if tipH >= from+maxBlocks {
			t.Fatalf("swap %s broadcast at %d, unmined at %d", h, from, tipH)
		}
		mine(t, 1)
	}
	t.Logf("%d blocks with staker's votes in %d; %d staker-fee outputs respent; swap %s mined %d blocks after its broadcast",
		checked, blocks, respent, h, at-from)
}
