//go:build !harness && !botlive

package core

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/client/db"
	"decred.org/dcrdex/dex/msgjson"
	"decred.org/dcrdex/server/account"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// TestDiscoverySkippedKeyBond checks that account discovery on a fresh profile
// saves the bond the server reports for the account it finds, and not the
// bonds, prepaid ones included, reported for a key index it skips.
func TestDiscoverySkippedKeyBond(t *testing.T) {
	rig := newTestRig()
	defer rig.shutdown()
	wallet, tWallet := newTWallet(tUTXOAssetA.ID)
	rig.core.wallets[tUTXOAssetA.ID] = wallet
	rig.core.Login(tPW) // bond keys

	// A fresh profile: no account in the DB, so AddBond fails.
	rig.db.acct = nil
	rig.db.acctErr = db.ErrAcctNotFound
	rig.db.addBondErr = errors.New("account not found")
	rig.dc.acct.bonds = nil

	skippedCoin, keptCoin, prepaidCoin := []byte{0x01}, []byte{0x02}, []byte{0x09}
	queueConnect := func(coinID []byte, penalties uint16, prepaid bool) {
		rig.ws.queueResponse(msgjson.ConnectRoute, func(msg *msgjson.Message, f msgFunc) error {
			connect := new(msgjson.Connect)
			msg.Unmarshal(connect)
			sign(tDexPriv, connect)
			tWallet.findBond = tFoundBond(coinID)
			bonds := []*msgjson.Bond{{
				AssetID: tUTXOAssetA.ID,
				CoinID:  coinID,
				Amount:  dcrBondAsset.Amt,
			}}
			if prepaid {
				bonds = append(bonds, &msgjson.Bond{AssetID: account.PrepaidBondID, CoinID: prepaidCoin, Strength: 1})
			}
			resp, _ := msgjson.NewResponse(msg.ID, &msgjson.ConnectResult{
				Sig:         connect.Sig,
				ActiveBonds: bonds,
				Reputation:  &account.Reputation{BondedTier: 1, Penalties: penalties},
			}, nil)
			f(resp)
			return nil
		})
	}
	queueConnect(skippedCoin, 1, true) // key index 0: penalties at the tier, skipped
	queueConnect(keptCoin, 0, false)   // key index 1: the account

	paid, err := rig.core.discoverAccount(rig.dc, rig.crypter)
	if err != nil || !paid {
		t.Fatalf("discoverAccount: paid %v, err %v", paid, err)
	}
	if rig.db.acct == nil {
		t.Fatal("no account saved")
	}
	bonds := rig.db.acct.Bonds
	if len(bonds) != 1 || !bytes.Equal(bonds[0].CoinID, keptCoin) {
		var ids [][]byte
		for _, b := range bonds {
			ids = append(ids, b.CoinID)
		}
		t.Fatalf("saved bonds %x, want only %x", ids, keptCoin)
	}
}

// TestFindBondStoreFailure checks that findBond still drops a bond it cannot
// store unless the DB has no account for the host.
func TestFindBondStoreFailure(t *testing.T) {
	rig := newTestRig()
	defer rig.shutdown()
	wallet, tWallet := newTWallet(tUTXOAssetA.ID)
	rig.core.wallets[tUTXOAssetA.ID] = wallet
	rig.core.Login(tPW) // bond keys
	rig.core.db = hostTDB{rig.db}
	rig.db.addBondErr = errors.New("write failed")
	coinID := []byte{0x03}
	tWallet.findBond = tFoundBond(coinID)

	for _, tt := range []struct {
		name    string
		acctErr error
	}{
		{"account in the DB", nil},
		{"account read error", errors.New("decode failed")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rig.db.acct = &db.AccountInfo{Host: rig.dc.acct.host}
			rig.db.acctErr = tt.acctErr
			rig.dc.acct.bonds = nil
			str, _ := rig.core.findBond(rig.dc, &msgjson.Bond{AssetID: tUTXOAssetA.ID, CoinID: coinID, Amount: dcrBondAsset.Amt})
			if str != 0 || len(rig.dc.acct.bonds) != 0 {
				t.Fatalf("strength %d, %d bonds kept; want 0 and none", str, len(rig.dc.acct.bonds))
			}
		})
	}
}

// hostTDB is a TDB whose Account finds its account only under its own host.
type hostTDB struct{ *TDB }

func (tdb hostTDB) Account(host string) (*db.AccountInfo, error) {
	if tdb.acctErr != nil {
		return nil, tdb.acctErr
	}
	if tdb.acct == nil || tdb.acct.Host != host {
		return nil, db.ErrAcctNotFound
	}
	return tdb.acct, nil
}

// tFoundBond is what the wallet's FindBond reports for coinID.
func tFoundBond(coinID []byte) *asset.BondDetails {
	return &asset.BondDetails{
		Bond: &asset.Bond{
			AssetID: tUTXOAssetA.ID,
			CoinID:  coinID,
			Amount:  dcrBondAsset.Amt,
		},
		LockTime:     time.Now().Add(time.Hour),
		CheckPrivKey: func(*secp256k1.PrivateKey) bool { return true },
	}
}
