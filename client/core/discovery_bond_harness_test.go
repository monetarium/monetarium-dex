//go:build harness && !nolgpl

// Requires the VAR, BTC, LTC and dcrdex harnesses.

package core

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"decred.org/dcrdex/client/asset/btc"
	"decred.org/dcrdex/client/asset/ltc"
	"decred.org/dcrdex/client/asset/mon"
	"decred.org/dcrdex/client/db"
	"decred.org/dcrdex/dex"
)

var discoveryPW = []byte("abc")

// discoveryCore starts a core on a fresh DB in dir, initialized from seed (nil:
// a new one), logged in, with the VAR trading1 and the BTC and LTC alpha RPC
// wallets synced. Both cores share those wallets, a stand-in for a restored
// wallet: the bond's transaction is already in the wallet, so FindBond needs
// no rescan. VAR's alpha votes, and core locking it would stall the chain.
// stop ends the core and waits for it, so t.TempDir's cleanup finds its files
// closed; it also runs at cleanup.
func discoveryCore(t *testing.T, dir string, seed *string) (c *Core, mnemonic string, stop func()) {
	t.Helper()
	var err error
	c, err = New(&Config{
		DBPath: filepath.Join(dir, "dex.db"),
		Net:    dex.Regtest,
		Logger: dex.StdOutLogger("DISCOVERY", dex.LevelInfo),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	stop = func() { cancel(); <-done }
	t.Cleanup(stop)
	<-c.Ready()
	mnemonic, err = c.InitializeClient(discoveryPW, seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(discoveryPW); err != nil {
		t.Fatal(err)
	}
	for _, mk := range []struct {
		id   uint32
		fn   func(SimWalletType, string) (*tWallet, error)
		node string
	}{{mon.BipID, varWallet, "trading1"}, {btc.BipID, btcWallet, "alpha"}, {ltc.BipID, ltcWallet, "alpha"}} {
		tw, err := mk.fn(WTCoreClone, mk.node)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.CreateWallet(discoveryPW, tw.pass, &WalletForm{AssetID: mk.id, Config: tw.config, Type: tw.walletType}); err != nil {
			t.Fatalf("create %s wallet: %v", unbip(mk.id), err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for !c.WalletState(mk.id).Synced {
			if time.Now().After(deadline) {
				t.Fatalf("%s wallet not synced", unbip(mk.id))
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if seed != nil {
		mnemonic = *seed
	}
	return c, mnemonic, stop
}

// TestDiscoveryKeepsBond posts two bonds from one core, restores its seed into
// a second core on a fresh DB, and checks that DiscoverAccount alone saves the
// server's bonds under the restored account with the key indexes they were
// posted under.
func TestDiscoveryKeepsBond(t *testing.T) {
	for _, assetID := range []uint32{mon.BipID, btc.BipID, ltc.BipID} {
		t.Run(unbip(assetID), func(t *testing.T) {
			hc := newHarnessCtrl(assetID)

			a, seed, stopA := discoveryCore(t, t.TempDir(), nil)
			cfg, err := a.GetDEXConfig(dexHost, nil)
			if err != nil {
				t.Fatal(err)
			}
			ba := cfg.BondAssets[dex.BipIDSymbol(assetID)]
			if ba == nil {
				t.Fatalf("server has no %s bond asset", unbip(assetID))
			}
			// Two bonds, so they are posted under key indexes 0 and 1.
			for i := int64(1); i <= 2; i++ {
				if _, err := a.PostBond(&PostBondForm{Addr: dexHost, AppPass: discoveryPW, Asset: &assetID, Bond: ba.Amt}); err != nil {
					t.Fatal(err)
				}
				// Mine until the server holds the bond.
				deadline := time.Now().Add(3 * time.Minute)
				for {
					exch, err := a.Exchange(dexHost)
					if err == nil && exch.Auth.LiveStrength >= i {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("server never accepted bond %d", i)
					}
					hc.mineBlocks(context.Background(), 1)
					time.Sleep(3 * time.Second)
				}
			}
			acctA, err := a.db.Account(dexHost)
			if err != nil || len(acctA.Bonds) != 2 {
				t.Fatalf("first core's account: %v, %d bonds", err, len(acctA.Bonds))
			}
			stopA() // the profile is lost

			b, _, _ := discoveryCore(t, t.TempDir(), &seed)
			_, paid, err := b.DiscoverAccount(dexHost, discoveryPW, nil)
			if err != nil || !paid {
				t.Fatalf("DiscoverAccount: paid %v, err %v", paid, err)
			}
			// Right after discovery, before any reconnect could restore it.
			acctB, err := b.db.Account(dexHost)
			if err != nil {
				t.Fatalf("restored account: %v", err)
			}
			for _, posted := range acctA.Bonds {
				var got *db.Bond
				for _, bond := range acctB.Bonds {
					if bytes.Equal(bond.CoinID, posted.CoinID) {
						got = bond
					}
				}
				if got == nil {
					t.Fatalf("restored account holds %d bonds, not posted bond %s", len(acctB.Bonds), coinIDString(posted.AssetID, posted.CoinID))
				}
				if got.KeyIndex != posted.KeyIndex || !bytes.Equal(got.Data, posted.Data) ||
					got.LockTime != posted.LockTime || got.Amount != posted.Amount {
					t.Fatalf("restored bond %s: key index %d, lock time %d, amount %d, data %x; posted %d, %d, %d, %x",
						coinIDString(posted.AssetID, posted.CoinID), got.KeyIndex, got.LockTime, got.Amount, got.Data,
						posted.KeyIndex, posted.LockTime, posted.Amount, posted.Data)
				}
			}
		})
	}
}
