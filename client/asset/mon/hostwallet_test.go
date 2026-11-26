//go:build !harness && !vspd

package mon

import (
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/dex"
	"decred.org/dcrdex/dex/encode"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"github.com/monetarium/monetarium-node/dcrutil"
	"github.com/monetarium/monetarium-node/wire"
	walletjson "github.com/monetarium/monetarium-wallet/rpc/jsonrpc/types"
	"github.com/monetarium/monetarium-wallet/wallet"
)

// tHostTypeAtStart is whether the host type was offered before any test handed
// a wallet over.
var tHostTypeAtStart bool

// tOpenWallet creates and opens a wallet with passphrase pw, as a host does.
func tOpenWallet(t *testing.T, params *chaincfg.Params, pw []byte) *wallet.Wallet {
	t.Helper()
	db, err := wallet.CreateDB(dbDriver, filepath.Join(t.TempDir(), walletDbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := wallet.Create(tCtx, db, nil, pw, encode.RandomBytes(32), params); err != nil {
		t.Fatal(err)
	}
	w, err := wallet.Open(tCtx, newWalletConfig(db, params, wallet.DefaultGapLimit))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// tNewHostWallet is the host-type test seam: tNewNativeWallet's wallet and
// mock, as the host type, with tAcctName the one account it funds from.
func tNewHostWallet() (*ExchangeWallet, *tDcrWallet, func()) {
	nw, dcrw, shutdown := tNewNativeWallet()
	ew := nw.ExchangeWallet
	ew.wallet = &hostSPVWallet{spvWallet: nw.spvw}
	ew.walletType = WalletTypeHost
	return ew, dcrw, shutdown
}

// tHostSeam is the seam over the host type, which lists fx.
func tHostSeam(fx []walletjson.ListUnspentResult) *tSeam {
	ew, dcrw, shutdown := tNewHostWallet()
	for i := range fx {
		dcrw.unspents = append(dcrw.unspents, &fx[i])
	}
	spendable := dcrutil.Amount(15e8 + tStakerFeeSums(fx)[tAcctName])
	dcrw.acctBal = wallet.Balances{Spendable: spendable, Total: spendable}
	return &tSeam{w: ew, shutdown: shutdown,
		locked: func() []*wire.OutPoint { return dcrw.lockedOutpoints },
		sent:   func() []*wire.MsgTx { return dcrw.sentTxs },
	}
}

// TestHostWalletOwnsLittle: the host type opens only a wallet handed over, on
// the network asked for, and leaves the host wallet's own settings alone.
func TestHostWalletOwnsLittle(t *testing.T) {
	if tHostTypeAtStart {
		t.Error("the host type offered before a wallet was handed over")
	}
	cfg := &asset.WalletConfig{Type: WalletTypeHost, DataDir: t.TempDir(), Settings: map[string]string{}}
	if _, err := NewWallet(cfg, tLogger, dex.Simnet); err == nil {
		t.Error("opened with nothing handed over")
	}

	ew, _, shutdown := tNewHostWallet()
	defer shutdown()
	if restart, _ := ew.wallet.Reconfigure(tCtx, &asset.WalletConfig{Type: WalletTypeHost}, dex.Simnet, ""); restart {
		t.Error("restart on a reconfigure of the same type")
	}
	if restart, _ := ew.wallet.Reconfigure(tCtx, &asset.WalletConfig{Type: walletTypeSPV}, dex.Simnet, ""); !restart {
		t.Error("no restart on a change of type")
	}
	if err := ew.SetVotingPreferences(map[string]string{}, nil, nil); err == nil {
		t.Error("set the host wallet's voting preferences")
	}
}

// TestHostWalletHistory: the DEX's history reads only the account's
// transactions, and a payment into it from another account is no send.
func TestHostWalletHistory(t *testing.T) {
	ew, dcrw, shutdown := tNewHostWallet()
	defer shutdown()
	hw := ew.wallet.(*hostSPVWallet)
	hw.acct = 2
	tx := func(b byte, inAcct, outAcct uint32) wallet.TransactionSummary {
		return wallet.TransactionSummary{Hash: &chainhash.Hash{b},
			MyInputs:  []wallet.TransactionSummaryInput{{PreviousAccount: inAcct}},
			MyOutputs: []wallet.TransactionSummaryOutput{{Account: outAcct}}}
	}
	dcrw.blocks = []*wallet.Block{{Transactions: []wallet.TransactionSummary{
		tx(1, 0, 0), // the host's own payment between its other accounts
		tx(2, 0, 2), // the host funds the account
		tx(3, 2, 0), // the account pays the host
	}}}
	txs, err := hw.ListSinceBlock(tCtx, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]bool)
	for _, r := range txs {
		got[r.TxID] = r.Send
	}
	want := map[string]bool{(&chainhash.Hash{2}).String(): false, (&chainhash.Hash{3}).String(): true}
	if !maps.Equal(got, want) {
		t.Errorf("listed %v (txid: send), want %v", got, want)
	}

	dcrw.knownAddr = &tKnownAddress{Address: tPKHAddr, acctName: "another"}
	if owns, err := ew.wallet.WalletOwnsAddress(tCtx, tPKHAddr); err != nil || owns {
		t.Errorf("owns another account's address: %t (%v)", owns, err)
	}
	dcrw.knownAddr = &tKnownAddress{Address: tPKHAddr, acctName: tAcctName}
	if owns, err := ew.wallet.WalletOwnsAddress(tCtx, tPKHAddr); err != nil || !owns {
		t.Errorf("does not own the account's address: %t (%v)", owns, err)
	}

	// The native type lists every transaction, and each as a send.
	txs, err = hw.spvWallet.ListSinceBlock(tCtx, 0)
	if err != nil {
		t.Fatal(err)
	}
	clear(got)
	for _, r := range txs {
		got[r.TxID] = r.Send
	}
	want = map[string]bool{(&chainhash.Hash{1}).String(): true, (&chainhash.Hash{2}).String(): true,
		(&chainhash.Hash{3}).String(): true}
	if !maps.Equal(got, want) {
		t.Errorf("native: listed %v (txid: send), want %v", got, want)
	}
}

// TestUnlockAccounts: Unlock leaves an unlocked account alone, except on the
// host type, and the native type unlocks each of its own accounts.
func TestUnlockAccounts(t *testing.T) {
	rw, node, shutdown := tNewWallet()
	defer shutdown()
	node.passErr = tErr // the account reports unlocked
	if err := rw.Unlock([]byte("pw")); err != nil {
		t.Errorf("rpc: unlocked account unlocked again: %v", err)
	}
	nw, dcrw, nshutdown := tNewNativeWallet()
	defer nshutdown()
	dcrw.unlockAcctErr = tErr
	if err := nw.Unlock([]byte("pw")); err != nil {
		t.Errorf("native: unlocked account unlocked again: %v", err)
	}
	dcrw.unlockAcctErr, dcrw.acctLocked = nil, true
	if err := nw.Unlock([]byte("pw")); err != nil || dcrw.unlockAcctCalls != len(nativeAccounts) {
		t.Errorf("native: %d accounts unlocked (%v), want %d", dcrw.unlockAcctCalls, err, len(nativeAccounts))
	}
}

// TestHostWalletNetwork: a wallet handed over on one network does not open on
// another.
func TestHostWalletNetwork(t *testing.T) {
	w := tOpenWallet(t, chaincfg.TestNet3Params(), []byte("pw"))
	cfg := &asset.WalletConfig{Type: WalletTypeHost, DataDir: t.TempDir(), Settings: map[string]string{}}
	SetHostWallet(nil, tNoSync{}, 0)
	defer SetHostWallet(nil, nil, 0)
	if _, err := NewWallet(cfg, tLogger, dex.Testnet); err == nil {
		t.Error("opened with no wallet handed over")
	}
	cfg.Settings = map[string]string{}
	SetHostWallet(w, nil, 0)
	if _, err := NewWallet(cfg, tLogger, dex.Testnet); err == nil {
		t.Error("opened with no sync handed over")
	}
	cfg.Settings = map[string]string{}
	SetHostWallet(w, tNoSync{}, 0)
	if _, err := NewWallet(cfg, tLogger, dex.Simnet); err == nil {
		t.Error("a testnet wallet opened on simnet")
	}
	cfg.Settings = map[string]string{}
	if _, err := NewWallet(cfg, tLogger, dex.Testnet); err != nil {
		t.Errorf("a testnet wallet on testnet: %v", err)
	}

	// One definition however many hand-overs, with the native type's settings.
	var defs []*asset.WalletDefinition
	for _, def := range WalletInfo.AvailableWallets {
		if def.Type == WalletTypeHost {
			defs = append(defs, def)
		}
	}
	native, err := asset.WalletDef(BipID, walletTypeSPV)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 {
		t.Fatalf("%d host wallet definitions, want 1", len(defs))
	}
	if !reflect.DeepEqual(defs[0].ConfigOpts, native.ConfigOpts) || !reflect.DeepEqual(defs[0].MultiFundingOpts, native.MultiFundingOpts) {
		t.Error("the host type's settings are not the native type's")
	}

	// An account the wallet does not have fails the connect.
	SetHostWallet(w, tNoSync{}, 7)
	cfg.Settings = map[string]string{}
	aw, err := NewWallet(cfg, tLogger, dex.Testnet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := aw.Connect(tCtx); err == nil || !strings.Contains(err.Error(), "host account 7") {
		t.Errorf("connect with no account 7: %v, want a host account error", err)
	}
}
