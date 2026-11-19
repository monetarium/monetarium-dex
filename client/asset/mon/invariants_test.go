package mon

// Tests for the items of docs/var-port/invariants.md, over the RPC wallet.

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"slices"
	"testing"
	"time"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/dex"
	dexmon "decred.org/dcrdex/dex/networks/mon"
	"github.com/decred/base58"
	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/monetarium/monetarium-node/blockchain/stake"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/dcrec"
	"github.com/monetarium/monetarium-node/dcrutil"
	chainjson "github.com/monetarium/monetarium-node/rpc/jsonrpc/types"
	"github.com/monetarium/monetarium-node/txscript/stdaddr"
	"github.com/monetarium/monetarium-node/wire"
	walletjson "github.com/monetarium/monetarium-wallet/rpc/jsonrpc/types"
)

// TestFeeRateUnits is item 7, for an RPC wallet in RPC sync mode:
// estimatesmartfee answers in atoms/kB as an integer string. FeeRate rounds up
// to atoms/B and never returns less than the node's 10 atoms/B minimum. An
// answer in another shape, such as Decred's coins/kB, is refused, not misread.
func TestFeeRateUnits(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

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
		{"18446744073709551615", 18446744073709552}, // rounding up must not wrap
	} {
		node.smartFee = tt.smartFee
		if got := wallet.FeeRate(); got != tt.want {
			t.Errorf("estimatesmartfee %q: FeeRate = %d atoms/B, want %d", tt.smartFee, got, tt.want)
		}
	}

	wallet.config().apiFeeFallback = false // no oracle: a refused answer leaves no rate
	for _, bad := range []string{"0.0001", "x", "-1"} {
		node.smartFee = bad
		if rate, err := wallet.wallet.(*rpcWallet).EstimateSmartFeeRate(tCtx, 2, chainjson.EstimateSmartFeeConservative); err == nil {
			t.Errorf("estimatesmartfee %q: rate %v, want an error", bad, rate)
		}
		if got := wallet.FeeRate(); got != 0 {
			t.Errorf("estimatesmartfee %q: FeeRate = %d atoms/B, want 0", bad, got)
		}
	}
}

// TestDecredAddressRejected is item 10: on mainnet, where Monetarium's address
// prefixes differ from Decred's, a Decred address must be refused. Each case
// has a control: the same key hash as a Monetarium mainnet address passes.
func TestDecredAddressRejected(t *testing.T) {
	const decredAddr = "DsYXjAK3UiTVN9js8v9G21iRbr2wPty7f12" // Decred mainnet P2PKH
	pkh, _, err := base58.CheckDecode(decredAddr)
	if err != nil {
		t.Fatal(err)
	}
	mainnet := chaincfg.MainNetParams()
	monAddr, err := stdaddr.NewAddressPubKeyHashEcdsaSecp256k1V0(pkh, mainnet)
	if err != nil {
		t.Fatal(err)
	}

	wallet, node, shutdown := tNewWalletMonitorBlocks(false) // chainParams changes below
	defer shutdown()
	wallet.chainParams = mainnet
	node.changeAddr, node.newAddr = monAddr, monAddr
	node.privWIF, err = dcrutil.NewWIF(append(make([]byte, 31), 1), mainnet.PrivateKeyID, dcrec.STEcdsaSecp256k1) // signs Swap's refund
	if err != nil {
		t.Fatal(err)
	}

	for _, addr := range []string{decredAddr, monAddr.String()} {
		wantOK := addr != decredAddr
		if got := wallet.ValidateAddress(addr); got != wantOK {
			t.Errorf("ValidateAddress(%s) = %v", addr, got)
		}
		node.unspent = []walletjson.ListUnspentResult{*tUnspent(0, "10")}
		if _, err := wallet.Send(addr, 1e8, feeSuggestion); (err == nil) != wantOK {
			t.Errorf("Send to %s: error %v", addr, err)
		}
		if _, err := wallet.Withdraw(addr, 1e8, feeSuggestion); (err == nil) != wantOK {
			t.Errorf("Withdraw to %s: error %v", addr, err)
		}
		// The counterparty's address, which Swap pays the contract to.
		swaps := &asset.Swaps{
			Inputs:    asset.Coins{newOutput(tTxHash, 0, 10e8, wire.TxTreeRegular)},
			Contracts: []*asset.Contract{{Address: addr, Value: 1e8, SecretHash: make([]byte, 32), LockTime: uint64(time.Now().Unix())}},
			FeeRate:   feeSuggestion,
		}
		if _, _, _, err := wallet.Swap(swaps); (err == nil) != wantOK {
			t.Errorf("Swap to %s: error %v", addr, err)
		}
	}
}

// TestAuditContractSKAOutput is item 1: a counterparty contract that pays the
// right P2SH in SKA1 must fail the audit, whether the audit reads the
// transaction (txData), the node's gettxout, which reports neither the coin
// type nor an SKA output's value, or the block a filter scan finds.
func TestAuditContractSKAOutput(t *testing.T) {
	secretHash := make([]byte, 32)
	contract, err := dexmon.MakeContract(tPKHAddr.String(), tPKHAddr.String(), secretHash, time.Now().Add(12*time.Hour).Unix(), tChainParams)
	if err != nil {
		t.Fatal(err)
	}
	p2sh, _ := stdaddr.NewAddressScriptHashV0(contract, tChainParams)
	_, pkScript := p2sh.PaymentScript()

	expected := 5 * int64(tLotSize) // the VAR atoms the auditor expects
	for _, ska := range []*big.Int{big.NewInt(expected), new(big.Int).Mul(big.NewInt(expected), big.NewInt(1e10))} {
		tx := wire.NewMsgTx()
		tx.AddTxIn(&wire.TxIn{})
		tx.AddTxOut(&wire.TxOut{CoinType: 1, SKAValue: ska, PkScript: pkScript})
		txData, err := tx.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		txHash := tx.TxHash()
		coinID := toCoinID(&txHash, 0)

		t.Run("txData/"+ska.String(), func(t *testing.T) {
			wallet, _, shutdown := tNewWallet()
			defer shutdown()
			if audit, err := wallet.AuditContract(coinID, contract, txData, false); err == nil {
				t.Fatalf("SKA1 contract passed the audit, valued %d", audit.Coin.Value())
			}
		})
		t.Run("gettxout/"+ska.String(), func(t *testing.T) {
			wallet, node, shutdown := tNewWallet()
			defer shutdown()
			node.txOutRes[newOutPoint(&txHash, 0)] = &chainjson.GetTxOutResult{ // as the node reports an SKA output
				Confirmations: 1,
				ScriptPubKey:  chainjson.ScriptPubKeyResult{Hex: hex.EncodeToString(pkScript)},
			}
			if audit, err := wallet.AuditContract(coinID, contract, nil, false); err == nil {
				t.Fatalf("SKA1 contract passed the audit, valued %d", audit.Coin.Value())
			}
		})
		t.Run("block scan/"+ska.String(), func(t *testing.T) {
			wallet, node, shutdown := tNewWalletMonitorBlocks(false) // currentTip changes below
			defer shutdown()
			mined, err := msgTxFromBytes(txData) // as a block carries it
			if err != nil {
				t.Fatal(err)
			}
			node.blockchain.addRawTx(1, mined) // not unspent per gettxout: the audit scans block filters
			tip, err := wallet.getBestBlock(wallet.ctx)
			if err != nil {
				t.Fatal(err)
			}
			wallet.currentTip.Store(tip)
			if audit, err := wallet.AuditContract(coinID, contract, nil, false); err == nil {
				t.Fatalf("SKA1 contract passed the audit, valued %d", audit.Coin.Value())
			}
		})
	}
}

const tTradingAcct = "tTradingAcct"

// TestFundingIgnoresSKA is item 6: no funding path may select or lock an SKA
// outpoint. The wallet lists SKA1 outputs next to VAR ones, spendable and
// confirmed like them. Each amount is above the largest VAR output, so a
// driver that read an SKA1 output as VAR would cover it with a 1000 SKA1 one;
// FundMultiOrder takes the set that over-funds least, which the 5 SKA1 enters.
func TestFundingIgnoresSKA(t *testing.T) {
	var unspents []walletjson.ListUnspentResult
	for i, amt := range []string{"4", "5", "6"} {
		unspents = append(unspents, *tUnspent(uint32(i), amt))
	}
	ska := make(map[outPoint]bool)
	addSKA := func(vout uint32, amt string, coinType uint8) {
		u := tUnspent(vout, amt)
		u.CoinType = coinType
		unspents = append(unspents, *u)
		ska[newOutPoint(tTxHash, vout)] = true
	}
	for vout := uint32(10); vout < 14; vout++ {
		addSKA(vout, "1000", 1)
	}
	addSKA(14, "5", 1)
	addSKA(16, "1000", 2) // any SKA type, not SKA1 only
	round := unspents
	trading := make([]walletjson.ListUnspentResult, len(round))
	for i, u := range round {
		if u.CoinType != 0 {
			u.Account = tTradingAcct // the trading account is read on its own
		}
		trading[i] = u
	}
	addSKA(15, "0.123456789012345678", 1) // SKA change after an SKA fee; more decimals than VAR has
	fixtures := map[string][]walletjson.ListUnspentResult{"round SKA": round, "with fractional SKA1": unspents,
		"SKA in the trading account": trading}
	testFundingAvoids(t, fixtures, ska)
}

// TestStakerFeeNotSelected is item 8: an output of a staker-fee transaction
// must never fund a DEX transaction, however old. A miner can respend it in a
// block without the owner's key, as an augmented staker-fee input. The wallet
// lists it once matured, spendable, with an OP_SSGEN-tagged P2PKH script.
func TestStakerFeeNotSelected(t *testing.T) {
	var unspents []walletjson.ListUnspentResult
	for i, amt := range []string{"4", "5", "6"} {
		unspents = append(unspents, *tUnspent(uint32(i), amt))
	}
	payout, err := stake.ConsolidationAddrToPkScript(tPKHAddr.(*stdaddr.AddressPubKeyHashEcdsaSecp256k1V0).Hash160()[:])
	if err != nil {
		t.Fatal(err)
	}
	avoid := make(map[outPoint]bool)
	for vout, o := range map[uint32]struct {
		amt   string
		confs int64
	}{20: {"1000", 16}, 21: {"3", 100_000}} { // just matured on simnet, and old
		u := tUnspent(vout, o.amt)
		u.TxType, u.Tree, u.Confirmations = int(stake.TxTypeSSFee), wire.TxTreeStake, o.confs
		u.ScriptPubKey = hex.EncodeToString(payout)
		unspents = append(unspents, *u)
		avoid[newOutPoint(tTxHash, vout)] = true
	}
	trading := slices.Clone(unspents)
	for i := range trading {
		if trading[i].TxType == int(stake.TxTypeSSFee) {
			trading[i].Account = tTradingAcct // the trading account is read on its own
		}
	}
	testFundingAvoids(t, map[string][]walletjson.ListUnspentResult{"staker fee": unspents,
		"staker fee in the trading account": trading}, avoid)

	// Only the staker-fee type goes: the owner's vote and revocation outputs still fund an order
	// that the 1 VAR regular output cannot.
	for _, txType := range []stake.TxType{stake.TxTypeSSGen, stake.TxTypeSSRtx} {
		wallet, node, shutdown := tNewWallet()
		reward := tUnspent(1, "10")
		reward.TxType, reward.Tree, reward.Confirmations = int(txType), wire.TxTreeStake, 1000
		reward.ScriptPubKey = hex.EncodeToString(payout)
		node.unspent = []walletjson.ListUnspentResult{*tUnspent(0, "1"), *reward}
		node.newAddr, node.changeAddr = tPKHAddr, tPKHAddr
		node.balanceResult = &walletjson.GetBalanceResult{Balances: []walletjson.GetAccountBalanceResult{tAcctBal("11")}}
		_, _, _, err := wallet.FundOrder(&asset.Order{Version: version, Value: 5e8, MaxSwapCount: 5e8 / tLotSize,
			MaxFeeRate: tDCR.MaxFeeRate, FeeSuggestion: feeSuggestion})
		shutdown()
		if err != nil {
			t.Errorf("tx type %v: FundOrder: %v", txType, err)
		}
	}
}

// testFundingAvoids runs every funding path over each fixture: VAR outputs of
// 4, 5 and 6 VAR next to outputs that must be avoided. Each amount is above
// the largest VAR output, so a driver that took an avoided output for an
// ordinary VAR one would cover it with a large one; FundMultiOrder takes the
// set that over-funds least, so each fixture also holds a small one. Each path must
// succeed without returning, locking or spending an avoided outpoint.
func testFundingAvoids(t *testing.T, fixtures map[string][]walletjson.ListUnspentResult, avoid map[outPoint]bool) {
	t.Helper()
	const largestVAR = 6e8
	order := func(split bool) *asset.Order {
		const val = largestVAR + 2e8
		return &asset.Order{Version: version, Value: val, MaxSwapCount: val / tLotSize,
			MaxFeeRate: tDCR.MaxFeeRate, FeeSuggestion: feeSuggestion,
			Options: map[string]string{"swapsplit": map[bool]string{true: "true", false: "false"}[split]}}
	}
	multiOrder := func(w *ExchangeWallet, split bool) (asset.Coins, error) {
		second := uint64(1e8) // 7 and 1 VAR fit without a split; two of 7 VAR need one
		if split {
			second = largestVAR + 1e8
		}
		mo := &asset.MultiOrder{Values: []*asset.MultiOrderValue{
			{Value: largestVAR + 1e8, MaxSwapCount: (largestVAR + 1e8) / tLotSize},
			{Value: second, MaxSwapCount: second / tLotSize},
		}, MaxFeeRate: tDCR.MaxFeeRate, FeeSuggestion: feeSuggestion,
			Options: map[string]string{multiSplitKey: map[bool]string{true: "true", false: "false"}[split]}}
		coinSets, _, _, err := w.FundMultiOrder(mo, 0)
		if err == nil && len(coinSets) != len(mo.Values) {
			err = fmt.Errorf("funded %d of %d orders", len(coinSets), len(mo.Values)) // best effort funds what it can
		}
		var all asset.Coins
		for _, cs := range coinSets {
			all = append(all, cs...)
		}
		return all, err
	}
	bondKey := dcrsecp256k1.PrivKeyFromBytes(append(make([]byte, 31), 1))
	addr := tPKHAddr.String()

	cases := []struct {
		name     string
		wantSent bool
		run      func(*ExchangeWallet) (asset.Coins, error)
	}{
		{"FundOrder", false, func(w *ExchangeWallet) (asset.Coins, error) {
			coins, _, _, err := w.FundOrder(order(false))
			return coins, err
		}},
		{"FundOrder split", true, func(w *ExchangeWallet) (asset.Coins, error) {
			coins, _, _, err := w.FundOrder(order(true))
			return coins, err
		}},
		{"FundMultiOrder", false, func(w *ExchangeWallet) (asset.Coins, error) {
			return multiOrder(w, false)
		}},
		{"FundMultiOrder split", true, func(w *ExchangeWallet) (asset.Coins, error) {
			return multiOrder(w, true)
		}},
		{"Send", true, func(w *ExchangeWallet) (asset.Coins, error) {
			_, err := w.Send(addr, largestVAR+1e8, feeSuggestion)
			return nil, err
		}},
		{"Withdraw", true, func(w *ExchangeWallet) (asset.Coins, error) {
			_, err := w.Withdraw(addr, largestVAR+1e8, feeSuggestion)
			return nil, err
		}},
		{"MakeBondTx", false, func(w *ExchangeWallet) (asset.Coins, error) {
			bond, _, err := w.MakeBondTx(0, largestVAR+1e8, 20, time.Now().Add(12*time.Hour), bondKey, make([]byte, 32))
			if err != nil {
				return nil, err
			}
			tx, err := msgTxFromBytes(bond.SignedTx) // built, not broadcast: check its inputs
			if err != nil {
				return nil, err
			}
			var ins asset.Coins
			for _, in := range tx.TxIn {
				ins = append(ins, newOutput(&in.PreviousOutPoint.Hash, in.PreviousOutPoint.Index, 0, wire.TxTreeRegular))
			}
			return ins, nil
		}},
	}

	for fxName, fx := range fixtures {
		for _, tc := range cases {
			t.Run(fxName+"/"+tc.name, func(t *testing.T) {
				wallet, node, shutdown := tNewWallet() // fresh: the stub's listunspent ignores locks
				defer shutdown()
				wallet.wallet.(*rpcWallet).accountsV.Store(XCWalletAccounts{PrimaryAccount: tAcctName, UnmixedAccount: "tUnmixedAcct", TradingAccount: tTradingAcct})
				node.unspent = fx
				node.newAddr, node.changeAddr = tPKHAddr, tPKHAddr
				unmixed, trading := tAcctBal("0"), tAcctBal("0")
				unmixed.AccountName, trading.AccountName = "tUnmixedAcct", tTradingAcct
				node.balanceResult = &walletjson.GetBalanceResult{
					Balances: []walletjson.GetAccountBalanceResult{tAcctBal("15"), unmixed, trading},
				}
				wallet.config().useSplitTx = true

				coins, err := tc.run(wallet)
				if err != nil {
					t.Fatalf("error: %v", err) // a driver that picks an avoided output and then fails must not pass
				}
				for _, c := range coins {
					h, v, _ := decodeCoinID(c.ID())
					if avoid[newOutPoint(h, v)] {
						t.Errorf("returned avoided coin %s", c)
					}
				}
				for _, op := range node.lockedCoins {
					if avoid[newOutPoint(&op.Hash, op.Index)] {
						t.Errorf("locked avoided outpoint %s", op)
					}
				}
				if node.sentRawTx == nil {
					if tc.wantSent {
						t.Fatal("no transaction sent") // e.g. a split that fell back to standard funding
					}
					return
				}
				for _, in := range node.sentRawTx.TxIn {
					if op := in.PreviousOutPoint; avoid[newOutPoint(&op.Hash, op.Index)] {
						t.Errorf("spent avoided outpoint %s", op)
					}
				}
			})
		}
		// FundingCoins' last resort looks a coin up in listunspent, and must not find one.
		t.Run(fxName+"/FundingCoins", func(t *testing.T) {
			wallet, node, shutdown := tNewWallet()
			defer shutdown()
			wallet.wallet.(*rpcWallet).accountsV.Store(XCWalletAccounts{PrimaryAccount: tAcctName, UnmixedAccount: "tUnmixedAcct", TradingAccount: tTradingAcct})
			node.unspent = fx
			for op := range avoid {
				if coins, err := wallet.FundingCoins([]dex.Bytes{toCoinID(&op.txHash, op.vout)}); err == nil {
					t.Errorf("FundingCoins found avoided coin %s", coins[0])
				}
			}
		})
	}
}
