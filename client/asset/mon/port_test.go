//go:build !harness && !vspd

// Tests for the values the port takes from monetarium-wallet and the
// Monetarium node rather than from dcrwallet and dcrd: config defaults, the
// JSON-RPC API versions, the registered driver, and the wallet's decimal coin
// strings.

package mon

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/dex"
	dexmon "decred.org/dcrdex/dex/networks/mon"
	dcrwalletjson "decred.org/dcrwallet/v5/rpc/jsonrpc/types"
	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"github.com/monetarium/monetarium-node/cointype"
	"github.com/monetarium/monetarium-node/dcrjson"
	"github.com/monetarium/monetarium-node/dcrutil"
	chainjson "github.com/monetarium/monetarium-node/rpc/jsonrpc/types"
	"github.com/monetarium/monetarium-node/wire"
	walletjson "github.com/monetarium/monetarium-wallet/rpc/jsonrpc/types"
	"github.com/monetarium/monetarium-wallet/wallet"
)

// TestWalletConfigDefaults checks the RPC wallet defaults against
// monetarium-wallet's own: JSON-RPC ports 9510, 19510 and 19957, and its app
// data dir and config file.
func TestWalletConfigDefaults(t *testing.T) {
	walletDir := dcrutil.AppDataDir("monetarium-wallet", false)
	if want := filepath.Join(walletDir, "monetarium-wallet.conf"); defaultConfigPath != want {
		t.Errorf("defaultConfigPath = %q, want %q", defaultConfigPath, want)
	}
	for _, opt := range rpcOpts {
		if opt.Key == "rpclisten" && opt.DefaultValue != "127.0.0.1:9510" {
			t.Errorf("rpclisten option default = %q, want 127.0.0.1:9510", opt.DefaultValue)
		}
	}
	for net, listen := range map[dex.Network]string{
		dex.Mainnet: "localhost:9510",
		dex.Testnet: "localhost:19510",
		dex.Simnet:  "localhost:19957",
	} {
		cfg, _, err := loadRPCConfig(map[string]string{}, net)
		if err != nil {
			t.Fatalf("%s: %v", net, err)
		}
		if cfg.RPCListen != listen {
			t.Errorf("%s: RPCListen = %q, want %q", net, cfg.RPCListen, listen)
		}
		if want := filepath.Join(walletDir, "rpc.cert"); cfg.RPCCert != want {
			t.Errorf("%s: RPCCert = %q, want %q", net, cfg.RPCCert, want)
		}
	}
}

type tVersionConnector struct {
	versions map[string]chainjson.VersionResult
}

func (c *tVersionConnector) Connect(context.Context, bool) error { return nil }
func (c *tVersionConnector) Version(context.Context) (map[string]chainjson.VersionResult, error) {
	return c.versions, nil
}
func (c *tVersionConnector) Disconnected() bool { return false }
func (c *tVersionConnector) Shutdown()          {}
func (c *tVersionConnector) WaitForShutdown()   {}

// TestCheckRPCConnectionVersions checks the version gate against the keys
// monetarium-wallet's version RPC returns: its own API as "monwjsonrpcapi"
// (10.0.0 at the pinned commit) and, in RPC sync mode, the node's as
// "monetariumjsonrpcapi" (8.3.0 at v1.3.14).
func TestCheckRPCConnectionVersions(t *testing.T) {
	ver := func(major, minor uint32) chainjson.VersionResult {
		return chainjson.VersionResult{Major: major, Minor: minor}
	}
	for _, tt := range []struct {
		name     string
		versions map[string]chainjson.VersionResult
		wantErr  string
	}{
		{"monetarium wallet and node", map[string]chainjson.VersionResult{
			"monwjsonrpcapi": ver(10, 0), "monetariumjsonrpcapi": ver(8, 3)}, ""},
		{"newer patch and minor", map[string]chainjson.VersionResult{
			"monwjsonrpcapi": ver(10, 1), "monetariumjsonrpcapi": ver(8, 4)}, ""},
		{"dcrwallet and dcrd", map[string]chainjson.VersionResult{
			"dcrwalletjsonrpcapi": ver(11, 0), "dcrdjsonrpcapi": ver(8, 0)}, "monwjsonrpcapi"},
		{"monetarium wallet, dcrd's node key", map[string]chainjson.VersionResult{
			"monwjsonrpcapi": ver(10, 0), "dcrdjsonrpcapi": ver(8, 3)}, "monetariumjsonrpcapi"},
		{"other wallet major", map[string]chainjson.VersionResult{
			"monwjsonrpcapi": ver(11, 0), "monetariumjsonrpcapi": ver(8, 3)}, "incompatible"},
		{"older node minor", map[string]chainjson.VersionResult{
			"monwjsonrpcapi": ver(10, 0), "monetariumjsonrpcapi": ver(8, 2)}, "incompatible"},
		{"node 7.0, which dcrd's list allowed", map[string]chainjson.VersionResult{
			"monwjsonrpcapi": ver(10, 0), "monetariumjsonrpcapi": ver(7, 0)}, "incompatible"},
		// tRPCClient's walletinfo says the wallet is not in SPV mode.
		{"no node version", map[string]chainjson.VersionResult{
			"monwjsonrpcapi": ver(10, 0)}, "monetariumjsonrpcapi"},
	} {
		_, _, err := checkRPCConnection(tCtx, &tVersionConnector{tt.versions}, newTRPCClient(), tLogger)
		switch {
		case tt.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error: %v", tt.name, err)
		case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
			t.Errorf("%s: error = %v, want one containing %q", tt.name, err, tt.wantErr)
		}
	}
}

// TestRegisteredDriver checks that the driver registers as asset 9508, the
// symbol "var", with VAR's unit info.
func TestRegisteredDriver(t *testing.T) {
	if BipID != 9508 {
		t.Fatalf("BipID = %d, want 9508", BipID)
	}
	if sym := dex.BipIDSymbol(BipID); sym != "var" {
		t.Fatalf("BipIDSymbol(%d) = %q, want var", BipID, sym)
	}
	info, err := asset.Info(BipID)
	if err != nil {
		t.Fatalf("asset.Info(%d): %v", BipID, err)
	}
	if info.Name != "Monetarium" {
		t.Errorf("Name = %q, want Monetarium", info.Name)
	}
	if info.UnitInfo.Conventional.ConversionFactor != dexmon.UnitInfo.Conventional.ConversionFactor ||
		info.UnitInfo.Conventional.Unit != "VAR" {
		t.Errorf("UnitInfo = %+v, want dexmon.UnitInfo", info.UnitInfo)
	}
}

// TestCoinStrToAtoms checks the parse of the wallet's decimal coin strings
// into VAR atoms: exact at 8 decimals, never through a float.
func TestCoinStrToAtoms(t *testing.T) {
	for _, tt := range []struct {
		in      string
		want    uint64
		wantErr bool
	}{
		{"0", 0, false},
		{"1", 1e8, false},
		{"0.00000001", 1, false},
		{"0.1", 1e7, false},
		// float64(0.29) * 1e8 is 28999999.999999996.
		{"0.29", 29e6, false},
		{"20999999.99999999", 21e14 - 1, false},
		{"21000000", 21e14, false},
		{"21000000.00000001", 0, true}, // over VAR's supply cap
		{"0.000000001", 0, true},       // 9 decimals: not a VAR amount
		{"-1", 0, true},
		{"", 0, true},
		{"1e8", 0, true},
		{"1.2.3", 0, true},
		{".-5", 0, true}, // parses as a negative big.Int: refused by the range check
		{" 1", 0, true},
		{"1 ", 0, true},
		{"+1", 0, true},
		{".+5", 0, true},
		{".5", 0, true},
		{"5.", 0, true},
	} {
		got, err := coinStrToAtoms(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("coinStrToAtoms(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("coinStrToAtoms(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
	if cointype.MaxVARAtoms != 21e14 {
		t.Fatalf("MaxVARAtoms = %v, the cases above assume 21e14", cointype.MaxVARAtoms)
	}
	// Round trip with the formatter the SPV wallet's balances use.
	for _, atoms := range []dcrutil.Amount{0, 1, 29e6, 123456789, 21e14} {
		got, err := coinStrToAtoms(varAtomsToDecimalString(atoms))
		if err != nil || got != uint64(atoms) {
			t.Errorf("round trip of %d atoms: got %d, %v", atoms, got, err)
		}
	}
}

// The wallet reports every amount as a decimal coin string. The tests below
// drive each place the port parses one.

func tUnspent(vout uint32, amount string) *walletjson.ListUnspentResult {
	return &walletjson.ListUnspentResult{
		TxID:          tTxID,
		Vout:          vout,
		Address:       tPKHAddr.String(),
		Account:       tAcctName,
		Amount:        amount,
		Confirmations: 1,
		ScriptPubKey:  hex.EncodeToString(tP2PKHScript),
		Spendable:     true,
	}
}

// TestParseUTXOsAtoms checks that parseUTXOs values each output exactly and
// sorts by atoms, not by the amount strings, and that it refuses an amount
// that is not VAR's.
func TestParseUTXOsAtoms(t *testing.T) {
	wallet, _, shutdown := tNewWallet()
	defer shutdown()

	unspents := []*walletjson.ListUnspentResult{
		tUnspent(0, "9"), tUnspent(1, "11"), tUnspent(2, "0.29"), tUnspent(3, "0.00000001"),
	}
	utxos, err := wallet.parseUTXOs(unspents)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{1, 29e6, 9e8, 11e8} // "11" sorts before "9" as a string
	if len(utxos) != len(want) {
		t.Fatalf("got %d utxos, want %d", len(utxos), len(want))
	}
	for i, u := range utxos {
		if u.value != want[i] {
			t.Errorf("utxo %d: value %d, want %d", i, u.value, want[i])
		}
	}

	if _, err := wallet.parseUTXOs(append(unspents, tUnspent(4, "1.000000001"))); err == nil {
		t.Fatal("parseUTXOs accepted an amount with 9 decimals")
	}
}

// TestBalanceAtoms checks Balance's parse of getbalance strings, for a plain
// account and for a mixed one (unmixed and trading accounts set).
func TestBalanceAtoms(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	primary := tAcctBal("1.5")
	primary.ImmatureCoinbaseRewards = "0.25"
	primary.ImmatureStakeGeneration = "0.125"
	primary.LockedByTickets = "2"
	node.balanceResult = &walletjson.GetBalanceResult{Balances: []walletjson.GetAccountBalanceResult{primary}}

	bal, err := wallet.Balance()
	if err != nil {
		t.Fatal(err)
	}
	if bal.Available != 15e7 || bal.Immature != 375e5 || bal.Locked != 2e8 ||
		bal.Other[asset.BalanceCategoryStaked].Amount != 2e8 {
		t.Fatalf("plain account: got %+v", bal)
	}
	for _, field := range []*string{&primary.Spendable, &primary.ImmatureCoinbaseRewards,
		&primary.ImmatureStakeGeneration, &primary.LockedByTickets} {
		good := *field
		*field = "0.000000001"
		node.balanceResult.Balances[0] = primary
		if _, err := wallet.Balance(); err == nil {
			t.Errorf("Balance accepted a 9-decimal amount in %+v", primary)
		}
		*field = good
	}
	node.balanceResult.Balances[0] = primary

	// Mixed account: the trading account's spendable is available, the
	// unmixed account's total is immature.
	rpcw := wallet.wallet.(*rpcWallet)
	rpcw.accountsV.Store(XCWalletAccounts{PrimaryAccount: tAcctName, UnmixedAccount: "unmixed", TradingAccount: "trading"})
	trading, unmixed := tAcctBal("0.5"), tAcctBal("0")
	trading.AccountName, unmixed.AccountName = "trading", "unmixed"
	unmixed.Total = "0.75"
	node.balanceResult.Balances = []walletjson.GetAccountBalanceResult{primary, trading, unmixed}
	bal, err = wallet.Balance()
	if err != nil {
		t.Fatal(err)
	}
	if bal.Available != 2e8 || bal.Immature != 1125e5 || bal.Other[asset.BalanceCategoryUnmixed].Amount != 75e6 {
		t.Fatalf("mixed account: got %+v", bal)
	}
	node.balanceResult.Balances[2].Total = "0.000000001"
	if _, err := wallet.Balance(); err == nil {
		t.Fatal("Balance accepted a 9-decimal unmixed total")
	}
	node.balanceResult.Balances[2].Total = "0.75"
	node.balanceResult.Balances[1].Spendable = "0.000000001"
	if _, err := wallet.Balance(); err == nil {
		t.Fatal("Balance accepted a 9-decimal trading spendable")
	}
}

// TestFundingCoinsAtoms checks the listunspent lookup in FundingCoins: the
// coin carries the output's exact value, and an amount that is not VAR's is
// refused.
func TestFundingCoinsAtoms(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	const vout = 7
	ids := []dex.Bytes{toCoinID(tTxHash, vout)}
	node.unspent = []walletjson.ListUnspentResult{*tUnspent(vout, "1.29")}
	coins, err := wallet.FundingCoins(ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(coins) != 1 || coins[0].Value() != 129e6 {
		t.Fatalf("got coins %v, want one of 129000000 atoms", coins)
	}

	// The supply cap itself is accepted.
	wallet.fundingCoins = make(map[outPoint]*fundingCoin)
	node.unspent = []walletjson.ListUnspentResult{*tUnspent(vout, "21000000")}
	if coins, err := wallet.FundingCoins(ids); err != nil || coins[0].Value() != 21e14 {
		t.Fatalf("FundingCoins at the supply cap: %v, %v", coins, err)
	}

	for _, amt := range []string{"1.000000001", "21000000.00000001", ".-5", " 1", "1 ", ".+5", "+1", ".5", "5.",
		"184467440737.09551617", // 2^64 + 1 atoms, which would wrap to 1 in a uint64
	} {
		wallet.fundingCoins = make(map[outPoint]*fundingCoin)
		node.unspent = []walletjson.ListUnspentResult{*tUnspent(vout, amt)}
		if _, err := wallet.FundingCoins(ids); err == nil {
			t.Fatalf("FundingCoins accepted the amount %q", amt)
		}
	}
}

// TestListSinceBlockFee checks the transaction-history fee parse: an exact VAR
// fee whatever its sign, no fee when the wallet omits it, and no fee (rather
// than a rescaled one or an error) for a fee that is not a VAR amount, which
// the row cannot tell from an SKA fee.
func TestListSinceBlockFee(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	node.blockchain.mainchain[0] = &chainhash.Hash{}
	node.listSinceBlock = &walletjson.ListSinceBlockResult{Transactions: []walletjson.ListTransactionsResult{
		{TxID: "send", Category: "send", Fee: "-0.00012345"},
		{TxID: "positive", Fee: "0.00000029"},
		{TxID: "none"},
		{TxID: "ska", Fee: "-0.000000000000000001"},
	}}
	txs, err := wallet.wallet.(*rpcWallet).ListSinceBlock(tCtx, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]uint64{"send": 12345, "positive": 29}
	for _, tx := range txs {
		w, ok := want[tx.TxID]
		switch {
		case ok && (tx.Fee == nil || toAtoms(*tx.Fee) != w):
			t.Errorf("%s: fee %v, want %d atoms", tx.TxID, tx.Fee, w)
		case !ok && tx.Fee != nil:
			t.Errorf("%s: fee %v, want none", tx.TxID, *tx.Fee)
		}
		if rpcTxFee(&tx) != w {
			t.Errorf("%s: rpcTxFee = %d, want %d", tx.TxID, rpcTxFee(&tx), w)
		}
	}
	if len(txs) != 4 {
		t.Fatalf("got %d transactions, want 4", len(txs))
	}
}

// TestPurchaseTicketsSpendLimit checks that the RPC wallet passes
// purchaseticket's spendlimit, which monetarium-wallet takes before minconf,
// as no limit.
func TestPurchaseTicketsSpendLimit(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	if _, err := wallet.wallet.(*rpcWallet).PurchaseTickets(tCtx, 1, "", "", false); err != nil {
		t.Fatal(err)
	}
	if node.spendLimit != dcrutil.Amount(cointype.MaxVARAtoms) {
		t.Fatalf("spendLimit = %v, want %v", node.spendLimit, dcrutil.Amount(cointype.MaxVARAtoms))
	}
}

// TestSPVSignIncomplete checks that the native wallet refuses a transaction
// the wallet library signed without error but did not complete.
func TestSPVSignIncomplete(t *testing.T) {
	w, dcrw := tNewSpvWallet()
	tx := wire.NewMsgTx()
	if _, err := w.SignRawTransaction(tCtx, tx); err != nil {
		t.Fatalf("complete signing refused: %v", err)
	}
	dcrw.signIncomplete = true
	if _, err := w.SignRawTransaction(tCtx, tx); err == nil {
		t.Fatal("incomplete signing accepted")
	}
}

// TestSPVBalanceStrings checks that the native wallet reports every balance
// field as the wallet's RPC does: a decimal coin string.
func TestSPVBalanceStrings(t *testing.T) {
	w, dcrw := tNewSpvWallet()
	dcrw.acctBal = wallet.Balances{
		ImmatureCoinbaseRewards: 1,
		ImmatureStakeGeneration: 2e8,
		LockedByTickets:         29e6,
		Spendable:               123456789,
		Total:                   21e14,
		Unconfirmed:             10,
		VotingAuthority:         5e7,
	}
	bal, err := w.AccountBalance(tCtx, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ name, got, want string }{
		{"ImmatureCoinbaseRewards", bal.ImmatureCoinbaseRewards, "0.00000001"},
		{"ImmatureStakeGeneration", bal.ImmatureStakeGeneration, "2"},
		{"LockedByTickets", bal.LockedByTickets, "0.29"},
		{"Spendable", bal.Spendable, "1.23456789"},
		{"Total", bal.Total, "21000000"},
		{"Unconfirmed", bal.Unconfirmed, "0.0000001"},
		{"VotingAuthority", bal.VotingAuthority, "0.5"},
	} {
		if f.got != f.want {
			t.Errorf("%s = %q, want %q", f.name, f.got, f.want)
		}
	}
}

// TestFundsMixingStatsAtoms checks the native wallet's mixing stats, which
// parse the mixed and trading accounts' totals.
func TestFundsMixingStatsAtoms(t *testing.T) {
	spvw, dcrw := tNewSpvWallet()
	w := &NativeWallet{
		ExchangeWallet: &ExchangeWallet{wallet: spvw, ctx: tCtx, log: tLogger},
		spvw:           spvw,
	}
	dcrw.acctNums = map[string]uint32{mixedAccountName: 1, tradingAccountName: 2}
	dcrw.acctBals = map[uint32]wallet.Balances{1: {Total: 129e6}, 2: {Total: 29e6}}
	stats, err := w.FundsMixingStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.MixedFunds != 129e6 || stats.TradingFunds != 29e6 {
		t.Fatalf("got mixed %d, trading %d, want 129000000 and 29000000", stats.MixedFunds, stats.TradingFunds)
	}
}

// TestTransferAccountAtoms checks that the native wallet's account transfer
// refuses an output whose amount is not VAR's rather than sending it.
func TestTransferAccountAtoms(t *testing.T) {
	// The outputs come from the SPV wallet; the transfer is built, signed and
	// sent through the RPC test wallet's stubs.
	xw, node, shutdown := tNewWallet()
	defer shutdown()
	spvw, dcrw := tNewSpvWallet()
	w := &NativeWallet{ExchangeWallet: xw, spvw: spvw}
	node.changeAddr = tPKHAddr

	mixed := func(vout uint32, amt string) *walletjson.ListUnspentResult {
		u := tUnspent(vout, amt)
		u.Account = mixedAccountName // the account swept
		return u
	}
	dcrw.unspents = []*walletjson.ListUnspentResult{mixed(0, "1.29"), mixed(1, "0.00000029")}
	if err := w.transferAccount(tCtx, "default", mixedAccountName); err != nil {
		t.Fatal(err)
	}
	tx := node.sentRawTx
	if tx == nil || len(tx.TxIn) != 2 || tx.TxIn[0].ValueIn != 129e6 || tx.TxIn[1].ValueIn != 29 {
		t.Fatalf("sent %v, want inputs of 129000000 and 29 atoms", tx)
	}

	node.sentRawTx = nil
	dcrw.unspents = []*walletjson.ListUnspentResult{mixed(0, "1.000000001")}
	err := w.transferAccount(tCtx, "default", mixedAccountName)
	if err == nil || !strings.Contains(err.Error(), "1.000000001") || node.sentRawTx != nil {
		t.Fatalf("transferAccount error = %v, sent %v; want the amount refused, nothing sent", err, node.sentRawTx)
	}
}

// TestRefundBondKey checks that RefundBond signs with the key core passes in
// dcrd's type: the refund's signature script carries that key's public key.
func TestRefundBondKey(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	privBytes, _ := hex.DecodeString("b07209eec1a8fb6cfe5cb6ace36567406971a75c330db7101fb21bc679bc5330")
	bondKey := dcrsecp256k1.PrivKeyFromBytes(privBytes)
	node.unspent = []walletjson.ListUnspentResult{*tUnspent(0, "1")}
	node.newAddr = tPKHAddr
	node.changeAddr = tPKHAddr
	const amt = 50_000
	bond, _, err := wallet.MakeBondTx(0, amt, 20, time.Now().Add(12*time.Hour), bondKey, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	node.sentRawTx = nil
	if _, err := wallet.RefundBond(tCtx, 0, bond.CoinID, bond.Data, amt, bondKey); err != nil {
		t.Fatal(err)
	}
	if node.sentRawTx == nil || len(node.sentRawTx.TxIn) != 1 {
		t.Fatal("no refund sent")
	}
	if pk := bondKey.PubKey().SerializeCompressed(); !bytes.Contains(node.sentRawTx.TxIn[0].SignatureScript, pk) {
		t.Fatal("refund not signed with the bond key")
	}
}

// TestTicketFeeAtoms checks the RPC wallet's ticket list: gettransaction
// reports a ticket's fee as a negative decimal coin string.
func TestTicketFeeAtoms(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	ticket := wire.NewMsgTx()
	ticket.AddTxOut(wire.NewTxOut(2e8, tP2PKHScript))
	txB, err := ticket.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	node.ticketHashes = []*chainhash.Hash{tTxHash}
	node.walletTxFn = func() (*walletjson.GetTransactionResult, error) {
		return &walletjson.GetTransactionResult{Hex: hex.EncodeToString(txB), Fee: "-0.00012345"}, nil
	}
	tix, err := wallet.wallet.(*rpcWallet).Tickets(tCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tix) != 1 || tix[0].Tx.Fees != 12345 || tix[0].Tx.TicketPrice != 2e8 {
		t.Fatalf("got %+v, want one ticket of price 2e8 and fee 12345", tix)
	}

	// Either sign gives the fee's size; a fee that is not a VAR amount is 0.
	for fee, want := range map[string]uint64{"0.00012345": 12345, "-0.000000006": 0} {
		node.walletTxFn = func() (*walletjson.GetTransactionResult, error) {
			return &walletjson.GetTransactionResult{Hex: hex.EncodeToString(txB), Fee: fee}, nil
		}
		tix, err := wallet.wallet.(*rpcWallet).Tickets(tCtx)
		if err != nil {
			t.Fatal(err)
		}
		if len(tix) != 1 || tix[0].Tx.Fees != want {
			t.Errorf("fee %q: got %+v, want fee %d", fee, tix, want)
		}
	}
}

// TestTreasuryPolicyConversion checks that every treasury policy reaches
// asset.Stances unchanged.
func TestTreasuryPolicyConversion(t *testing.T) {
	in := []*walletjson.TreasuryPolicyResult{{Key: "k1", Policy: "yes"}, {Key: "k2", Policy: "no", Ticket: "t"}}
	out := dcrTreasuryPolicy(in)
	if len(out) != len(in) {
		t.Fatalf("got %d policies, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i].Key != in[i].Key || out[i].Policy != in[i].Policy || out[i].Ticket != in[i].Ticket {
			t.Errorf("policy %d: got %+v, want %+v", i, out[i], in[i])
		}
	}
}

// TestNoDecredText checks the package's user-visible strings (config option
// text, returned errors, log lines) for Decred's names and dcrwallet's ports.
// The literals allowed below are endpoints the port keeps for now: the mixing
// server, the VSP list and the dcrdata lookup, which are outside the PoC.
func TestNoDecredText(t *testing.T) {
	allowed := map[string]bool{
		`"mix.decred.org:5760"`:               true,
		`"mix.decred.org:15760"`:              true,
		`"https://api.decred.org/?c=vsp"`:     true,
		`"error getting tx from dcrdata: %w"`: true,
		`"https://dcrdata.decred.org/api/"`:   true,
		`"https://testnet.dcrdata.org/api/"`:  true,
	}
	bad := regexp.MustCompile(`\b(DCR|dcr|Decred|decred|dcrwallet|Dcrwallet|dcrd|dcrdata|9110|19110|19557)\b`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var checked int
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		imports := make(map[*ast.BasicLit]bool)
		for _, imp := range f.Imports {
			imports[imp.Path] = true
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || imports[lit] {
				return true
			}
			checked++
			if bad.MatchString(lit.Value) && !allowed[lit.Value] {
				t.Errorf("%s: %s", fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
	if checked < 500 {
		t.Fatalf("checked only %d string literals", checked)
	}
}

// TestNodeRPCErrors checks the RPC wallet's matching of the node's JSON-RPC
// errors, which arrive as the node's dcrjson.RPCError type: a missing
// transaction becomes asset.CoinNotFoundError, and an already locked account
// is not an error.
func TestNodeRPCErrors(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()
	rpcw := wallet.wallet.(*rpcWallet)

	// The stub answers gettransaction for an unknown tx with ErrRPCNoTxInfo.
	if _, err := rpcw.GetTransaction(tCtx, &chainhash.Hash{1}); !errors.Is(err, asset.CoinNotFoundError) {
		t.Errorf("GetTransaction error = %v, want asset.CoinNotFoundError", err)
	}

	rpcw.rpcConnector = &tVersionConnector{} // connected
	node.accountUnlocked = &walletjson.AccountUnlockedResult{Encrypted: true}
	node.lockAccountErr = dcrjson.NewRPCError(dcrjson.ErrRPCWalletUnlockNeeded, "account is already locked")
	if err := rpcw.LockAccount(tCtx, tAcctName); err != nil {
		t.Errorf("LockAccount on a locked account: %v", err)
	}
}

// TestCreateSPVWalletDB checks that the native wallet creates its database
// through monetarium-wallet's bdb driver, which the driver package registers.
func TestCreateSPVWalletDB(t *testing.T) {
	dir := t.TempDir()
	seed := bytes.Repeat([]byte{7}, 32)
	if err := createSPVWallet([]byte("pass"), seed, dir, 0, 0, wallet.DefaultGapLimit, chaincfg.SimNetParams()); err != nil {
		t.Fatalf("createSPVWallet: %v", err)
	}
	exists, err := walletExists(filepath.Join(dir, chaincfg.SimNetParams().Name, "spv"))
	if err != nil || !exists {
		t.Fatalf("wallet not created: %v", err)
	}
}

// TestStakeStatusTreasuryKeys checks that StakeStatus passes the wallet's
// treasury policies on in asset.Stances.
func TestStakeStatusTreasuryKeys(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	wallet.connected.Store(true)
	node.rawRes["tspendpolicy"] = mustMarshal(t, []walletjson.TSpendPolicyResult{})
	policies := []walletjson.TreasuryPolicyResult{{Key: "k1", Policy: "yes"}, {Key: "k2", Policy: "no", Ticket: "t"}}
	node.rawRes["treasurypolicy"] = mustMarshal(t, policies)
	status, err := wallet.StakeStatus()
	if err != nil {
		t.Fatal(err)
	}
	keys := status.Stances.TreasuryKeys
	if len(keys) != len(policies) {
		t.Fatalf("TreasuryKeys = %+v, want %+v", keys, policies)
	}
	for i, p := range policies {
		if *keys[i] != dcrwalletjson.TreasuryPolicyResult(p) {
			t.Errorf("TreasuryKeys[%d] = %+v, want %+v", i, *keys[i], p)
		}
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDecredImports lists the Decred and dcrwallet packages the driver may
// import. The rest come from monetarium-node and monetarium-wallet: Decred's
// dcrjson, for one, would not match the node's RPC errors, and dcrwallet's bdb
// driver would leave the native wallet without a database driver.
func TestDecredImports(t *testing.T) {
	allowed := map[string]string{
		"decred.org/dcrwallet/v5/rpc/jsonrpc/types": "asset.Stances carries its TreasuryPolicyResult",
		"github.com/decred/dcrd/dcrec/secp256k1/v4": "core passes the bond key in this type",
		"github.com/decred/slog":                    "the logger monetarium-wallet uses too",
		"github.com/decred/vspd/types/v2":           "vspd's JSON types",
	}
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, imp := range strings.Fields(string(out)) {
		decred := strings.HasPrefix(imp, "github.com/decred/") || strings.HasPrefix(imp, "decred.org/dcrwallet")
		if _, ok := allowed[imp]; decred && !ok {
			t.Errorf("client/asset/mon imports %s", imp)
		}
	}
}

// TestFundMultiOrderCachedValue checks the coin FundMultiOrder caches for a
// funded order: FundingCoins later returns it from the cache at the output's
// exact value.
func TestFundMultiOrderCachedValue(t *testing.T) {
	wallet, node, shutdown := tNewWallet()
	defer shutdown()

	node.unspent = []walletjson.ListUnspentResult{*tUnspent(3, "0.29")}
	node.balanceResult = &walletjson.GetBalanceResult{Balances: []walletjson.GetAccountBalanceResult{tAcctBal("0.29")}}
	coins, _, _, err := wallet.FundMultiOrder(&asset.MultiOrder{
		Values:        []*asset.MultiOrderValue{{Value: 1e6, MaxSwapCount: 1}},
		MaxFeeRate:    80,
		FeeSuggestion: 60,
		Options:       map[string]string{"swapsplit": "false"},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(coins) != 1 || len(coins[0]) != 1 {
		t.Fatalf("funded with %v, want one coin", coins)
	}

	node.unspent = nil // FundingCoins must answer from its cache
	cached, err := wallet.FundingCoins([]dex.Bytes{coins[0][0].ID()})
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 || cached[0].Value() != 29e6 {
		t.Fatalf("cached %v, want one coin of 29000000 atoms", cached)
	}
}
