//go:build !harness && !vspd

package mon

// Tests for the items of docs/var-port/invariants.md, over the RPC wallet and,
// where named SPV, the native SPV wallet.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
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
	"github.com/monetarium/monetarium-node/dcrec/secp256k1"
	"github.com/monetarium/monetarium-node/dcrutil"
	"github.com/monetarium/monetarium-node/hdkeychain"
	chainjson "github.com/monetarium/monetarium-node/rpc/jsonrpc/types"
	"github.com/monetarium/monetarium-node/txscript"
	"github.com/monetarium/monetarium-node/txscript/sign"
	"github.com/monetarium/monetarium-node/txscript/stdaddr"
	"github.com/monetarium/monetarium-node/wire"
	walletjson "github.com/monetarium/monetarium-wallet/rpc/jsonrpc/types"
	"github.com/monetarium/monetarium-wallet/wallet"
	"github.com/monetarium/monetarium-wallet/wallet/udb"
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

// TestFeeOracle is item 7 for the native SPV wallet, which has no
// estimatesmartfee and asks the network's explorer instead: Monetarium's on
// mainnet and testnet, none on simnet. An oracle answer below the node's 10
// atoms/B minimum, such as a Decred-shaped one, is raised to it. With the
// oracle unreachable or absent FeeRate gives no rate (0), and core falls back
// to the server's. In every case a send, given FeeRate's answer as core
// gives it, pays at least 10 atoms/B, as it does for a low suggestion or a
// low configured fallback rate.
func TestFeeOracle(t *testing.T) {
	for net, want := range map[dex.Network]string{
		dex.Mainnet: "https://monetarium.online/insight/api/utils/estimatefee?nbBlocks=%d",
		dex.Testnet: "https://testnet.monetarium.online/insight/api/utils/estimatefee?nbBlocks=%d",
		dex.Simnet:  "",
	} {
		if got := feeOracleURL(net); got != want {
			t.Errorf("%s oracle %q, want %q", net, got, want)
		}
	}

	// Every rate is raised to 10, so a lower limit is refused.
	for limit, ok := range map[float64]bool{0.00009: false, 0.0001: true} { // coins/kB: 9 and 10 atoms/B
		if _, err := getExchangeWalletCfg(&walletConfig{FeeRateLimit: limit}, tLogger); (err == nil) != ok {
			t.Errorf("fee rate limit %v coins/kB: error %v, want refused = %v", limit, err, !ok)
		}
	}

	var calls int
	oracle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query() // answered as insight does: keyed by the block count asked for
		fmt.Fprintf(w, `{%q:%s}`, q.Get("nbBlocks"), q.Get("rate"))
	}))
	defer oracle.Close()

	for _, tt := range []struct {
		name       string
		url        string // "" is no oracle
		suggestion uint64 // the send's rate if not FeeRate's
		fallback   uint64
		wantRate   uint64 // FeeRate
		wantCalls  int
	}{
		{name: "unreachable", url: "http://127.0.0.1:0/?nbBlocks=%d"}, // port 0 is never listening
		{name: "below 10 atoms/B", url: oracle.URL + `/?rate=0.00009&nbBlocks=%d`, wantRate: 10, wantCalls: 1},
		{name: "valid", url: oracle.URL + `/?rate=0.0002&nbBlocks=%d`, wantRate: 20, wantCalls: 1},
		{name: "no oracle"},
		{name: "low suggestion", suggestion: 5},
		{name: "low fallback", fallback: 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newTSigner(t)
			seam := tSPVSignSeam(t, s) // fresh: the wallet caches oracle answers and failures
			defer seam.shutdown()
			seam.w.feeOracleURL = tt.url
			cfg := seam.w.config()
			cfg.apiFeeFallback = true
			if tt.fallback != 0 {
				cfg.fallbackFeeRate = tt.fallback
			}
			calls = 0
			rate := seam.w.FeeRate()
			if rate != tt.wantRate {
				t.Errorf("FeeRate = %d atoms/B, want %d", rate, tt.wantRate)
			}
			if calls != tt.wantCalls {
				t.Errorf("oracle asked %d times, want %d", calls, tt.wantCalls)
			}
			if tt.url == "" && seam.w.oracleFailing {
				t.Error("no oracle, but the wallet tried one") // a URL left unset is not a failing oracle
			}
			if tt.suggestion != 0 {
				rate = tt.suggestion
			}
			if _, err := seam.w.Send(s.addr.String(), 1e8, rate); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if len(seam.sent()) != 1 {
				t.Fatalf("%d transactions sent, want 1", len(seam.sent()))
			}
			tx := seam.sent()[0]
			fee := int64(0)
			for _, in := range tx.TxIn {
				fee += []int64{4e8, 5e8, 6e8}[in.PreviousOutPoint.Index] // tSignUnspents' amounts
			}
			for _, out := range tx.TxOut {
				fee -= out.Value
			}
			if size := int64(tx.SerializeSize()); fee < int64(dexmon.MinRelayFeeRate)*size {
				t.Errorf("send pays %d atoms for %d bytes, below %d atoms/B", fee, size, dexmon.MinRelayFeeRate)
			}
		})
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
	fixtures, ska := skaFundingFixtures()
	testFundingAvoids(t, tRPCSeam, fixtures, ska)
}

// TestSPVFundingIgnoresSKA is TestFundingIgnoresSKA over the native SPV wallet.
func TestSPVFundingIgnoresSKA(t *testing.T) {
	fixtures, ska := skaFundingFixtures()
	testFundingAvoids(t, tSPVSeam, fixtures, ska)
}

// TestTransferAccountIgnoresSKA is item 6 for the native wallet's account
// transfer: stopping mixing sweeps the mixed and trading accounts into the
// default one, and the simnet mixer sweeps the default account into the mixed
// one. Every account holds SKA outputs next to VAR ones; each sweep, run with
// mixing on, must spend every VAR output of its accounts and no SKA one.
func TestTransferAccountIgnoresSKA(t *testing.T) {
	var round []walletjson.ListUnspentResult
	add := func(vout uint32, amt, acct string, coinType uint8) {
		u := tUnspent(vout, amt)
		u.Account, u.CoinType = acct, coinType
		round = append(round, *u)
	}
	add(0, "4", mixedAccountName, 0)
	add(1, "5", tradingAccountName, 0)
	add(2, "6", defaultAccountName, 0)
	add(10, "1000", mixedAccountName, 1)
	add(11, "5", tradingAccountName, 1)
	add(12, "1000", tradingAccountName, 2)
	add(13, "1000", defaultAccountName, 1)
	fractional := slices.Clone(round)
	for i, acct := range []string{mixedAccountName, defaultAccountName} {
		u := tUnspent(uint32(14+i), "0.123456789012345678") // more decimals than VAR has
		u.Account, u.CoinType = acct, 1
		fractional = append(fractional, *u)
	}

	sweeps := []struct {
		name string
		to   string
		from []string
		vars []uint32
	}{
		{"stop mixing", defaultAccountName, []string{mixedAccountName, tradingAccountName}, []uint32{0, 1}},
		{"simnet mixer", mixedAccountName, []string{defaultAccountName}, []uint32{2}},
	}
	for name, fx := range map[string][]walletjson.ListUnspentResult{"round SKA": round, "with fractional SKA1": fractional} {
		for _, sw := range sweeps {
			t.Run(name+"/"+sw.name, func(t *testing.T) {
				nw, dcrw, shutdown := tNewNativeWallet()
				defer shutdown()
				nw.spvw.setAccounts(true)
				for i := range fx {
					dcrw.unspents = append(dcrw.unspents, &fx[i])
				}
				if err := nw.transferAccount(nw.ctx, sw.to, sw.from...); err != nil {
					t.Fatalf("transferAccount: %v", err)
				}
				if len(dcrw.sentTxs) != 1 {
					t.Fatalf("%d transactions sent, want 1", len(dcrw.sentTxs))
				}
				var spent, want []outPoint
				for _, in := range dcrw.sentTxs[0].TxIn {
					spent = append(spent, newOutPoint(&in.PreviousOutPoint.Hash, in.PreviousOutPoint.Index))
				}
				for _, vout := range sw.vars {
					want = append(want, newOutPoint(tTxHash, vout))
				}
				if !reflect.DeepEqual(spent, want) {
					t.Errorf("spent %v, want the VAR outputs %v", spent, want)
				}
			})
		}
	}
}

// skaFundingFixtures are item 6's listunspent fixtures and the SKA outpoints
// in them.
func skaFundingFixtures() (map[string][]walletjson.ListUnspentResult, map[outPoint]bool) {
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
	return fixtures, ska
}

// TestStakerFeeNotSelected is item 8: an output of a staker-fee transaction
// must never fund a DEX transaction, however old. A miner can respend it in a
// block without the owner's key, as an augmented staker-fee input. The wallet
// lists it once matured, spendable, with an OP_SSGEN-tagged P2PKH script.
func TestStakerFeeNotSelected(t *testing.T) {
	testStakerFeeNotSelected(t, tRPCSeam)
}

// TestSPVStakerFeeNotSelected is TestStakerFeeNotSelected over the native SPV
// wallet, whose account transfer must not sweep a staker-fee output either.
func TestSPVStakerFeeNotSelected(t *testing.T) {
	testStakerFeeNotSelected(t, tSPVSeam)

	nw, dcrw, shutdown := tNewNativeWallet()
	defer shutdown()
	nw.spvw.setAccounts(true)
	fee := tStakerFeeUnspent(t, 20, "1000", 100_000)
	for _, u := range []*walletjson.ListUnspentResult{tUnspent(0, "4"), fee} {
		u.Account = mixedAccountName
		dcrw.unspents = append(dcrw.unspents, u)
	}
	if err := nw.transferAccount(nw.ctx, defaultAccountName, mixedAccountName); err != nil {
		t.Fatalf("transferAccount: %v", err)
	}
	if len(dcrw.sentTxs) != 1 {
		t.Fatalf("%d transactions sent, want 1", len(dcrw.sentTxs))
	}
	for _, in := range dcrw.sentTxs[0].TxIn {
		if in.PreviousOutPoint.Index != 0 {
			t.Errorf("transferAccount spent %s, want only the VAR output", in.PreviousOutPoint)
		}
	}
}

// TestStakerFeeNotAvailable is item 8 in Balance: the wallet counts a matured
// staker-fee output as spendable, and funding never takes one, so Available
// leaves it out. Otherwise a withdrawal of all of Available cannot be funded.
// An SKA staker-fee output is not in the VAR balance at all, nor is one the
// wallet lists as not spendable; a vote's reward funds orders and stays in.
func TestStakerFeeNotAvailable(t *testing.T) {
	for name, newSeam := range map[string]func([]walletjson.ListUnspentResult) *tSeam{"rpc": tRPCSeam, "spv": tSPVSeam} {
		ska, unspendable := tStakerFeeUnspent(t, 22, "1", 1000), tStakerFeeUnspent(t, 23, "2", 1000)
		ska.CoinType, unspendable.Spendable = 1, false
		vote := tStakerFeeUnspent(t, 1, "3", 1000) // a vote's reward funds orders, so it stays in Available
		vote.TxType = int(stake.TxTypeSSGen)
		seam := newSeam([]walletjson.ListUnspentResult{*tUnspent(0, "12"), *vote,
			*tStakerFeeUnspent(t, 20, "0.75", 16), *tStakerFeeUnspent(t, 21, "0.25", 1000), *ska, *unspendable})
		bal, err := seam.w.Balance() // getbalance reports 16 VAR spendable, the staker fees included
		seam.shutdown()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if bal.Available != 15e8 {
			t.Errorf("%s: Available %d, want 15e8: the 1 VAR of staker-fee outputs left out", name, bal.Available)
		}
	}

	// A block between getbalance and listunspent matured more staker fees than getbalance's
	// spendable amount: each wallet's AccountBalance stops at 0, without an error.
	rw, rnode, rshutdown := tNewWallet()
	defer rshutdown()
	rnode.balanceResult = &walletjson.GetBalanceResult{Balances: []walletjson.GetAccountBalanceResult{tAcctBal("0.5")}}
	rnode.unspent = []walletjson.ListUnspentResult{*tStakerFeeUnspent(t, 20, "0.75", 16)}
	if bal, err := rw.Balance(); err != nil || bal.Available != 0 {
		t.Errorf("rpc: staker fees above spendable: Available %+v (%v), want 0", bal, err)
	}
	nw, dcrw, nshutdown := tNewNativeWallet()
	defer nshutdown()
	dcrw.acctBal = wallet.Balances{Spendable: 5e7, Total: 5e7}
	dcrw.unspents = []*walletjson.ListUnspentResult{tStakerFeeUnspent(t, 20, "0.75", 16)}
	if bal, err := nw.Balance(); err != nil || bal.Available != 0 {
		t.Errorf("spv: staker fees above spendable: Available %+v (%v), want 0", bal, err)
	}

	// A listunspent that fails, or lists a staker-fee amount that does not parse, fails Balance
	// rather than leave the staker fees in Available.
	bad := tStakerFeeUnspent(t, 20, "0.123456789", 16)
	rnode.unspentErr, dcrw.listUnspentErr = tErr, tErr
	if _, err := rw.Balance(); err == nil {
		t.Error("rpc: Balance with listunspent failing: no error")
	}
	if _, err := nw.Balance(); err == nil {
		t.Error("spv: Balance with listunspent failing: no error")
	}
	rnode.unspentErr, dcrw.listUnspentErr = nil, nil
	rnode.unspent, dcrw.unspents = []walletjson.ListUnspentResult{*bad}, []*walletjson.ListUnspentResult{bad}
	if _, err := rw.Balance(); err == nil {
		t.Error("rpc: Balance with a staker-fee amount of 9 decimals: no error")
	}
	if _, err := nw.Balance(); err == nil {
		t.Error("spv: Balance with a staker-fee amount of 9 decimals: no error")
	}

	// A block between getbalance and listunspent matured a staker-fee output that getbalance
	// did not count, with all the regular VAR locked: Available is 0, not wrapped past it.
	wallet, node, shutdown := tNewWallet()
	defer shutdown()
	node.balanceResult = &walletjson.GetBalanceResult{Balances: []walletjson.GetAccountBalanceResult{tAcctBal("1")}}
	node.unspent = []walletjson.ListUnspentResult{*tStakerFeeUnspent(t, 20, "0.5", 16)}
	node.lluCoins = []walletjson.ListUnspentResult{*tUnspent(0, "1")}
	if bal, err := wallet.Balance(); err != nil || bal.Available != 0 {
		t.Errorf("staker fees matured between the calls: Available %+v (%v), want 0", bal, err)
	}
	// The same in the trading account, read on its own with mixing on.
	wallet.wallet.(*rpcWallet).accountsV.Store(tTestAccounts)
	trading, unmixed := tAcctBal("1"), tAcctBal("0")
	trading.AccountName, unmixed.AccountName = tTestAccounts.TradingAccount, tTestAccounts.UnmixedAccount
	node.balanceResult.Balances = []walletjson.GetAccountBalanceResult{tAcctBal("0"), unmixed, trading}
	for _, u := range []*walletjson.ListUnspentResult{&node.unspent[0], &node.lluCoins[0]} {
		u.Account = tTestAccounts.TradingAccount
	}
	if bal, err := wallet.Balance(); err != nil || bal.Available != 0 {
		t.Errorf("trading account, staker fees matured between the calls: Available %+v (%v), want 0", bal, err)
	}
}

// tStakerFeeUnspent is a matured staker-fee output paying tPKHAddr, as the
// wallet lists it.
func tStakerFeeUnspent(t *testing.T, vout uint32, amt string, confs int64) *walletjson.ListUnspentResult {
	t.Helper()
	u := tUnspent(vout, amt)
	u.TxType, u.Tree, u.Confirmations = int(stake.TxTypeSSFee), wire.TxTreeStake, confs
	u.ScriptPubKey = hex.EncodeToString(tStakePayout(t))
	return u
}

// tStakePayout is the OP_SSGEN-tagged P2PKH script of tPKHAddr.
func tStakePayout(t *testing.T) []byte {
	t.Helper()
	payout, err := stake.ConsolidationAddrToPkScript(tPKHAddr.(*stdaddr.AddressPubKeyHashEcdsaSecp256k1V0).Hash160()[:])
	if err != nil {
		t.Fatal(err)
	}
	return payout
}

func testStakerFeeNotSelected(t *testing.T, newSeam func([]walletjson.ListUnspentResult) *tSeam) {
	var unspents []walletjson.ListUnspentResult
	for i, amt := range []string{"4", "5", "6"} {
		unspents = append(unspents, *tUnspent(uint32(i), amt))
	}
	avoid := make(map[outPoint]bool)
	for vout, o := range map[uint32]struct {
		amt   string
		confs int64
	}{20: {"1000", 16}, 21: {"3", 100_000}} { // just matured on simnet, and old
		unspents = append(unspents, *tStakerFeeUnspent(t, vout, o.amt, o.confs))
		avoid[newOutPoint(tTxHash, vout)] = true
	}
	trading := slices.Clone(unspents)
	for i := range trading {
		if trading[i].TxType == int(stake.TxTypeSSFee) {
			trading[i].Account = tTradingAcct // the trading account is read on its own
		}
	}
	testFundingAvoids(t, newSeam, map[string][]walletjson.ListUnspentResult{"staker fee": unspents,
		"staker fee in the trading account": trading}, avoid)

	// Only the staker-fee type goes: the owner's vote and revocation outputs still fund an order
	// that the 1 VAR regular output cannot.
	for _, txType := range []stake.TxType{stake.TxTypeSSGen, stake.TxTypeSSRtx} {
		reward := tUnspent(1, "10")
		reward.TxType, reward.Tree, reward.Confirmations = int(txType), wire.TxTreeStake, 1000
		reward.ScriptPubKey = hex.EncodeToString(tStakePayout(t))
		seam := newSeam([]walletjson.ListUnspentResult{*tUnspent(0, "1"), *reward})
		_, _, _, err := seam.w.FundOrder(&asset.Order{Version: version, Value: 5e8, MaxSwapCount: 5e8 / tLotSize,
			MaxFeeRate: tDCR.MaxFeeRate, FeeSuggestion: feeSuggestion})
		seam.shutdown()
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
func testFundingAvoids(t *testing.T, newSeam func([]walletjson.ListUnspentResult) *tSeam,
	fixtures map[string][]walletjson.ListUnspentResult, avoid map[outPoint]bool) {
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
				seam := newSeam(fx) // fresh: the stubs' listunspent ignores locks
				defer seam.shutdown()
				seam.w.config().useSplitTx = true

				coins, err := tc.run(seam.w)
				if err != nil {
					t.Fatalf("error: %v", err) // a driver that picks an avoided output and then fails must not pass
				}
				for _, c := range coins {
					h, v, _ := decodeCoinID(c.ID())
					if avoid[newOutPoint(h, v)] {
						t.Errorf("returned avoided coin %s", c)
					}
				}
				for _, op := range seam.locked() {
					if avoid[newOutPoint(&op.Hash, op.Index)] {
						t.Errorf("locked avoided outpoint %s", op)
					}
				}
				sent := seam.sent()
				if len(sent) == 0 && tc.wantSent {
					t.Fatal("no transaction sent") // e.g. a split that fell back to standard funding
				}
				for _, tx := range sent {
					for _, in := range tx.TxIn {
						if op := in.PreviousOutPoint; avoid[newOutPoint(&op.Hash, op.Index)] {
							t.Errorf("spent avoided outpoint %s", op)
						}
					}
				}
			})
		}
		// FundingCoins' last resort looks a coin up in listunspent, and must not find one.
		t.Run(fxName+"/FundingCoins", func(t *testing.T) {
			seam := newSeam(fx)
			defer seam.shutdown()
			for op := range avoid {
				if coins, err := seam.w.FundingCoins([]dex.Bytes{toCoinID(&op.txHash, op.vout)}); err == nil {
					t.Errorf("FundingCoins found avoided coin %s", coins[0])
				}
			}
		})
	}
}

// tSeam is an ExchangeWallet over a stubbed wallet backend, holding 15 VAR
// spendable in the primary account, and what the wallet locked and sent.
type tSeam struct {
	w        *ExchangeWallet
	locked   func() []*wire.OutPoint // every outpoint locked
	sent     func() []*wire.MsgTx    // every transaction sent
	shutdown func()
}

// tTestAccounts are the accounts the funding seams fund from: mixing on, so
// the trading account is read on its own.
var tTestAccounts = XCWalletAccounts{PrimaryAccount: tAcctName, UnmixedAccount: "tUnmixedAcct", TradingAccount: tTradingAcct}

// tRPCSeam is the seam over the RPC wallet, which lists fx.
func tRPCSeam(fx []walletjson.ListUnspentResult) *tSeam {
	wallet, node, shutdown := tNewWallet()
	wallet.wallet.(*rpcWallet).accountsV.Store(tTestAccounts)
	node.unspent = fx
	node.newAddr, node.changeAddr = tPKHAddr, tPKHAddr
	sf := tStakerFeeSums(fx)
	primary := tAcctBal(varAtomsToDecimalString(dcrutil.Amount(15e8 + sf[tTestAccounts.PrimaryAccount])))
	unmixed, trading := tAcctBal("0"), tAcctBal(varAtomsToDecimalString(dcrutil.Amount(sf[tTestAccounts.TradingAccount])))
	unmixed.AccountName, trading.AccountName = tTestAccounts.UnmixedAccount, tTestAccounts.TradingAccount
	node.balanceResult = &walletjson.GetBalanceResult{
		Balances: []walletjson.GetAccountBalanceResult{primary, unmixed, trading},
	}
	return &tSeam{w: wallet, shutdown: shutdown,
		locked: func() []*wire.OutPoint { return node.lockedCoins },
		sent:   func() []*wire.MsgTx { return node.sentRawTxs },
	}
}

// tSPVSeam is the seam over the native SPV wallet, which lists fx.
func tSPVSeam(fx []walletjson.ListUnspentResult) *tSeam {
	nw, dcrw, shutdown := tNewNativeWallet()
	nw.spvw.accts.Store(tTestAccounts)
	for i := range fx {
		dcrw.unspents = append(dcrw.unspents, &fx[i])
	}
	dcrw.acctNums = map[string]uint32{tTestAccounts.PrimaryAccount: 0, tTestAccounts.UnmixedAccount: 1, tTestAccounts.TradingAccount: 2}
	sf := tStakerFeeSums(fx)
	dcrw.acctBals = map[uint32]wallet.Balances{
		0: {Spendable: dcrutil.Amount(15e8 + sf[tTestAccounts.PrimaryAccount]), Total: dcrutil.Amount(15e8 + sf[tTestAccounts.PrimaryAccount])}, 1: {},
		2: {Spendable: dcrutil.Amount(sf[tTestAccounts.TradingAccount])}}
	return &tSeam{w: nw.ExchangeWallet, shutdown: shutdown,
		locked: func() []*wire.OutPoint { return dcrw.lockedOutpoints },
		sent:   func() []*wire.MsgTx { return dcrw.sentTxs },
	}
}

// tStakerFeeSums sums fx's VAR staker-fee outputs per account: the wallet
// counts them in getbalance's spendable, so the seams' balances do too.
func tStakerFeeSums(fx []walletjson.ListUnspentResult) map[string]uint64 {
	sums := make(map[string]uint64)
	for i := range fx {
		if u := &fx[i]; isStakerFee(u) && u.CoinType == 0 && u.Spendable {
			v, err := coinStrToAtoms(u.Amount)
			if err != nil {
				panic(err)
			}
			sums[u.Account] += v
		}
	}
	return sums
}

// verifyFlags are the node's standard script flags: mempool's
// BaseStandardVerifyFlags plus the SHA256 and treasury flags its
// standardScriptVerifyFlags adds once those agendas are active, which
// Monetarium's simnet params force from the start (mainnet and testnet force
// only SHA256); these tests run on simnet. Copied, as the node exports
// neither; step 7's broadcast against the node catches drift.
const verifyFlags = txscript.ScriptDiscourageUpgradableNops | txscript.ScriptVerifyCleanStack |
	txscript.ScriptVerifyCheckLockTimeVerify | txscript.ScriptVerifyCheckSequenceVerify |
	txscript.ScriptVerifySHA256 | txscript.ScriptVerifyTreasury

// tSigner is a wallet key for the stub's signrawtransaction, which signs every
// input as a P2PKH spend of it, as the wallet does for the funding outputs.
type tSigner struct {
	priv     []byte
	addr     stdaddr.Address
	pkScript []byte
}

func newTSigner(t *testing.T) *tSigner {
	t.Helper()
	priv := secp256k1.PrivKeyFromBytes(append(make([]byte, 31), 7))
	addr, err := stdaddr.NewAddressPubKeyHashEcdsaSecp256k1V0(stdaddr.Hash160(priv.PubKey().SerializeCompressed()), tChainParams)
	if err != nil {
		t.Fatal(err)
	}
	_, pkScript := addr.PaymentScript()
	return &tSigner{priv.Serialize(), addr, pkScript}
}

func (s *tSigner) sign(tx *wire.MsgTx) (*wire.MsgTx, bool, error) {
	return tx, true, s.signHashType(tx, txscript.SigHashAll)
}

// signHashType signs in place with the hash type the caller asked for, as
// the SPV wallet library does.
func (s *tSigner) signHashType(tx *wire.MsgTx, hashType txscript.SigHashType) error {
	for i := range tx.TxIn {
		sigScript, err := sign.SignatureScript(tx, i, s.pkScript, hashType, s.priv, dcrec.STEcdsaSecp256k1, true)
		if err != nil {
			return err
		}
		tx.TxIn[i].SignatureScript = sigScript
	}
	return nil
}

// verifyTx runs every input of tx through Monetarium's script engine.
// prevScripts maps an outpoint to the script it pays; any other input spends
// a P2PKH output of the signer.
func verifyTx(t *testing.T, kind string, tx *wire.MsgTx, s *tSigner, prevScripts map[outPoint][]byte) {
	t.Helper()
	for i, in := range tx.TxIn {
		pkScript, ok := prevScripts[newOutPoint(&in.PreviousOutPoint.Hash, in.PreviousOutPoint.Index)]
		if !ok {
			pkScript = s.pkScript
		}
		// Every spend here starts with its signature; any valid hash type verifies,
		// so check that it is SigHashAll.
		if sigScript := in.SignatureScript; len(sigScript) == 0 || int(sigScript[0]) >= len(sigScript) ||
			txscript.SigHashType(sigScript[sigScript[0]]) != txscript.SigHashAll {
			t.Errorf("%s input %d: signature is not SigHashAll", kind, i)
		}
		vm, err := txscript.NewEngine(pkScript, tx, i, verifyFlags, 0, nil)
		if err == nil {
			err = vm.Execute()
		}
		if err != nil {
			t.Errorf("%s input %d does not verify: %v", kind, i, err)
		}
	}
}

// TestSignVerify is item 2: each kind of transaction the driver builds, built
// through the ExchangeWallet methods, verifies under Monetarium's script
// engine, and the txid in the coin ID the method returns is the txid of the
// bytes it sent. Redeem, refund and bond refund are signed by the driver;
// the other kinds by the stub's signer, standing in for the wallet's.
func TestSignVerify(t *testing.T) {
	testSignVerify(t, tRPCSignSeam)
}

// TestSPVSignVerify is TestSignVerify over the native SPV wallet.
func TestSPVSignVerify(t *testing.T) {
	testSignVerify(t, tSPVSignSeam)
}

// tSignSeam is an ExchangeWallet whose wallet signs for s and lists 4, 5 and
// 6 VAR paying s, 15 VAR spendable.
type tSignSeam struct {
	w        *ExchangeWallet
	sent     func() []*wire.MsgTx // every transaction sent, cleared by reset
	reset    func()
	shutdown func()
	// unspentContract makes the wallet find the contract output vout of tx,
	// paying contractScript, unspent and confirmed.
	unspentContract func(tx *wire.MsgTx, vout uint32, contractScript []byte)
}

func tSignUnspents(s *tSigner) []walletjson.ListUnspentResult {
	var unspents []walletjson.ListUnspentResult
	for i, amt := range []string{"4", "5", "6"} {
		u := tUnspent(uint32(i), amt)
		u.Address, u.ScriptPubKey = s.addr.String(), hex.EncodeToString(s.pkScript)
		unspents = append(unspents, *u)
	}
	return unspents
}

func tRPCSignSeam(t *testing.T, s *tSigner) *tSignSeam {
	wallet, node, shutdown := tNewWallet()
	node.signFunc = s.sign
	node.newAddr, node.changeAddr = s.addr, s.addr
	var err error
	node.privWIF, err = dcrutil.NewWIF(s.priv, tChainParams.PrivateKeyID, dcrec.STEcdsaSecp256k1)
	if err != nil {
		t.Fatal(err)
	}
	node.unspent = tSignUnspents(s)
	node.balanceResult = &walletjson.GetBalanceResult{Balances: []walletjson.GetAccountBalanceResult{tAcctBal("15")}}
	return &tSignSeam{w: wallet, shutdown: shutdown,
		sent:  func() []*wire.MsgTx { return node.sentRawTxs },
		reset: func() { node.sentRawTxs = nil },
		unspentContract: func(tx *wire.MsgTx, vout uint32, contractScript []byte) {
			tipHash, _ := node.getBestBlock()
			txHash := tx.TxHash()
			node.txOutRes[newOutPoint(&txHash, vout)] = &chainjson.GetTxOutResult{
				Confirmations: 1, Value: dcrutil.Amount(tx.TxOut[vout].Value).ToCoin(), BestBlock: tipHash.String(),
				ScriptPubKey: chainjson.ScriptPubKeyResult{Hex: hex.EncodeToString(contractScript)},
			}
		},
	}
}

func tSPVSignSeam(t *testing.T, s *tSigner) *tSignSeam {
	nw, dcrw, shutdown := tNewNativeWallet()
	dcrw.signFunc = s.signHashType
	dcrw.extAddr, dcrw.intAddr = s.addr, s.addr
	dcrw.priv = secp256k1.PrivKeyFromBytes(s.priv)
	for _, u := range tSignUnspents(s) {
		dcrw.unspents = append(dcrw.unspents, &u)
	}
	return &tSignSeam{w: nw.ExchangeWallet, shutdown: shutdown,
		sent:  func() []*wire.MsgTx { return dcrw.sentTxs },
		reset: func() { dcrw.sentTxs = nil },
		unspentContract: func(tx *wire.MsgTx, vout uint32, _ []byte) {
			_, tipHeight := dcrw.MainChainTip(tCtx)
			dcrw.txDetails = &udb.TxDetails{TxRecord: udb.TxRecord{MsgTx: *tx},
				Block: udb.BlockMeta{Block: udb.Block{Height: tipHeight}}, Credits: []udb.CreditRecord{{Index: vout}}}
			dcrw.listTxs = []walletjson.ListTransactionsResult{{Vout: vout, Address: s.addr.String()}}
		},
	}
}

func testSignVerify(t *testing.T, newSeam func(*testing.T, *tSigner) *tSignSeam) {
	s := newTSigner(t)
	seam := newSeam(t, s)
	defer seam.shutdown()
	wallet := seam.w
	var err error
	prevScripts := make(map[outPoint][]byte)

	// sent checks that the method sent exactly one transaction, that it
	// verifies, and that coinID names it.
	sent := func(kind string, coinID []byte) *wire.MsgTx {
		t.Helper()
		if len(seam.sent()) != 1 {
			t.Fatalf("%s: %d transactions sent, want 1", kind, len(seam.sent()))
		}
		tx := seam.sent()[0]
		seam.reset()
		raw, err := tx.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := msgTxFromBytes(raw) // the txid of the bytes, by Monetarium's wire
		if err != nil {
			t.Fatal(err)
		}
		if h, _, err := decodeCoinID(coinID); err != nil || *h != decoded.TxHash() {
			t.Errorf("%s: coin ID names %v, sent %s (%v)", kind, h, decoded.TxHash(), err)
		}
		verifyTx(t, kind, decoded, s, prevScripts)
		return decoded
	}

	// Swap, and the refund it signs in advance.
	secret := sha256.Sum256([]byte("TestSignVerify"))
	secretHash := sha256.Sum256(secret[:])
	const swapVal = 3e8
	receipts, _, _, err := wallet.Swap(&asset.Swaps{
		Inputs: asset.Coins{newOutput(tTxHash, 0, 4e8, wire.TxTreeRegular)},
		Contracts: []*asset.Contract{{Address: s.addr.String(), Value: swapVal, SecretHash: secretHash[:],
			LockTime: uint64(time.Now().Add(time.Hour).Unix())}},
		FeeRate: feeSuggestion,
	})
	if err != nil {
		t.Fatalf("Swap: %v", err)
	}
	contract, contractCoin := receipts[0].Contract(), receipts[0].Coin()
	swapTx := sent("swap", contractCoin.ID())
	p2sh, _ := stdaddr.NewAddressScriptHashV0(contract, tChainParams)
	_, contractScript := p2sh.PaymentScript()
	contractHash, contractVout, _ := decodeCoinID(contractCoin.ID())
	prevScripts[newOutPoint(contractHash, contractVout)] = contractScript
	signedRefund, err := msgTxFromBytes(receipts[0].SignedRefund())
	if err != nil {
		t.Fatal(err)
	}
	verifyTx(t, "swap's signed refund", signedRefund, s, prevScripts)

	// Redeem the contract Swap built.
	_, redeemCoin, _, err := wallet.Redeem(&asset.RedeemForm{Redemptions: []*asset.Redemption{{
		Spends: &asset.AuditInfo{Coin: contractCoin, Contract: contract, Recipient: s.addr.String(),
			Expiration: time.Now().Add(time.Hour)},
		Secret: secret[:],
	}}})
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	sent("redeem", redeemCoin.ID())

	// Refund it.
	seam.unspentContract(swapTx, contractVout, contractScript)
	refundID, err := wallet.Refund(contractCoin.ID(), contract, feeSuggestion)
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	sent("refund", refundID)

	// Bond, its pre-signed refund, and a bond refund through RefundBond.
	bondKey := dcrsecp256k1.PrivKeyFromBytes(append(make([]byte, 31), 9))
	const bondAmt = 2e8
	bond, _, err := wallet.MakeBondTx(0, bondAmt, feeSuggestion, time.Now().Add(12*time.Hour), bondKey, make([]byte, 32))
	if err != nil {
		t.Fatalf("MakeBondTx: %v", err)
	}
	bondTx, err := msgTxFromBytes(bond.SignedTx)
	if err != nil {
		t.Fatal(err)
	}
	if h, _, _ := decodeCoinID(bond.CoinID); *h != bondTx.TxHash() {
		t.Errorf("bond: coin ID names %s, signed %s", h, bondTx.TxHash())
	}
	verifyTx(t, "bond", bondTx, s, prevScripts)
	bondHash, bondVout, _ := decodeCoinID(bond.CoinID)
	bondP2SH, _ := stdaddr.NewAddressScriptHashV0(bond.Data, tChainParams)
	_, prevScripts[newOutPoint(bondHash, bondVout)] = bondP2SH.PaymentScript()
	bondRefund, err := msgTxFromBytes(bond.RedeemTx)
	if err != nil {
		t.Fatal(err)
	}
	verifyTx(t, "bond's signed refund", bondRefund, s, prevScripts)
	refundCoin, err := wallet.RefundBond(tCtx, 0, bond.CoinID, bond.Data, bondAmt, bondKey)
	if err != nil {
		t.Fatalf("RefundBond: %v", err)
	}
	sent("bond refund", refundCoin.ID())

	// Send, withdraw, split.
	sendCoin, err := wallet.Send(s.addr.String(), 1e8, feeSuggestion)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	sent("send", sendCoin.ID())
	withdrawCoin, err := wallet.Withdraw(s.addr.String(), 1e8, feeSuggestion)
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	sent("withdraw", withdrawCoin.ID())
	wallet.config().useSplitTx = true
	const orderVal = 7e8 // no single output covers it, so the split must be sent
	coins, _, _, err := wallet.FundOrder(&asset.Order{Version: version, Value: orderVal, MaxSwapCount: orderVal / tLotSize,
		MaxFeeRate: tDCR.MaxFeeRate, FeeSuggestion: feeSuggestion, Options: map[string]string{"swapsplit": "true"}})
	if err != nil {
		t.Fatalf("FundOrder split: %v", err)
	}
	sent("split", coins[0].ID())
}

// minRelayVal is the value of the contract output minRelayTx spends.
const minRelayVal = 1e8

// minRelayTx builds the redeem or the refund of a fixed contract at 10
// atoms/B, signed with the key whose last byte is k, and returns the
// transaction sent and the lengths of R and S in its signature.
func minRelayTx(t *testing.T, kind string, k byte) (tx *wire.MsgTx, rLen, sLen int) {
	t.Helper()
	wallet, node, shutdown := tNewWallet()
	defer shutdown()
	priv := secp256k1.PrivKeyFromBytes(append(make([]byte, 31), k))
	addr, err := stdaddr.NewAddressPubKeyHashEcdsaSecp256k1V0(stdaddr.Hash160(priv.PubKey().SerializeCompressed()), tChainParams)
	if err != nil {
		t.Fatal(err)
	}
	node.newAddr, node.changeAddr = addr, addr
	if node.privWIF, err = dcrutil.NewWIF(priv.Serialize(), tChainParams.PrivateKeyID, dcrec.STEcdsaSecp256k1); err != nil {
		t.Fatal(err)
	}
	node.smartFee = "10000" // 10 atoms/B
	secret := sha256.Sum256([]byte("TestRedeemRefundAtMinRelay"))
	secretHash := sha256.Sum256(secret[:])
	lockTime := time.Unix(1_800_000_000, 0)
	contract, err := dexmon.MakeContract(addr.String(), addr.String(), secretHash[:], lockTime.Unix(), tChainParams)
	if err != nil {
		t.Fatal(err)
	}
	const val = minRelayVal
	coin := newOutput(tTxHash, 0, val, wire.TxTreeRegular)
	switch kind {
	case "redeem":
		_, _, _, err = wallet.Redeem(&asset.RedeemForm{Redemptions: []*asset.Redemption{{
			Spends: &asset.AuditInfo{Coin: coin, Contract: contract, Recipient: addr.String(), Expiration: lockTime},
			Secret: secret[:],
		}}})
	case "refund":
		p2sh, _ := stdaddr.NewAddressScriptHashV0(contract, tChainParams)
		_, pkScript := p2sh.PaymentScript()
		tipHash, _ := node.getBestBlock()
		node.txOutRes[newOutPoint(tTxHash, 0)] = &chainjson.GetTxOutResult{Confirmations: 1,
			Value: dcrutil.Amount(val).ToCoin(), BestBlock: tipHash.String(),
			ScriptPubKey: chainjson.ScriptPubKeyResult{Hex: hex.EncodeToString(pkScript)}}
		_, err = wallet.Refund(coin.ID(), contract, dexmon.MinRelayFeeRate)
	}
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	tx = node.sentRawTx
	sigScript := tx.TxIn[0].SignatureScript
	sig := sigScript[1 : 1+sigScript[0]] // the first push: DER signature and hash type
	rLen = int(sig[3])
	return tx, rLen, int(sig[4+rLen+1])
}

// TestRedeemRefundAtMinRelay is item 4: a redeem and a refund at the node's
// minimum relay fee rate, each with the largest signature a low-S signer
// makes (a 33-byte R and a 32-byte S), pay at least the node's minimum fee
// for their serialized size.
func TestRedeemRefundAtMinRelay(t *testing.T) {
	for _, kind := range []string{"redeem", "refund"} {
		// Grind the key for this transaction: signatures are deterministic
		// (RFC 6979), so the same k is found on every run of the same code.
		var tx *wire.MsgTx
		for k := byte(1); tx == nil; k++ {
			if k == 64 {
				t.Fatalf("%s: no key up to 63 gives a 33-byte R and a 32-byte S", kind)
			}
			if sent, rLen, sLen := minRelayTx(t, kind, k); rLen == 33 && sLen == 32 {
				tx = sent
			}
		}
		in, out := int64(minRelayVal), int64(0) // the contract output minRelayTx spends
		for _, txOut := range tx.TxOut {
			out += txOut.Value
		}
		size := int64(tx.SerializeSize())
		if minFee := size * dexmon.MinRelayFeeRate; in-out < minFee {
			t.Errorf("%s: fee %d atoms for %d bytes, under the node's minimum %d", kind, in-out, size, minFee)
		}
	}
}

// TestDustChange is item 4: at 10 atoms/B the node's dust threshold for a
// P2PKH output is 6060 atoms, Decred's 6030. A swap whose change, its own
// output's fee counted, would be 6040 atoms must leave it in the fee or pay
// at least 6060.
func TestDustChange(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()
	node.newAddr, node.changeAddr = tPKHAddr, tPKHAddr
	node.signFunc = func(tx *wire.MsgTx) (*wire.MsgTx, bool, error) { return signFunc(tx, dexmon.P2PKHSigScriptSize) }
	node.privWIF, _ = dcrutil.NewWIF(append(make([]byte, 31), 1), tChainParams.PrivateKeyID, dcrec.STEcdsaSecp256k1)
	const contractVal = 1e8
	swap := func(inVal uint64) *wire.MsgTx {
		t.Helper()
		_, _, _, err := wallet.Swap(&asset.Swaps{
			Inputs:    asset.Coins{newOutput(tTxHash, 0, inVal, wire.TxTreeRegular)},
			Contracts: []*asset.Contract{{Address: tPKHAddr.String(), Value: contractVal, SecretHash: make([]byte, 32), LockTime: 1_800_000_000}},
			FeeRate:   dexmon.MinRelayFeeRate,
		})
		if err != nil {
			t.Fatalf("Swap: %v", err)
		}
		return node.sentRawTx
	}
	withChange := swap(2 * contractVal) // sizes the swap with a change output
	if len(withChange.TxOut) != 2 {
		t.Fatalf("%d outputs, want contract and change", len(withChange.TxOut))
	}
	const change = 6040
	tx := swap(contractVal + uint64(withChange.SerializeSize())*dexmon.MinRelayFeeRate + change)
	for i, out := range tx.TxOut[1:] {
		if out.Value < 6060 {
			t.Errorf("change output %d of %d atoms, under the node's dust threshold of 6060", i+1, out.Value)
		}
	}
}

// nodeMaxFee is the node's fee ceiling for a VAR transaction of size bytes
// when high fees are not allowed: max(size × 1000, 1 000 000) atoms.
func nodeMaxFee(size int) int64 { return max(int64(size)*1000, 1_000_000) }

// TestFeeRateCeiling is item 3: no transaction the driver sends pays more than
// the node's ceiling, whatever rate it is asked for. Swaps run at a market
// maxFeeRate of 600 with the 2.0 swap fee bump, over leftovers that sweep the
// range a change output would call dust. Redeem, refund and bond refund build
// no change, so they keep all the room under the ceiling to outbid other
// transactions: asked for a high rate, each pays the flat 1 000 000.
func TestFeeRateCeiling(t *testing.T) {
	const highRate = 10_000 // atoms/B
	wallet, node, shutdown := tNewWallet()
	defer shutdown()
	wallet.config().feeRateLimit = 1e6 // a user may raise it; the node's ceiling stays
	node.smartFee = "10000000"         // highRate, in atoms/kB
	node.newAddr, node.changeAddr = tPKHAddr, tPKHAddr
	node.signFunc = func(tx *wire.MsgTx) (*wire.MsgTx, bool, error) { return signFunc(tx, dexmon.P2PKHSigScriptSize) }
	priv := append(make([]byte, 31), 1)
	addr, err := stdaddr.NewAddressPubKeyHashEcdsaSecp256k1V0(stdaddr.Hash160(secp256k1.PrivKeyFromBytes(priv).PubKey().SerializeCompressed()), tChainParams)
	if err != nil {
		t.Fatal(err)
	}
	if node.privWIF, err = dcrutil.NewWIF(priv, tChainParams.PrivateKeyID, dcrec.STEcdsaSecp256k1); err != nil {
		t.Fatal(err)
	}
	prevVal := make(map[outPoint]int64) // what each input spends, from the fixtures
	// checkSent checks the fee of every transaction sent since the last call
	// and returns the last one's fee and size.
	checkSent := func(kind string) (fee int64, size int) {
		t.Helper()
		if len(node.sentRawTxs) == 0 {
			t.Fatalf("%s: nothing sent", kind)
		}
		for _, tx := range node.sentRawTxs {
			fee = 0
			for _, in := range tx.TxIn {
				v, ok := prevVal[newOutPoint(&in.PreviousOutPoint.Hash, in.PreviousOutPoint.Index)]
				if !ok {
					t.Fatalf("%s spends unknown outpoint %s", kind, in.PreviousOutPoint)
				}
				fee += v
			}
			for _, out := range tx.TxOut {
				fee -= out.Value
			}
			if size = tx.SerializeSize(); fee > nodeMaxFee(size) {
				t.Errorf("%s: fee %d atoms for %d bytes, above the node's ceiling %d", kind, fee, size, nodeMaxFee(size))
			}
		}
		node.sentRawTxs = nil
		return fee, size
	}
	// At highRate each no-change transaction asks for more than the flat
	// 1 000 000, so it must pay exactly that: the most the node takes.
	outbids := func(kind string) {
		t.Helper()
		if fee, size := checkSent(kind); fee != 1_000_000 {
			t.Errorf("%s: fee %d atoms for %d bytes, want the node's flat ceiling 1000000", kind, fee, size)
		}
	}

	// Swaps of 5 and 6 inputs, about 900 and 1100 bytes: the first under the
	// flat 1 000 000, the second above 1000 bytes. The leftover after the
	// contract sweeps past what a change output at 1200 atoms/B would call dust.
	const contractVal = 1e8
	for _, n := range []uint32{5, 6} {
		var swapped int
		for left := int64(0); left < 2_000_000; left += 1999 {
			var coins asset.Coins
			for i := range n {
				v := int64(1e4)
				if i == 0 {
					v = contractVal + left - int64(n-1)*1e4
				}
				coins = append(coins, newOutput(tTxHash, 100+i, uint64(v), wire.TxTreeRegular))
				prevVal[newOutPoint(tTxHash, 100+i)] = v
			}
			_, _, _, err := wallet.Swap(&asset.Swaps{
				Inputs:    coins,
				Contracts: []*asset.Contract{{Address: tPKHAddr.String(), Value: contractVal, SecretHash: make([]byte, 32), LockTime: 1_800_000_000}},
				FeeRate:   600,
				Options:   map[string]string{swapFeeBumpKey: "2.0"},
			})
			if err != nil {
				if !strings.Contains(err.Error(), "not enough funds to cover minimum fee") {
					t.Fatalf("%d-input swap, %d atoms left: %v", n, left, err)
				}
				node.sentRawTxs = nil
				continue // too little left for the fee
			}
			swapped++
			if checkSent(fmt.Sprintf("%d-input swap, %d atoms left", n, left)); t.Failed() {
				t.FailNow()
			}
		}
		if swapped == 0 {
			t.Fatalf("no %d-input swap was sent", n)
		}
	}
	// A swap of 1000 bytes before change, where a clamp a little above 600
	// would leave dust change in a fee above the flat 1 000 000: 1 000 300
	// atoms over the contract is change at 600 atoms/B, dust at 609.
	node.signFunc = func(tx *wire.MsgTx) (*wire.MsgTx, bool, error) {
		signFunc(tx, dexmon.P2PKHSigScriptSize)
		tx.TxIn[0].SignatureScript = randBytes(dexmon.P2PKHSigScriptSize + 115) // pads 885 bytes to 1000
		return tx, true, nil
	}
	var coins asset.Coins
	for i := range uint32(5) {
		v := int64(1e4)
		if i == 0 {
			v = contractVal + 1_000_300 - 4e4
		}
		coins = append(coins, newOutput(tTxHash, 200+i, uint64(v), wire.TxTreeRegular))
		prevVal[newOutPoint(tTxHash, 200+i)] = v
	}
	if _, _, _, err := wallet.Swap(&asset.Swaps{
		Inputs:    coins,
		Contracts: []*asset.Contract{{Address: tPKHAddr.String(), Value: contractVal, SecretHash: make([]byte, 32), LockTime: 1_800_000_000}},
		FeeRate:   600,
		Options:   map[string]string{swapFeeBumpKey: "2.0"},
	}); err != nil {
		t.Fatalf("1000-byte swap: %v", err)
	}
	if len(node.sentRawTxs) != 1 {
		t.Fatalf("1000-byte swap: %d transactions sent, want 1", len(node.sentRawTxs))
	}
	tx := node.sentRawTxs[0]
	size := tx.SerializeSize()
	if len(tx.TxOut) == 2 {
		size -= tx.TxOut[1].SerializeSize() // change
	}
	if size != 1000 {
		t.Fatalf("1000-byte swap is %d bytes before change", size)
	}
	checkSent("1000-byte swap")
	node.signFunc = func(tx *wire.MsgTx) (*wire.MsgTx, bool, error) { return signFunc(tx, dexmon.P2PKHSigScriptSize) }

	// Redeem at the 2.0 redeem fee bump over the node's highRate estimate.
	secret := sha256.Sum256([]byte("TestFeeRateCeiling"))
	secretHash := sha256.Sum256(secret[:])
	contract, err := dexmon.MakeContract(addr.String(), addr.String(), secretHash[:], 1_800_000_000, tChainParams)
	if err != nil {
		t.Fatal(err)
	}
	coin := newOutput(tTxHash, 0, contractVal, wire.TxTreeRegular)
	prevVal[newOutPoint(tTxHash, 0)] = contractVal
	if _, _, _, err = wallet.Redeem(&asset.RedeemForm{Redemptions: []*asset.Redemption{{
		Spends: &asset.AuditInfo{Coin: coin, Contract: contract, Recipient: addr.String(), Expiration: time.Unix(1_800_000_000, 0)},
		Secret: secret[:],
	}}, Options: map[string]string{redeemFeeBumpFee: "2.0"}}); err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	outbids("redeem")
	// A small redeem the capped fee leaves well above the node's dust: its dust
	// check must run at the rate the fee pays, not the rate asked.
	const smallVal = 6e6
	smallCoin := newOutput(tTxHash, 3, smallVal, wire.TxTreeRegular)
	prevVal[newOutPoint(tTxHash, 3)] = smallVal
	if _, _, _, err = wallet.Redeem(&asset.RedeemForm{Redemptions: []*asset.Redemption{{
		Spends: &asset.AuditInfo{Coin: smallCoin, Contract: contract, Recipient: addr.String(), Expiration: time.Unix(1_800_000_000, 0)},
		Secret: secret[:],
	}}, Options: map[string]string{redeemFeeBumpFee: "2.0"}}); err != nil {
		t.Fatalf("Redeem of %d atoms: %v", int64(smallVal), err)
	}
	outbids("small redeem")
	// A batch of six redeems, above 1000 bytes, where the node allows more
	// than the flat 1 000 000: it must pay more, at 600 atoms/B.
	var batch []*asset.Redemption
	for vout := uint32(10); vout < 16; vout++ {
		prevVal[newOutPoint(tTxHash, vout)] = contractVal
		batch = append(batch, &asset.Redemption{Secret: secret[:], Spends: &asset.AuditInfo{
			Coin: newOutput(tTxHash, vout, contractVal, wire.TxTreeRegular), Contract: contract,
			Recipient: addr.String(), Expiration: time.Unix(1_800_000_000, 0)}})
	}
	if _, _, _, err = wallet.Redeem(&asset.RedeemForm{Redemptions: batch,
		Options: map[string]string{redeemFeeBumpFee: "2.0"}}); err != nil {
		t.Fatalf("batch Redeem: %v", err)
	}
	if fee, size := checkSent("batch redeem"); size <= 1000 || fee < 600*int64(size) {
		t.Errorf("batch redeem: fee %d atoms for %d bytes, want at least 600 atoms/B", fee, size)
	}

	// Refund at highRate from its caller.
	p2sh, _ := stdaddr.NewAddressScriptHashV0(contract, tChainParams)
	_, contractScript := p2sh.PaymentScript()
	tipHash, _ := node.getBestBlock()
	node.txOutRes[newOutPoint(tTxHash, 0)] = &chainjson.GetTxOutResult{Confirmations: 1,
		Value: dcrutil.Amount(contractVal).ToCoin(), BestBlock: tipHash.String(),
		ScriptPubKey: chainjson.ScriptPubKeyResult{Hex: hex.EncodeToString(contractScript)}}
	if _, err = wallet.Refund(coin.ID(), contract, highRate); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	outbids("refund")

	// Send at highRate.
	node.unspent = []walletjson.ListUnspentResult{*tUnspent(1, "5")}
	prevVal[newOutPoint(tTxHash, 1)] = 5e8
	if _, err = wallet.Send(tPKHAddr.String(), 1e8, highRate); err != nil {
		t.Fatalf("Send: %v", err)
	}
	checkSent("send")
	node.unspent = []walletjson.ListUnspentResult{*tUnspent(5, "5")}
	prevVal[newOutPoint(tTxHash, 5)] = 5e8
	if _, err = wallet.Withdraw(tPKHAddr.String(), 1e8, highRate); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	checkSent("withdraw")

	// Bond refund at the node's highRate estimate.
	bondKey := dcrsecp256k1.PrivKeyFromBytes(append(make([]byte, 31), 9))
	node.unspent = []walletjson.ListUnspentResult{*tUnspent(2, "5")}
	prevVal[newOutPoint(tTxHash, 2)] = 5e8
	bond, _, err := wallet.MakeBondTx(0, 1e8, highRate, time.Now().Add(12*time.Hour), bondKey, make([]byte, 32))
	if err != nil {
		t.Fatalf("MakeBondTx: %v", err)
	}
	bondTx, err := msgTxFromBytes(bond.SignedTx) // built, not sent
	if err != nil {
		t.Fatal(err)
	}
	node.sentRawTxs = []*wire.MsgTx{bondTx}
	checkSent("bond")
	bondHash, bondVout, _ := decodeCoinID(bond.CoinID)
	prevVal[newOutPoint(bondHash, bondVout)] = 1e8
	if _, err = wallet.RefundBond(tCtx, 0, bond.CoinID, bond.Data, 1e8, bondKey); err != nil {
		t.Fatalf("RefundBond: %v", err)
	}
	outbids("bond refund")

	// Small contracts and bonds at the same rates: 0.06 VAR goes through at
	// the flat cap, its dust checked at the rate the capped fee pays, and
	// 0.02 VAR, which leaves 0.01 VAR after it, is refused as dust.
	refundOf := func(vout uint32, val int64) error {
		node.txOutRes[newOutPoint(tTxHash, vout)] = &chainjson.GetTxOutResult{Confirmations: 1,
			Value: dcrutil.Amount(val).ToCoin(), BestBlock: tipHash.String(),
			ScriptPubKey: chainjson.ScriptPubKeyResult{Hex: hex.EncodeToString(contractScript)}}
		prevVal[newOutPoint(tTxHash, vout)] = val
		_, err := wallet.Refund(toCoinID(tTxHash, vout), contract, highRate)
		return err
	}
	bondRefundOf := func(vout uint32, val int64) error {
		prevVal[newOutPoint(tTxHash, vout)] = val
		_, err := wallet.RefundBond(tCtx, 0, toCoinID(tTxHash, vout), bond.Data, uint64(val), bondKey)
		return err
	}
	if err := refundOf(21, 6e6); err != nil {
		t.Fatalf("Refund of 6000000 atoms: %v", err)
	}
	outbids("small refund")
	if err := bondRefundOf(22, 6e6); err != nil {
		t.Fatalf("RefundBond of 6000000 atoms: %v", err)
	}
	outbids("small bond refund")
	for kind, refuse := range map[string]func() error{
		"redeem": func() error {
			prevVal[newOutPoint(tTxHash, 23)] = 2e6
			_, _, _, err := wallet.Redeem(&asset.RedeemForm{Redemptions: []*asset.Redemption{{Secret: secret[:],
				Spends: &asset.AuditInfo{Coin: newOutput(tTxHash, 23, 2e6, wire.TxTreeRegular), Contract: contract,
					Recipient: addr.String(), Expiration: time.Unix(1_800_000_000, 0)}}}})
			return err
		},
		"refund":      func() error { return refundOf(24, 2e6) },
		"bond refund": func() error { return bondRefundOf(25, 2e6) },
	} {
		if err := refuse(); err == nil || !strings.Contains(err.Error(), "dust") {
			t.Errorf("%s of 2000000 atoms: error %v, want a dust refusal", kind, err)
		}
		node.sentRawTxs = nil
	}

	// A market rate above the swap cap is refused at funding, before a match
	// the clamped swap would underpay.
	node.unspent = []walletjson.ListUnspentResult{*tUnspent(4, "5")}
	node.balanceResult = &walletjson.GetBalanceResult{Balances: []walletjson.GetAccountBalanceResult{tAcctBal("5")}}
	if _, _, _, err = wallet.FundOrder(&asset.Order{Version: version, Value: 1e8, MaxSwapCount: 1e8 / tLotSize,
		MaxFeeRate: dexmon.MaxFeeRate + 1, FeeSuggestion: feeSuggestion}); err == nil {
		t.Error("FundOrder at a MaxFeeRate above 600: no error")
	}
	if _, _, _, err = wallet.FundMultiOrder(&asset.MultiOrder{Values: []*asset.MultiOrderValue{{Value: 1e8, MaxSwapCount: 1e8 / tLotSize}},
		MaxFeeRate: dexmon.MaxFeeRate + 1, FeeSuggestion: feeSuggestion}, 0); err == nil {
		t.Error("FundMultiOrder at a MaxFeeRate above 600: no error")
	}
}

// TestRedeemGeocodeFeeCap is item 3 for RedeemGeocode, which has no change
// output, at 10 000 atoms/B:
//   - one 0.06 VAR game output asks for about 2 190 000 atoms. It must pay the
//     flat 1 000 000 instead, and its dust check must run at the rate that fee
//     pays, so the 0.05 VAR left is kept;
//   - ten of them, an estimate of 1722 bytes, must pay 600 atoms/B, more than
//     the flat 1 000 000;
//   - one of 0.0105 VAR leaves 50 000 atoms after the flat fee, which is dust.
func TestRedeemGeocodeFeeCap(t *testing.T) {
	code := make([]byte, 32)
	k, err := hdkeychain.NewMaster(code, tChainParams)
	if err != nil {
		t.Fatal(err)
	}
	gameAddr, err := stdaddr.NewAddressPubKeyHashEcdsaSecp256k1V0(dcrutil.Hash160(k.SerializedPubKey()), tChainParams)
	if err != nil {
		t.Fatal(err)
	}
	_, gameScript := gameAddr.PaymentScript()
	for _, tc := range []struct {
		outs    int
		val     int64 // of each game output
		wantFee int64 // 0: a dust refusal
	}{{1, 6e6, 1_000_000}, {10, 6e6, 600 * (15 + 10*167 + 37)}, {1, 1.05e6, 0}} {
		t.Run(fmt.Sprintf("%d of %d atoms", tc.outs, tc.val), func(t *testing.T) {
			wallet, node, shutdown := tNewWallet()
			defer shutdown()
			wallet.config().feeRateLimit = 1e6
			node.smartFee = "10000000" // 10 000 atoms/B, in atoms/kB
			node.newAddr = tPKHAddr
			gameTx := wire.NewMsgTx()
			gameTx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(tTxHash, 0, wire.TxTreeRegular), 1e9, nil))
			for range tc.outs {
				gameTx.AddTxOut(wire.NewTxOut(tc.val, gameScript))
			}
			gameHex, err := gameTx.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/address/" + gameAddr.String():
					fmt.Fprintf(w, `{"address_transactions":[{"txid":%q}]}`, gameTx.TxHash())
				case "/tx/hex/" + gameTx.TxHash().String():
					fmt.Fprint(w, hex.EncodeToString(gameHex))
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			if wallet.geocodeAPI != geocodeAPI(dex.Simnet) { // the constructor sets it
				t.Fatalf("geocode API %q, want simnet's %q", wallet.geocodeAPI, geocodeAPI(dex.Simnet))
			}
			wallet.geocodeAPI = srv.URL + "/"

			_, win, err := wallet.RedeemGeocode(code, "")
			if tc.wantFee == 0 {
				if err == nil || !strings.Contains(err.Error(), "dust") {
					t.Fatalf("RedeemGeocode: error %v, want a dust refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RedeemGeocode: %v", err)
			}
			if len(node.sentRawTxs) != 1 {
				t.Fatalf("%d transactions sent, want 1", len(node.sentRawTxs))
			}
			tx := node.sentRawTxs[0]
			fee := int64(tc.outs)*tc.val - tx.TxOut[0].Value
			if fee != tc.wantFee || win != uint64(tx.TxOut[0].Value) {
				t.Errorf("fee %d atoms for %d bytes, win %d, want %d and the output's value %d",
					fee, tx.SerializeSize(), win, tc.wantFee, tx.TxOut[0].Value)
			}
			if size := tx.SerializeSize(); fee > nodeMaxFee(size) {
				t.Errorf("fee %d atoms for %d bytes, above the node's ceiling %d", fee, size, nodeMaxFee(size))
			}
		})
	}
}

// TestLargeSwapRefused is item 5: under SKA load a regular VAR transaction gets
// about 29 500 bytes of a default block, VAR's 37 500-byte share less the votes
// and tickets the node adds first, and one larger than that never confirms. A
// wallet holding only 300 outputs of 0.01 VAR gets orders of 2.4 VAR (242 of
// them, a swap of about 40 500 bytes) and 2.1 VAR (211, about 35 300 bytes).
// The driver must not send a swap or a split larger than the cap. An order of
// 1.7 VAR, 171 inputs and about 28 600 bytes, fits and must be swapped, and so
// must the largest order MaxOrder offers: its estimate selects coins as funding
// does.
func TestLargeSwapRefused(t *testing.T) {
	const maxSize = 29_500
	// A P2PK output, whose input the driver sizes at 133 bytes, not 167.
	p2pkScript, err := txscript.NewScriptBuilder().AddData(secp256k1.PrivKeyFromBytes(append(make([]byte, 31), 2)).PubKey().SerializeCompressed()).
		AddOp(txscript.OP_CHECKSIG).Script()
	if err != nil {
		t.Fatal(err)
	}
	// The cap and a shortfall refuse alike, so pin the size the margin rests on.
	if nfo, err := dexmon.InputInfo(0, p2pkScript, nil, tChainParams); err != nil || nfo.Size() != 133 {
		t.Fatalf("P2PK input info %+v, %v, want 133 bytes", nfo, err)
	}
	// fill gives the wallet nPKH P2PKH outputs, then nPK P2PK ones, 0.01 VAR
	// each, signed at the sizes the driver estimates.
	fill := func(node *tRPCClient, nPKH, nPK uint32) {
		for vout := range nPKH + nPK {
			u := tUnspent(vout, "0.01")
			if vout >= nPKH {
				u.ScriptPubKey = hex.EncodeToString(p2pkScript)
			}
			node.unspent = append(node.unspent, *u)
		}
		node.signFunc = func(tx *wire.MsgTx) (*wire.MsgTx, bool, error) {
			for i, in := range tx.TxIn {
				size := dexmon.P2PKHSigScriptSize
				if in.PreviousOutPoint.Hash == *tTxHash && in.PreviousOutPoint.Index >= nPKH {
					size = dexmon.P2PKSigScriptSize
				}
				tx.TxIn[i].SignatureScript = randBytes(size)
			}
			return tx, true, nil
		}
		node.balanceResult = &walletjson.GetBalanceResult{Balances: []walletjson.GetAccountBalanceResult{tAcctBal("3")}}
	}
	for _, tc := range []struct {
		val        uint64
		split      bool
		mustSwap   bool
		mustRefuse bool
		pkh, pk    uint32 // the wallet's P2PKH and P2PK outputs of 0.01 VAR
	}{{2.4e8, false, false, true, 300, 0}, {2.4e8, true, false, true, 300, 0}, {2.1e8, false, false, true, 300, 0}, {2.1e8, true, false, true, 300, 0},
		{1.7e8, false, true, false, 300, 0}, {1.7e8, true, true, false, 300, 0},
		{0, false, true, false, 300, 0}, // 0: MaxOrder's largest order
		// At the cap's margin: 175 inputs, the most it allows, and 176, refused.
		{1.735e8, false, true, false, 300, 0}, {1.745e8, false, false, true, 300, 0},
		// Orders that take every output, P2PK ones among them, estimated at
		// 29 504, 29 501 and 29 500 bytes: the first two over the cap, the last
		// at it. Each wallet covers its order and fees with under 0.01 VAR to spare.
		{1.805e8, false, false, true, 152, 30}, {1.815e8, false, false, true, 148, 35}, {2.045e8, false, true, false, 58, 148}} {
		t.Run(fmt.Sprintf("%d atoms, split=%v, %d+%d inputs", tc.val, tc.split, tc.pkh, tc.pk), func(t *testing.T) {
			val, split := tc.val, tc.split
			wallet, node, shutdown := tNewWallet()
			defer shutdown()
			node.newAddr, node.changeAddr = tPKHAddr, tPKHAddr
			node.privWIF, _ = dcrutil.NewWIF(append(make([]byte, 31), 1), tChainParams.PrivateKeyID, dcrec.STEcdsaSecp256k1)
			fill(node, tc.pkh, tc.pk)
			if val == 0 {
				est, err := wallet.MaxOrder(&asset.MaxOrderForm{LotSize: tLotSize, FeeSuggestion: feeSuggestion, MaxFeeRate: tDCR.MaxFeeRate})
				if err != nil {
					t.Fatalf("MaxOrder: %v", err)
				}
				val = est.Lots * tLotSize
				t.Logf("MaxOrder: %d lots", est.Lots)
			}
			coins, _, _, err := wallet.FundOrder(&asset.Order{Version: version, Value: val, MaxSwapCount: val / tLotSize,
				MaxFeeRate: tDCR.MaxFeeRate, FeeSuggestion: feeSuggestion,
				Options: map[string]string{"swapsplit": fmt.Sprint(split)}})
			t.Logf("FundOrder: %d coins, error %v", len(coins), err)
			if tc.mustRefuse && err == nil {
				t.Errorf("FundOrder funded %d atoms with %d coins, want a refusal", val, len(coins))
			}
			if err == nil {
				_, _, _, err = wallet.Swap(&asset.Swaps{
					Inputs:    coins,
					Contracts: []*asset.Contract{{Address: tPKHAddr.String(), Value: val, SecretHash: make([]byte, 32), LockTime: 1_800_000_000}},
					FeeRate:   tDCR.MaxFeeRate,
				})
				t.Logf("Swap: error %v", err)
			}
			if tc.mustSwap && (err != nil || len(node.sentRawTxs) == 0) {
				t.Errorf("an order within the cap was not swapped: %v", err)
			}
			for _, tx := range node.sentRawTxs {
				if size := tx.SerializeSize(); size > maxSize {
					t.Errorf("sent a transaction of %d bytes with %d inputs, above %d", size, len(tx.TxIn), maxSize)
				}
			}
		})
	}
	// FundMultiOrder: per-order selection cannot fund more than 175 inputs
	// under the cap, so it falls back to a multi-order split, which is capped
	// as well. At its margin, 1.75 VAR takes 176 inputs and is split; 1.76 VAR
	// takes 177, an estimate of 29 652 bytes, and is refused. Orders that take
	// all of 63 P2PKH and 142 P2PK outputs, and of 153 and 29, are estimated at
	// 29 500 bytes, at the cap, and split, and at 29 501, refused.
	for _, tc := range []struct {
		val      uint64
		mustFund bool
		pkh, pk  uint32
	}{{2.4e8, false, 300, 0}, {1.75e8, true, 300, 0}, {1.76e8, false, 300, 0}, {2.045e8, true, 63, 142}, {1.8125e8, false, 153, 29}} {
		t.Run(fmt.Sprintf("FundMultiOrder split of %d atoms, %d+%d inputs", tc.val, tc.pkh, tc.pk), func(t *testing.T) {
			wallet, node, shutdown := tNewWallet()
			defer shutdown()
			node.newAddr, node.changeAddr = tPKHAddr, tPKHAddr
			fill(node, tc.pkh, tc.pk)
			_, _, _, err := wallet.FundMultiOrder(&asset.MultiOrder{Values: []*asset.MultiOrderValue{{Value: tc.val, MaxSwapCount: tc.val / tLotSize}},
				MaxFeeRate: tDCR.MaxFeeRate, FeeSuggestion: feeSuggestion, Options: map[string]string{multiSplitKey: "true"}}, 0)
			t.Logf("FundMultiOrder: error %v", err)

			if tc.mustFund && (err != nil || len(node.sentRawTxs) == 0) {
				t.Errorf("FundMultiOrder did not split for the order: %v", err)
			}
			if !tc.mustFund && err == nil {
				t.Error("FundMultiOrder funded the order")
			}
			for _, tx := range node.sentRawTxs {
				if size := tx.SerializeSize(); size > maxSize {
					t.Errorf("sent a transaction of %d bytes with %d inputs, above %d", size, len(tx.TxIn), maxSize)
				}
			}
		})
	}
}
