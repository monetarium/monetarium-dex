//go:build !harness && !botlive

package core

import (
	"testing"
	"time"

	"decred.org/dcrdex/client/db"
	"decred.org/dcrdex/dex"
	"decred.org/dcrdex/dex/calc"
	"decred.org/dcrdex/dex/encode"
	"decred.org/dcrdex/dex/msgjson"
	"decred.org/dcrdex/dex/order"
	ordertest "decred.org/dcrdex/dex/order/test"
)

// TestMakerRedeemMargin checks that a maker stops redeeming the taker's
// contract once its lock time is less than 1/8 of the taker's lock time ahead,
// while the taker's redeem and swap keep their checks.
func TestMakerRedeemMargin(t *testing.T) {
	const lockTimeTaker, lockTimeMaker = 8 * time.Hour, 20 * time.Hour

	rig := newTestRig()
	defer rig.shutdown()
	dc := rig.dc
	tCore := rig.core

	dcrWallet, _ := newTWallet(tUTXOAssetA.ID)
	tCore.wallets[tUTXOAssetA.ID] = dcrWallet
	btcWallet, tBtcWallet := newTWallet(tUTXOAssetB.ID)
	tCore.wallets[tUTXOAssetB.ID] = btcWallet
	walletSet, _, _, _ := tCore.walletSet(dc, tUTXOAssetA.ID, tUTXOAssetB.ID, true)

	lo, dbOrder, preImg, addr := makeLimitOrder(dc, true, 0, 0)
	oid := lo.ID()
	tracker := newTrackedTrade(dbOrder, preImg, dc, lockTimeTaker, lockTimeMaker,
		rig.db, rig.queue, walletSet, nil, rig.core.notify, rig.core.formatDetails)
	dc.trades[oid] = tracker

	newMatch := func(side order.MatchSide, status order.MatchStatus) *matchTracker {
		now := uint64(time.Now().UnixMilli())
		return &matchTracker{
			prefix: lo.Prefix(),
			trade:  lo.Trade(),
			MetaMatch: db.MetaMatch{
				MetaData: &db.MatchMetaData{
					Proof: db.MatchProof{Auth: db.MatchAuth{MatchStamp: now, AuditStamp: now}},
				},
				UserMatch: &order.UserMatch{
					MatchID:     ordertest.RandomMatchID(),
					Side:        side,
					Address:     ordertest.RandomAddress(),
					Status:      status,
					FeeRateSwap: tMaxFeeRate,
				},
			},
		}
	}
	// withCounterSwap gives m a counterparty contract the BTC wallet (the
	// to-wallet) reports with its lock time ahead of the clock by ahead.
	withCounterSwap := func(m *matchTracker, ahead time.Duration, confs uint32) *matchTracker {
		auditQty := calc.BaseToQuote(m.Rate, m.Quantity)
		_, auditInfo := tMsgAudit(oid, m.MatchID, addr, auditQty, encode.RandomBytes(32))
		auditInfo.Expiration = encode.DropMilliseconds(time.Now().Add(ahead))
		tBtcWallet.setConfs(auditInfo.Coin.ID(), confs, nil)
		tBtcWallet.auditInfo = auditInfo
		tBtcWallet.contractLockTime = time.Now().Add(ahead)
		m.counterSwap = auditInfo
		m.MetaData.Proof.SecretHash = auditInfo.SecretHash
		return m
	}
	tickRedeem := func(t *testing.T, m *matchTracker) {
		t.Helper()
		tracker.matches = map[order.MatchID]*matchTracker{m.MatchID: m}
		tBtcWallet.redeemCounter = 0
		tBtcWallet.redeemCoins = []dex.Bytes{encode.RandomBytes(36)}
		rig.ws.queueResponse(msgjson.RedeemRoute, redeemAcker)
		if _, err := tCore.tick(tracker); err != nil {
			t.Fatalf("tick error: %v", err)
		}
	}

	for _, tt := range []struct {
		name          string
		lockTimeTaker time.Duration
		ahead         time.Duration
		redeem        bool
	}{
		{"maker 59 min", lockTimeTaker, 59 * time.Minute, false},
		{"maker 59 min 50 s", lockTimeTaker, 59*time.Minute + 50*time.Second, false},
		{"maker 61 min", lockTimeTaker, 61 * time.Minute, true},
		// The simnet trade tests' taker lock time, where the margin is 7.5 s.
		{"maker 10 s at a 1-minute taker lock time", time.Minute, 10 * time.Second, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tracker.lockTimeTaker = tt.lockTimeTaker
			defer func() { tracker.lockTimeTaker = lockTimeTaker }()
			m := withCounterSwap(newMatch(order.Maker, order.TakerSwapCast), tt.ahead, tUTXOAssetB.SwapConf)
			tickRedeem(t, m)
			redeemed, revoked := tBtcWallet.redeemCounter == 1, m.MetaData.Proof.SelfRevoked
			if redeemed != tt.redeem || revoked == tt.redeem {
				t.Fatalf("taker contract %v from its lock time: redeemed %t, revoked %t; want redeemed %t, revoked %t",
					tt.ahead, redeemed, revoked, tt.redeem, !tt.redeem)
			}
		})
	}

	t.Run("taker redeem 1 min", func(t *testing.T) {
		m := withCounterSwap(newMatch(order.Taker, order.MakerRedeemed), time.Minute, tUTXOAssetB.SwapConf)
		tracker.mtx.RLock()
		ready, revoke := tracker.isRedeemable(tCore.ctx, m)
		tracker.mtx.RUnlock()
		if !ready || revoke {
			t.Fatalf("taker's redeem 1 minute from the maker's lock time: ready %t, revoke %t", ready, revoke)
		}
	})

	t.Run("taker swap 59 min", func(t *testing.T) {
		m := withCounterSwap(newMatch(order.Taker, order.MakerSwapCast), 59*time.Minute, tUTXOAssetA.SwapConf)
		tracker.mtx.RLock()
		ready, revoke := tracker.isSwappable(tCore.ctx, m)
		tracker.mtx.RUnlock()
		if !ready || revoke {
			t.Fatalf("taker's swap 59 minutes from the maker's lock time: ready %t, revoke %t", ready, revoke)
		}
	})

	t.Run("taker swap past the lock time", func(t *testing.T) {
		m := withCounterSwap(newMatch(order.Taker, order.MakerSwapCast), -time.Minute, tUTXOAssetA.SwapConf)
		tracker.mtx.RLock()
		_, revoke := tracker.isSwappable(tCore.ctx, m)
		tracker.mtx.RUnlock()
		if !revoke {
			t.Fatalf("taker's swap against a maker contract past its lock time not revoked")
		}
	})
}
