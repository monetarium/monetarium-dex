// This code is available on the terms of the project LICENSE.md file,
// also available online at https://blueoakcouncil.org/license/1.0.0.

package mon

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"decred.org/dcrdex/client/asset"
	"decred.org/dcrdex/dex"
	"github.com/monetarium/monetarium-node/chaincfg"
	"github.com/monetarium/monetarium-node/chaincfg/chainhash"
	"github.com/monetarium/monetarium-node/txscript/stdaddr"
	"github.com/monetarium/monetarium-wallet/wallet"
)

// WalletTypeHost is the type of a wallet that the host application opens,
// syncs and keeps open itself, and hands over with SetHostWallet.
const WalletTypeHost = "host"

// HostSyncer is the host's network sync of the wallet it hands over: its
// *spv.Syncer, or, for a host that starts a new syncer on each sync start, a
// value that forwards to the current one.
type HostSyncer interface {
	spvSyncer
}

var (
	handOver struct {
		sync.Mutex
		w      *wallet.Wallet
		syncer HostSyncer
		acct   uint32
	}
	hostDef sync.Once
)

// SetHostWallet hands over a wallet the host has opened and keeps open, its
// sync, and the account the DEX funds from and receives to, for core to open
// as a WalletTypeHost wallet. Call it before core opens wallets. The DEX
// unlocks and locks only that account, so it needs a passphrase of its own.
// The DEX never creates, opens or closes the wallet or any account in it, and
// does not start a sync of its own.
func SetHostWallet(w *wallet.Wallet, syncer HostSyncer, account uint32) {
	hostDef.Do(func() {
		WalletInfo.AvailableWallets = append(WalletInfo.AvailableWallets, &asset.WalletDefinition{
			Type:             WalletTypeHost,
			Tab:              "Host",
			Description:      "Use the host application's wallet",
			ConfigOpts:       WalletOpts,
			MultiFundingOpts: multiFundingOpts,
		})
	})
	handOver.Lock()
	defer handOver.Unlock()
	handOver.w, handOver.syncer, handOver.acct = w, syncer, account
}

// openHostWallet wraps what SetHostWallet handed over. Core may open the
// wallet again, so the hand-over stays.
func openHostWallet(chainParams *chaincfg.Params, log dex.Logger) (*hostSPVWallet, error) {
	handOver.Lock()
	defer handOver.Unlock()
	hw := handOver.w
	if hw == nil || handOver.syncer == nil {
		return nil, errors.New("no host wallet and sync handed over")
	}
	if hw.ChainParams().Net != chainParams.Net {
		return nil, fmt.Errorf("host wallet is on %s, want %s", hw.ChainParams().Name, chainParams.Name)
	}
	return &hostSPVWallet{
		spvWallet: &spvWallet{
			dcrWallet:   &extendedWallet{hw},
			spv:         handOver.syncer,
			chainParams: chainParams,
			log:         log.SubLogger("HOST"),
			blockCache:  blockCache{blocks: make(map[chainhash.Hash]*cachedBlock)},
			tipChan:     make(chan *block, 16),
		},
		w:    hw,
		acct: handOver.acct,
	}, nil
}

// hostSPVWallet is the native SPV wallet's code over a wallet, sync and
// account that the host owns. Funding, balance, confirmations and signing are
// spvWallet's.
type hostSPVWallet struct {
	*spvWallet
	w    *wallet.Wallet
	acct uint32
}

var _ Wallet = (*hostSPVWallet)(nil)

// Connect starts the wallet's notifications and block cache, not the wallet
// or a sync.
func (w *hostSPVWallet) Connect(ctx context.Context) error {
	// shortcut: the name is read once per connect, so a rename by the host
	// fails the DEX's account calls until core reconnects the wallet.
	name, err := w.w.AccountName(ctx, w.acct)
	if err != nil {
		return fmt.Errorf("host account %d: %w", w.acct, err)
	}
	w.accts.Store(XCWalletAccounts{PrimaryAccount: name})
	ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(2)
	go func() {
		defer w.wg.Done()
		w.notesLoop(ctx, w.w)
	}()
	go func() {
		defer w.wg.Done()
		w.blockCacheLoop(ctx)
	}()
	return nil
}

// ListSinceBlock lists the transactions that spend from or pay to the account.
func (w *hostSPVWallet) ListSinceBlock(ctx context.Context, start int32) ([]ListTransactionsResult, error) {
	return w.listSinceBlock(ctx, start, &w.acct)
}

// WalletOwnsAddress is whether the account owns addr: the host's other
// accounts are not the DEX's.
func (w *hostSPVWallet) WalletOwnsAddress(ctx context.Context, addr stdaddr.Address) (bool, error) {
	return w.AccountOwnsAddress(ctx, addr, w.Accounts().PrimaryAccount)
}

// Reconfigure restarts the wallet only for a change of type.
func (w *hostSPVWallet) Reconfigure(ctx context.Context, cfg *asset.WalletConfig, net dex.Network, currentAddress string) (bool, error) {
	return cfg.Type != WalletTypeHost, nil
}

// SetVotingPreferences is refused: the choices are the host wallet's, for
// every ticket it holds.
func (w *hostSPVWallet) SetVotingPreferences(context.Context, map[string]string, map[string]string, map[string]string) error {
	return errors.New("voting preferences are set by the host's wallet")
}
