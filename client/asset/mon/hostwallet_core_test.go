//go:build !harness && !vspd

package mon

// Core's tests register their own driver for asset 0, the BTC driver's id,
// which this package imports, so the password change through core runs here.

import (
	"context"
	"path/filepath"
	"testing"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/client/core"
	"decred.org/dcrdex/dex"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-wallet/p2p"
	"github.com/monetarium/monetarium-wallet/wallet"
)

// tNoSync is a host's sync with no peers.
type tNoSync struct{ wallet.NetworkBackend }

func (tNoSync) Synced(context.Context) (bool, int32)       { return false, 0 }
func (tNoSync) GetRemotePeers() map[string]*p2p.RemotePeer { return nil }

// TestHostWalletPassword: the host type is not seeded, so core changes the
// password it stores for the wallet, which item 15's passphrase change needs.
// Core unlocks the account, and SetWalletPassword must refuse a wrong
// passphrase while it is unlocked. The host changes the account's passphrase
// and locks it; SetWalletPassword must then refuse the old one and take the
// new one, and core's next unlock must use it. Core refuses the change on a seeded
// type, which the native one is.
func TestHostWalletPassword(t *testing.T) {
	ctx, cancel := context.WithCancel(tCtx)
	defer cancel()
	params := chaincfg.SimNetParams()
	walletPW, oldPW, newPW, appPW := []byte("wallet"), []byte("old"), []byte("new"), []byte("app")
	hw := tOpenWallet(t, params, walletPW)
	if err := hw.Unlock(ctx, walletPW, nil); err != nil {
		t.Fatal(err)
	}
	acct, err := hw.NextAccount(ctx, "trading")
	if err != nil {
		t.Fatal(err)
	}
	unlocked := func() bool {
		t.Helper()
		u, err := hw.AccountUnlocked(ctx, acct)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	// setPass is the host's change of the account's passphrase, which leaves it locked.
	setPass := func(pw []byte) {
		t.Helper()
		if err := hw.Unlock(ctx, walletPW, nil); err != nil {
			t.Fatal(err)
		}
		if err := hw.SetAccountPassphrase(ctx, acct, pw); err != nil {
			t.Fatal(err)
		}
		hw.Lock()
		if unlocked() {
			if err := hw.LockAccount(ctx, acct); err != nil {
				t.Fatal(err)
			}
		}
	}
	setPass(oldPW)
	SetHostWallet(hw, tNoSync{}, acct)
	defer SetHostWallet(nil, nil, 0)

	c, err := core.New(&core.Config{DBPath: filepath.Join(t.TempDir(), "dex.db"), Net: dex.Simnet, Logger: tLogger})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	<-c.Ready()
	if _, err := c.InitializeClient(appPW, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateWallet(appPW, oldPW, &core.WalletForm{AssetID: BipID, Type: WalletTypeHost,
		Config: map[string]string{}}); err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if err := c.OpenWallet(BipID, appPW); err != nil || !unlocked() {
		t.Fatalf("core's unlock: %v, account unlocked %t", err, unlocked())
	}
	if err := c.SetWalletPassword(appPW, BipID, newPW); err == nil {
		t.Fatal("SetWalletPassword took a wrong passphrase while the account was unlocked")
	}

	setPass(newPW)
	if err := c.SetWalletPassword(appPW, BipID, oldPW); err == nil {
		t.Fatal("SetWalletPassword took the old passphrase")
	}
	if err := c.SetWalletPassword(appPW, BipID, newPW); err != nil {
		t.Fatalf("SetWalletPassword with the new passphrase: %v", err)
	}
	if unlocked() {
		t.Fatal("SetWalletPassword left unlocked the account it found locked")
	}
	if err := c.OpenWallet(BipID, appPW); err != nil || !unlocked() {
		t.Fatalf("core's unlock after the change: %v, account unlocked %t", err, unlocked())
	}

	for typ, want := range map[string]bool{walletTypeSPV: true, WalletTypeHost: false} {
		if def, err := asset.WalletDef(BipID, typ); err != nil || def.Seeded != want {
			t.Errorf("%s type: seeded %t (%v), want %t", typ, def != nil && def.Seeded, err, want)
		}
	}
}
