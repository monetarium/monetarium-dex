# Skarb stage: invariants for trading inside Skarb

VAR/BTC and VAR/LTC trading inside the Skarb wallet, after the proof of concept (PoC). The PoC's
invariants, [`../var-port/invariants.md`](../var-port/invariants.md) items 1-10, still hold.
They were tested on the DEX driver's own RPC and SPV wallets, on the `monetarium-wallet`
library this repository pins. Skarb builds against its own vendored copy of that library (SKARB
[`go.mod:132`][skarb:go.mod:132]), which also signs the DEX's VAR transactions (DEX
[`client/asset/mon/spv.go:753`][dex:client/asset/mon/spv.go:753]), so items 1 and 2 carry over to
Skarb once steps 1-2 of [`plan.md`](plan.md) replace that copy with a wallet release at or past
the commit this repository pins. This file adds what Skarb changes: the VAR wallet is Skarb's
own, joined to the DEX through a seam; the users are real users on mainnet; and the app runs only
while the user keeps it open.

As in the PoC file, an invariant here is a failure that is silent or shows up late. How each
test runs is the job of the Skarb-stage plan, [`plan.md`](plan.md).

## The shape these invariants assume

Decided on 2026-11-23. SKA trading is a separate, later stage.

- **Platforms:** desktop only (macOS, Windows, Linux). Skarb ships no mobile build yet (SKARB
  [`README.md:7-12`][skarb:README.md:7-12]); mobile trading is a later stage, after a Skarb
  mobile release.
- **BTC and LTC:** DCRDEX's built-in native SPV wallets. Their keys come from the DEX app seed
  (DEX [`client/core/core.go:2904-2925`][dex:client/core/core.go:2904-2925]).
- **One backup:** Skarb derives the DEX app seed from the seed of the VAR trading wallet,
  domain-separated, and passes it to `InitializeClient` as the restoration seed (DEX
  [`client/core/core.go:4442`][dex:client/core/core.go:4442]).
- **VAR trading wallet:** one Skarb wallet, fixed for the life of the DEX data, trading from a
  dedicated account that Skarb creates. The trading account buys no tickets: Skarb's ticket
  purchase excludes it. Changing the trading wallet is a reset of the DEX data.
- **VAR seam:** `client/asset/mon` wraps Skarb's in-process `monetarium-wallet` wallet, so the
  Monetarium rules of the driver's SPV wallet apply unchanged. Skarb does not re-implement them.
- **DEX login:** Skarb logs in to the DEX when it starts, with its startup passphrase as the
  DEX app password. Trading requires a startup passphrase, which Skarb otherwise leaves optional
  and core requires (DEX [`client/core/core.go:4525-4526`][dex:client/core/core.go:4525-4526]),
  and a change to it changes the DEX password
  ([`:3493`][dex:client/core/core.go:3493]). So trades resume with the app, and a restart without
  login is not listed among the accepted risks.
- **Lock times:** DCRDEX's defaults, 8 hours for the taker and 20 hours for the maker (DEX
  [`dex/asset.go:17-18`][dex:dex/asset.go:17-18]). They are global and fixed on mainnet, so VAR
  cannot have its own.
- **Accepted risks:** a lost DEX database is not recoverable from the seed for swaps in flight
  and bonds already expired, and there is no database export (item 14); a swap fails, and a
  taker's coins can be lost, when Skarb cannot run a redeem before its deadline because the lid
  is closed, the computer is shut down, restarted (an operating-system update included) or
  loses power, Skarb is forced to quit or crashes, or the network is lost (item 15). A maker
  redeeming a BTC or LTC contract just before the taker's lock time can lose its coins by the
  same causes; the plan narrows that case with a margin, and accepts what remains: a redeem
  sent before the margin and still unconfirmed when the taker can refund. Idle sleep after the
  computer refuses Skarb's sleep inhibitor is accepted too (item 15). Two installs
  restored from one seed and run at once share one DEX account and the same coins (item
  13); Skarb's user-facing text says to trade from one install at a time.

Versions read, each citation linking to the file at that commit:

- DEX = this repository, `main` at `49a91dc7`
- SKARB = `skarb-wallet` `3e8790cf` (main), which pins DCRDEX `v1.0.6` and replaces
  `monetarium-wallet` with a vendored copy, `v1.3.10` plus local changes
- WAL = `monetarium-wallet` `42aa5102` (main) and tag `v1.3.14`
- NODE = `monetarium-node` `e1b5d170` (main)

## Funds at risk

### 11. Skarb's wallet follows node rules for maturity and dust

Moved here from the PoC file; the number is kept.

- **Side:** client, Skarb's embedded wallet.
- **Why it can break:** Skarb's vendored wallet differs from the node in two places that wallet
  main has fixed. No wallet tag has either fix: `v1.3.14` still has both bugs.
  - **Maturity:** it applies coinbase maturity only to coinbase outputs, not to outputs of
    transactions with an expiry (SKARB
    [`_vendored/monetarium-wallet/wallet/wallet.go:4619-4624`][skarb:_vendored/monetarium-wallet/wallet/wallet.go:4619-4624]);
    the node requires it for both (NODE
    [`validate.go:4247-4258`][node:internal/blockchain/validate.go:4247-4258]; WAL
    [`wallet/wallet.go:4599-4607`][wallet:wallet/wallet.go:4599-4607]). The server accepts
    such a coin; the swap is then rejected (`ErrExpiryTxSpentEarly`).
  - **Dust:** its dust rule leaves out the coin-type byte (SKARB
    [`_vendored/monetarium-wallet/wallet/txrules/rules.go:52-64`][skarb:_vendored/monetarium-wallet/wallet/txrules/rules.go:52-64]),
    so transactions it builds itself (tickets, VSP fees) can carry change the node calls dust.
- **Exposed by:** a Skarb wallet that receives a transaction with an expiry and places and
  fills an order within coinbase maturity; a Skarb-built transaction whose change sits just
  above the vendored dust threshold.

### 12. The trading wallet keeps the driver's Monetarium rules through Skarb's seam

- **Side:** client.
- **Why it can break:** the driver's own SPV wallet holds three Monetarium rules:
  - funding lists only VAR outputs (item 6; DEX
    [`client/asset/mon/spv.go:637-640`][dex:client/asset/mon/spv.go:637-640]);
  - funding leaves out staker-fee outputs, and the spendable balance subtracts them, from
    decimal strings (item 8; [`:630-633`][dex:client/asset/mon/spv.go:630-633],
    [`:587-611`][dex:client/asset/mon/spv.go:587-611]);
  - an output's confirmations are counted from the tip, as `TestSwapConfirmationsSPVCredit`
    checks ([`:710-717`][dex:client/asset/mon/spv.go:710-717]).

  The seam also lets a host supply a `Wallet` of its own through `RegisterCustomWallet` (DEX
  [`client/asset/mon/wallet.go:38`][dex:client/asset/mon/wallet.go:38]). The shape puts the
  wrapper in `client/asset/mon` instead: a `Wallet` written in Skarb that lists, balances or
  counts confirmations its own way would drop these rules where no driver test runs. An SKA
  output funding a VAR swap, or a staker-fee output the node later respends, fails only at
  broadcast or after it.
- **Exposed by:** a trading account holding an SKA1 output, a staker-fee output and a VAR
  output, read through the seam Skarb uses: funding takes only the VAR output, and `Available`
  leaves out the staker-fee output; and the driver's SPV-wallet tests for these three rules run
  against that seam, with its harness tests over an in-process wallet.

## Failed swap or rejected order

### 13. A coin funding an order is spent only by that order's swap

- **Side:** client.
- **Why it can break:** the DEX keeps an order's funding coins out of other spends by locking
  them in the wallet (DEX
  [`client/asset/mon/mon.go:2875-2888`][dex:client/asset/mon/mon.go:2875-2888]). Skarb's coin
  list skips locked coins (SKARB
  [`libwallet/assets/dcr/accounts.go:218-221`][skarb:libwallet/assets/dcr/accounts.go:218-221]),
  and that list, or the coins the user picked from it, is the only filter on a Skarb send
  except a Max send with no picked coins: Skarb passes the wallet its own input source (SKARB
  [`libwallet/assets/dcr/txauthor.go:852-856`][skarb:libwallet/assets/dcr/txauthor.go:852-856],
  [`:943`][skarb:libwallet/assets/dcr/txauthor.go:943]), and the wallet skips locked coins only
  when it gets none
  ([`_vendored/monetarium-wallet/wallet/createtx.go:206-211`][skarb:_vendored/monetarium-wallet/wallet/createtx.go:206-211]).
  Two gaps remain:
  - **Before login:** the wallet keeps locks in memory only (WAL
    [`wallet/wallet.go:183`][wallet:wallet/wallet.go:183]). After a restart they are set again
    only when DEX core logs in and resumes its trades (DEX
    [`client/core/core.go:7752`][dex:client/core/core.go:7752]). A send from the trading
    account between Skarb's start and DEX login can spend them.
  - **Between listing a send's coins and its publish:** Skarb lists the coins, the user may pick
    from them, Skarb builds the send without locking its inputs (SKARB
    [`libwallet/assets/dcr/txauthor.go:560`][skarb:libwallet/assets/dcr/txauthor.go:560]) and
    publishes it later ([`:651`][skarb:libwallet/assets/dcr/txauthor.go:651]). Until then those
    coins still list as unspent, so a DEX funding call in between can select the same coin.

  A matched order then fails when its swap is due: the match is revoked and the account's score
  drops. No coins are lost, but a counterparty that already swapped waits for its lock time to
  refund. A booked order whose coins are gone at login is canceled when core connects to the
  server (DEX [`client/core/core.go:7115-7136`][dex:client/core/core.go:7115-7136]).
  Each wallet unlock also retries unpaid VSP fees in the background (SKARB
  [`libwallet/assets/dcr/ticket.go:410-425`][skarb:libwallet/assets/dcr/ticket.go:410-425]),
  which would spend from a ticket's account before DEX login; the trading account buys no
  tickets, so it has none.
- **Accepted risk:** two installs restored from one seed and run at once: the locks of one do
  not exist in the other, so each can spend what the other locked. Skarb's user-facing text
  says to trade from one install at a time.
- **Exposed by:** an order booked, Skarb restarted, and a send from the trading account before
  DEX login; a DEX `FundOrder` between Skarb listing a send's coins and its publish, from the
  same account; a ticket purchase from the trading account, which Skarb refuses.

### 14. Funds the DEX holds are recoverable from the trading wallet's seed, or Skarb keeps them

- **Side:** client.
- **Why it can break:** the native BTC and LTC wallets (DEX
  [`client/core/core.go:2904-2925`][dex:client/core/core.go:2904-2925]) and every bond (DEX
  [`client/core/bond.go:1102-1104`][dex:client/core/bond.go:1102-1104]) take their keys from the
  DEX app seed.
  - **The derivation:** if a later Skarb version derives the DEX app seed differently, a
    restore yields another seed, and the BTC and LTC funds and the bonds do not show up. Nothing
    reports it.
  - **Bonds:** after a restore, core rebuilds the bonds the server reports and finds their
    keys from the app seed. The server reports only active bonds (DEX
    [`client/core/core.go:6705-6733`][dex:client/core/core.go:6705-6733]), so a bond that
    expired before the restore is not recovered.
  - **Swaps in flight** need the DEX database: a fresh instance "lacks any order or match
    history" (DEX [`client/core/core.go:4253-4255`][dex:client/core/core.go:4253-4255]).

  So deleting the trading wallet, resetting the DEX data or switching the trading wallet in-app
  can strand funds that a seed restore does not bring back.
- **Accepted risk:** losing the DEX database outside the app (a lost device, a wiped app
  directory) while a swap is in flight or after a bond has expired. There is no database
  export. Skarb states this limit in its user-facing text, and a reset with no open match, no
  unrefunded bond and no BTC or LTC balance at or above that chain's dust limit goes ahead only
  after a warning.
- **Exposed by:**
  - a fixed test vector: a given trading-wallet seed gives a given DEX app seed in every
    version;
  - a restore from the trading wallet's seed into a fresh profile, holding an active bond and
    BTC and LTC balances: the bond is found and later refunded, and the balances show;
  - deletion, reset and switch attempts while a match is open, while a bond is unrefunded, and
    while a BTC or LTC balance is at or above that chain's dust limit: each is refused.

### 15. Skarb stays up for each redeem before the counterparty's lock time

- **Side:** client.
- **Why it can break:** each side must redeem before the other side's lock time, counted from
  the match: 8 hours for the taker's contract, 20 hours for the maker's (DEX
  [`client/core/trade.go:2333-2339`][dex:client/core/trade.go:2333-2339]).
  - **The taker** redeems second. If Skarb does not run between the maker's redeem and the
    maker's lock time, the maker can redeem the taker's contract and then refund its own.
  - **The maker** redeems first. Core does not redeem once the taker's lock time has passed
    (DEX [`client/core/trade.go:1644-1650`][dex:client/core/trade.go:1644-1650]); both sides
    then refund, each after its own lock time
    ([`:1702-1740`][dex:client/core/trade.go:1702-1740]). A redeem broadcast just before that
    lock time has revealed the secret. If it is still unconfirmed when the chain's median time
    passes the lock time, and the chain's nodes let a later spend replace it, the taker can refund
    its own contract in its place and redeem the maker's with the secret. The Monetarium node
    refuses a second spend of a contract (NODE
    [`internal/mempool/mempool.go:1055-1058`][node:internal/mempool/mempool.go:1055-1058]), so
    this concerns a maker redeeming a BTC or LTC contract.

  Skarb has no background process, and core resumes its trades only at DEX login (DEX
  [`client/core/core.go:4669`][dex:client/core/core.go:4669]). So a swap step runs only while
  Skarb is open, logged in to the DEX and online, on a computer that is awake. DEX core refuses
  to log out while an order is active (DEX
  [`client/core/core.go:4918-4921`][dex:client/core/core.go:4918-4921]), and a shutdown that
  stops core without that check bypasses the refusal. On mobile the operating system stops
  background apps. Core unlocks the trading wallet with the password the DEX stores for it,
  and does not check it while the wallet is unlocked (DEX
  [`client/core/wallet.go:167-169`][dex:client/core/wallet.go:167-169]): a spending passphrase
  changed in Skarb shows only when core next unlocks the locked wallet, to place an order,
  resume trades at login, or run a swap, redeem or refund.
- **What Skarb does:** while any match of this account is active (neither complete nor
  refunded), Skarb warns on quit, naming the swap and its deadline and asking to confirm, gives
  the same warning when the user turns off the trading wallet's sync, which stops its network
  backend (SKARB [`ui/page/root/home_page.go:259-265`][skarb:ui/page/root/home_page.go:259-265],
  [`libwallet/assets/dcr/sync.go:346`][skarb:libwallet/assets/dcr/sync.go:346]), and holds an
  idle-sleep inhibitor on macOS, Windows and Linux. Its user-facing text says to keep
  the computer on with Skarb running until swaps complete. No build without the quit warning and
  the idle-sleep inhibitor offers trading, which leaves mobile out. When the user changes the
  trading wallet's spending passphrase, Skarb updates the password the DEX stores for it (DEX
  [`client/core/core.go:3912`][dex:client/core/core.go:3912]).
- **Accepted risk:** a closed lid, a shutdown or restart (an operating-system update included),
  a power loss, a forced quit, a crash or a network loss that keeps Skarb from running a redeem
  before its deadline; idle sleep after the computer refuses Skarb's sleep inhibitor; and the
  maker's late BTC or LTC redeem above only when it was sent before the margin the plan guards
  it with and is still unconfirmed when the taker can refund.
- **Exposed by:** quitting Skarb, or turning off the trading wallet's sync, with an active
  match: the user is told which swap and until when, and the action waits for confirmation;
  the computer's idle sleep held off while any match is active and released after the last one
  ends; removing the startup passphrase while trading is on is refused, and changing it keeps
  the DEX login at start working; an order, a login and a swap, redeem and refund after the
  trading wallet's spending passphrase changed and Skarb locked the wallet; a mobile build of
  Skarb offers no trading.

## Not invariants

- **Skarb locks the wallet** on its own actions (signing a message, creating an account,
  restarting sync). Before each swap, redeem and refund, core unlocks it again with the password
  the DEX stores for the wallet, which item 15 keeps current (DEX [`client/core/trade.go:2059`][dex:client/core/trade.go:2059],
  [`:2084`][dex:client/core/trade.go:2084], [`:2111`][dex:client/core/trade.go:2111],
  [`:3028`][dex:client/core/trade.go:3028]).
- **VAR lock times under SKA load:** in the PoC's load runs, with SKA filling its share and VAR
  traffic filling VAR's 10%, every DEX VAR transaction, paying at or above the load's rate, was
  mined 1 block after its broadcast
  ([`../var-port/plan.md`](../var-port/plan.md), step 9). Blocks are 5 minutes apart on
  mainnet, against lock times of hours.
- **Private redeem and refund** come with DCRDEX `release-v1.1`; no invariant here needs them.

[dex:client/asset/mon/mon.go:2875-2888]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/asset/mon/mon.go#L2875-L2888
[dex:client/asset/mon/spv.go:587-611]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/asset/mon/spv.go#L587-L611
[dex:client/asset/mon/spv.go:630-633]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/asset/mon/spv.go#L630-L633
[dex:client/asset/mon/spv.go:637-640]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/asset/mon/spv.go#L637-L640
[dex:client/asset/mon/spv.go:710-717]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/asset/mon/spv.go#L710-L717
[dex:client/asset/mon/spv.go:753]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/asset/mon/spv.go#L753
[dex:client/asset/mon/wallet.go:38]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/asset/mon/wallet.go#L38
[dex:client/core/bond.go:1102-1104]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/bond.go#L1102-L1104
[dex:client/core/core.go:2904-2925]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L2904-L2925
[dex:client/core/core.go:4253-4255]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L4253-L4255
[dex:client/core/core.go:3493]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L3493
[dex:client/core/core.go:3912]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L3912
[dex:client/core/core.go:4442]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L4442
[dex:client/core/core.go:4525-4526]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L4525-L4526
[dex:client/core/core.go:4918-4921]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L4918-L4921
[dex:client/core/core.go:6705-6733]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L6705-L6733
[dex:client/core/core.go:7115-7136]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L7115-L7136
[dex:client/core/core.go:4669]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L4669
[dex:client/core/core.go:7752]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/core.go#L7752
[dex:client/core/trade.go:2333-2339]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/trade.go#L2333-L2339
[dex:client/core/wallet.go:167-169]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/wallet.go#L167-L169
[dex:client/core/trade.go:1644-1650]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/trade.go#L1644-L1650
[dex:client/core/trade.go:1702-1740]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/trade.go#L1702-L1740
[dex:client/core/trade.go:2059]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/trade.go#L2059
[dex:client/core/trade.go:2084]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/trade.go#L2084
[dex:client/core/trade.go:2111]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/trade.go#L2111
[dex:client/core/trade.go:3028]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/client/core/trade.go#L3028
[dex:dex/asset.go:17-18]: https://github.com/monetarium/monetarium-dex/blob/49a91dc7b1d64a28bd35f6da020a5dd70fded300/dex/asset.go#L17-L18
[node:internal/mempool/mempool.go:1055-1058]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mempool/mempool.go#L1055-L1058
[node:internal/blockchain/validate.go:4247-4258]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/blockchain/validate.go#L4247-L4258
[skarb:go.mod:132]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/go.mod#L132
[skarb:README.md:7-12]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/README.md#L7-L12
[skarb:_vendored/monetarium-wallet/wallet/txrules/rules.go:52-64]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/_vendored/monetarium-wallet/wallet/txrules/rules.go#L52-L64
[skarb:_vendored/monetarium-wallet/wallet/wallet.go:4619-4624]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/_vendored/monetarium-wallet/wallet/wallet.go#L4619-L4624
[skarb:libwallet/assets/dcr/txauthor.go:560]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/libwallet/assets/dcr/txauthor.go#L560
[skarb:libwallet/assets/dcr/txauthor.go:651]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/libwallet/assets/dcr/txauthor.go#L651
[skarb:_vendored/monetarium-wallet/wallet/createtx.go:206-211]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/_vendored/monetarium-wallet/wallet/createtx.go#L206-L211
[skarb:libwallet/assets/dcr/txauthor.go:852-856]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/libwallet/assets/dcr/txauthor.go#L852-L856
[skarb:libwallet/assets/dcr/txauthor.go:943]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/libwallet/assets/dcr/txauthor.go#L943
[skarb:libwallet/assets/dcr/ticket.go:410-425]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/libwallet/assets/dcr/ticket.go#L410-L425
[skarb:libwallet/assets/dcr/sync.go:346]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/libwallet/assets/dcr/sync.go#L346
[skarb:ui/page/root/home_page.go:259-265]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/ui/page/root/home_page.go#L259-L265
[skarb:libwallet/assets/dcr/accounts.go:218-221]: https://github.com/monetarium/skarb-wallet/blob/3e8790cfdeb84aae38805d2713b73ac6559303d0/libwallet/assets/dcr/accounts.go#L218-L221
[wallet:wallet/wallet.go:183]: https://github.com/monetarium/monetarium-wallet/blob/42aa5102b0a29e9e741cad6532d8fce749f120fa/wallet/wallet.go#L183
[wallet:wallet/wallet.go:4599-4607]: https://github.com/monetarium/monetarium-wallet/blob/42aa5102b0a29e9e741cad6532d8fce749f120fa/wallet/wallet.go#L4599-L4607
