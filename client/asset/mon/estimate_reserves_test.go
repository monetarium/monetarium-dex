//go:build !harness && !vspd

package mon

import (
	"encoding/hex"
	"errors"
	"testing"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/dex/calc"
	dexmon "decred.org/dcrdex/dex/networks/mon"
	walletjson "github.com/monetarium/monetarium-wallet/rpc/jsonrpc/types"
)

func tReserveUnspent(vout uint32, atoms uint64) walletjson.ListUnspentResult {
	return walletjson.ListUnspentResult{
		TxID:          tTxID,
		Vout:          vout,
		Address:       tPKHAddr.String(),
		Account:       tAcctName,
		Amount:        toCoinStr(float64(atoms) / 1e8),
		Confirmations: 5,
		ScriptPubKey:  hex.EncodeToString(tP2PKHScript),
		Spendable:     true,
	}
}

// TestEstimateReservesAboveBalance checks that a bond reserve larger than the
// spendable VAR leaves no order to estimate. The reserve is subtracted from the
// available amount, so an unguarded uint64 subtraction wrapped and MaxOrder and
// PreSwap offered lots that FundOrder then refused.
func TestEstimateReservesAboveBalance(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	const avail = 5e8 // 50 lots' worth before fees
	node.unspent = []walletjson.ListUnspentResult{tReserveUnspent(0, avail)}
	wallet.bondReserves.Store(avail + 1)

	est, err := wallet.MaxOrder(&asset.MaxOrderForm{
		LotSize:       tLotSize,
		FeeSuggestion: 10,
		MaxFeeRate:    tDCR.MaxFeeRate,
	})
	if err != nil {
		t.Fatalf("MaxOrder error: %v", err)
	}
	if est.Lots != 0 {
		t.Fatalf("MaxOrder offered %d lots with reserves above the balance", est.Lots)
	}

	if _, err := wallet.PreSwap(&asset.PreSwapForm{
		Version:       version,
		LotSize:       tLotSize,
		Lots:          1,
		MaxFeeRate:    tDCR.MaxFeeRate,
		FeeSuggestion: 10,
	}); err == nil {
		t.Fatal("PreSwap estimated a lot with reserves above the balance")
	}

	// Reserves below the balance still leave a no-split order that a UTXO
	// set aside for them can fund.
	node.unspent = []walletjson.ListUnspentResult{tReserveUnspent(0, 1e8), tReserveUnspent(1, 4e8)}
	wallet.bondReserves.Store(2e8)
	preSwap, err := wallet.PreSwap(&asset.PreSwapForm{
		Version:         version,
		LotSize:         tLotSize,
		Lots:            1,
		MaxFeeRate:      tDCR.MaxFeeRate,
		FeeSuggestion:   10,
		SelectedOptions: map[string]string{splitKey: "false"},
	})
	if err != nil || preSwap.Estimate == nil {
		t.Fatalf("PreSwap with reserves below the balance: %v", err)
	}
}

// TestFundMultiReservesAboveUTXOs checks that multi-order funding leaves the
// bond reserves alone when they exceed the spendable outputs, even though the
// wallet's balance, read separately, passes FundMultiOrder's own check.
func TestFundMultiReservesAboveUTXOs(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	const maxFeeRate, value, swaps = 80, 15e5, 2
	utxoVal := calc.RequiredOrderFunds(value, dexmon.P2PKHInputSize, swaps,
		dexmon.InitTxSizeBase, dexmon.InitTxSize, maxFeeRate)

	for _, tt := range []struct {
		name      string
		reserves  uint64
		split     bool
		wantErr   error
		wantCoins int
	}{
		{"above the outputs", utxoVal + 1, false, asset.ErrInsufficientBalance, 0},
		{"above the outputs, split allowed", utxoVal + 1, true, asset.ErrInsufficientBalance, 0},
		{"equal to the outputs", utxoVal, false, nil, 0}, // funds no order, with no error
	} {
		node.unspent = []walletjson.ListUnspentResult{tReserveUnspent(0, utxoVal)}
		node.balanceResult = &walletjson.GetBalanceResult{
			Balances: []walletjson.GetAccountBalanceResult{tAcctBal(toCoinStr(toDCR(10*utxoVal + tt.reserves)))},
		}
		node.lockedCoins = nil
		wallet.fundingCoins = make(map[outPoint]*fundingCoin)
		wallet.bondReserves.Store(tt.reserves)

		mo := &asset.MultiOrder{
			Values:        []*asset.MultiOrderValue{{Value: value, MaxSwapCount: swaps}},
			MaxFeeRate:    maxFeeRate,
			FeeSuggestion: 60,
		}
		if tt.split {
			mo.Options = map[string]string{multiSplitKey: "true"}
		}
		coins, _, _, err := wallet.FundMultiOrder(mo, 0)
		if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
			t.Fatalf("%s: err %v, want %v", tt.name, err, tt.wantErr)
		}
		if len(coins) != tt.wantCoins {
			t.Fatalf("%s: funded %d orders, want %d", tt.name, len(coins), tt.wantCoins)
		}
	}
}
