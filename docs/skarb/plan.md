# Skarb stage: plan

VAR/BTC and VAR/LTC trading inside the Skarb wallet, on desktop. This file is the work order
for [`invariants.md`](invariants.md) items 11-15; the shape they assume (desktop only, BTC and
LTC through DCRDEX's native SPV wallets, one backup, a dedicated VAR trading account, DEX login
at start, lock times of 8 and 20 hours) holds here unchanged. SKA trading and mobile trading
are later stages.

Not covered here: Skarb's trading screens (markets, the order form, the BTC and LTC wallet
pages, the choice of bond coin), which get a plan of their own. The steps below build what
items 11-15 need underneath them.

Each step below ends when its named tests pass. As in the PoC plan
([`../var-port/plan.md`](../var-port/plan.md)), each test for an item is written before the code
it guards and is first seen failing for the reason the item names. Each step is one PR or more
into the repository it names.

Versions read, each citation linking to the file at that commit:

- DEX = this repository, `main` at `59769704`
- SKARB = `skarb-wallet` `97cac0ab` (main)
- WAL = `monetarium-wallet` `2c5d237e` (main)

Skarb today has no DEX code: the DCRDEX integration it inherited from Cryptopower was removed,
and stubs stand in for it (SKARB
[`libwallet/phase1_stubs.go:1-6`][skarb:libwallet/phase1_stubs.go:1-6]). Steps 5 and 6 add it
back on this repository's fork.

## Step 1. A wallet release with both of item 11's fixes, `monetarium-wallet`

Item 11's maturity and dust fixes are on wallet main and in no tag. Skarb's vendored copy is
`v1.3.10` plus local changes, among them one that drops duplicate transaction messages from a
peer instead of disconnecting it (SKARB [`go.mod:127-132`][skarb:go.mod:127-132]), and others in
SPV relay, coin selection, the spendable-output list, VSP fee payment and transaction queries.

The port into wallet main, with related wallet fixes, is merged at WAL: wallet PRs #25-#32,
PRs #34-#37, #39, #40 and #47-#52, with #54 (a test fix) and #56 (a vspd client bump), and the
follow-up fixes, PRs #64-#71, #78, #81, #83 and #84. The copy's change for duplicate
transaction messages is dropped, and wallet main still disconnects a peer for an unrequested transaction
(WAL [`p2p/peering.go:1255-1259`][wallet:p2p/peering.go:1255-1259]): the disconnect it worked
around came from the protocol version that wallet PR #24, in `v1.3.14`, moves to 13.
What remains is the release:

- A release candidate, built by hand with no tag: a wallet main commit at or past WAL, taken
  once one more follow-up PR is merged, built against the node commit the next node release
  will be tagged at. It passes the simnet check named in wallet PR #32's live check (a relayed VSP fee
  transaction is held published and resent to a new peer) three runs out of three, and the
  wallet daemon on it runs on testnet.
- The Skarb developer runs a Skarb build on the candidate, step 2's branch built early, on
  testnet. A "received unrequested tx" disconnect in its SPV log reopens the dropped
  duplicate-message change above.
- The tag ships with the next node release, since the release script releases node, wallet and
  ctl together. It is cut on top of the candidate, by a commit that pins the wallet to that node
  release and bumps the version constants, so it has the maturity and dust fixes.

**Done when** the items above are done, the tag's own tests pass, and this repository's pins
(PoC plan, Versions) move to that release with the PoC plan's step 1a per-PR harness job green.

## Step 2. Skarb on the wallet release, `skarb-wallet`

Skarb drops `_vendored/monetarium-wallet` and the `replace` line, takes the step 1 tag and the
node modules it needs (`v1.3.14` or later), and ships a release. Importing this repository later
pulls in node modules `v1.3.14` and the wallet at `42aa5102` or later, and the vendored copy is
`v1.3.10` on node modules `v1.3.6`.

**Tests**, item 11, on simnet (coinbase maturity 16), in `skarb-wallet`:

- **Maturity.** A Skarb wallet that receives a transaction with an expiry must not select its
  output before maturity in a ticket purchase, which uses the wallet's own input selection (a
  regular send already skips it). The pass condition is "coin not selected": the purchase is
  then funded from other coins or refused.
- **Dust.** A Skarb-built transaction (a send, a ticket) must carry no change the node calls
  dust: either no change output, or change of at least the node's threshold. The case is change
  just above the vendored copy's threshold and below the node's.

**Done when** both tests pass and the Skarb release is out.

## Step 3. A VAR wallet type over a host's in-process wallet, `client/asset/mon`

Item 12. A new wallet type in `client/asset/mon` takes an already open `monetarium-wallet`
wallet, its sync and an account number from the host, and runs the native SPV wallet's code
over them: funding, balance, confirmations and signing are `spvWallet`'s, so the Monetarium
rules stay in the driver. The type differs from the native one only where the host owns things:

- it neither creates, opens nor closes the wallet or any account in it, and does not start its
  own sync;
- it funds and receives from the given account only;
- it unlocks and locks only that account, which carries its own passphrase, set by Skarb (step
  5). A Skarb account has none, and the wallet refuses a per-account unlock without one (WAL
  [`wallet/udb/addressmanager.go:1614-1617`][wallet:wallet/udb/addressmanager.go:1614-1617]),
  so the native type's account setup and unlock do not carry over unchanged;
- it is not seeded, and it supports a password change. Core refuses a password change on a
  seeded type (DEX [`client/core/core.go:3949-3951`][dex:client/core/core.go:3949-3951]), and
  the native type is seeded ([`client/asset/mon/mon.go:284`][dex:client/asset/mon/mon.go:284]).
  Item 15's passphrase-change step needs this.

Core opens a wallet only through the driver, from a type and string settings, so the host hands
the open wallet over first: an exported function in `client/asset/mon` that the host calls with
the wallet, its sync and the account before core opens wallets; the new type's `Open` takes
what was handed over and fails if nothing was. Skarb uses this type and supplies no `Wallet` of
its own through `RegisterCustomWallet` (DEX
[`client/asset/mon/wallet.go:38`][dex:client/asset/mon/wallet.go:38]).

**Tests:**

- **Item 12, unit.** The parts of the driver's SPV-wallet tests for item 12's three rules that
  use one account run on the new type as a third seam, over the mock wallet the SPV seam uses
  (the harness tests cover the hand-over): `TestSPVFundingIgnoresSKA`,
  `TestSPVStakerFeeNotSelected`, `TestStakerFeeNotAvailable` and
  `TestSwapConfirmationsSPVCredit`.
- **Item 12, harness.** In `client/asset/mon/simnet_test.go`, the parts of the PoC plan's step 7
  tests that run on the native SPV wallet within one account also run on the new type over an
  in-process wallet the test opens:
  `TestBroadcastEachKind`, `TestDustChangeNode`, `TestChainedSwapsReserve`,
  `TestSPVMinedSKAContract` and `TestSKAWalletFunding`.
- **Item 12, account.** A second account of the same wallet holds VAR: no funding, send or
  withdrawal may spend from it.
- **Password change**, in `client/core` with the new type: the test unlocks the account
  through core, changes the account's passphrase in the in-process wallet and locks it; core's
  `SetWalletPassword` with the new one must then succeed and the old one must fail (for the
  native type it fails as seeded). After locking the account again, core's next unlock must use
  the new one.

Only the tests named above are repeated; the PoC's items 9 and 10 do not depend on the wallet
type.

**Done when** these tests pass, on a fresh harness, twice.

## Step 4. The maker's redeem margin, `client/core`

Item 15's maker case. The maker redeems the taker's contract first, and core stops it only once
the local clock has passed that contract's lock time (DEX
[`client/core/trade.go:1159-1164`][dex:client/core/trade.go:1159-1164],
[`:1644-1650`][dex:client/core/trade.go:1644-1650]). A redeem sent just before then reveals the
secret, and on Bitcoin or Litecoin the taker's refund can replace it while it is unconfirmed.

The change, in the maker branch of `isRedeemable`
([`:1626-1650`][dex:client/core/trade.go:1626-1650]): the maker treats the taker's contract as
expired once its lock time is less than 1/8 of the taker's lock time ahead of the local clock
(1 hour at the 8-hour default).
`counterPartyConfirms`, which computes the expiry, also serves the taker's swap check, which
keeps the check it has. The match is then revoked as for an expired contract, and both
sides refund after their own lock times: on a match it has not yet tried to redeem, the maker
loses time, not coins. The taker's redeem is
unchanged ([`:1683-1684`][dex:client/core/trade.go:1683-1684]): by then the maker holds the
taker's coins, so the taker must redeem whatever the time.

This edits an upstream-owned file; the change applies to every asset, not to VAR alone.

**Tests**, in `client/core`:

- **`TestMakerRedeemMargin`**, at the 8-hour taker lock time. A maker's match whose taker
  contract has its lock time 59 minutes ahead of the clock must not be redeemable and must be
  revoked; at 61 minutes it must be redeemable. A taker's match in `MakerRedeemed` with the
  maker's lock time 1 minute ahead must still be redeemable, and on a fresh match a taker's swap
  against a maker's contract the wallet reports 59 minutes from its lock time must still be
  swappable.

**Done when** this test passes and the existing `client/core` tests still do, with their mock's
default contract lock time moved past the margin.

## Step 5. Trading in Skarb, `skarb-wallet`

Items 13, 14 and 15, the parts that live in Skarb's code. Skarb embeds DEX core from this
repository: its `go.mod` replaces `decred.org/dcrdex` with this repository at a pinned commit
past step 4. It hands its trading wallet to step 3's type through that step's exported
function. BTC and LTC use DCRDEX's
native SPV wallets.

- **Turning trading on.** The user picks one Skarb wallet. Skarb creates a dedicated trading
  account in it, gives that account its own passphrase, equal to the wallet's spending
  passphrase (WAL [`wallet/wallet.go:2400`][wallet:wallet/wallet.go:2400]), and requires a
  startup passphrase. A whole-wallet unlock does not unlock an account with its own passphrase
  ([`wallet/udb/addressmanager.go:1566-1574`][wallet:wallet/udb/addressmanager.go:1566-1574]),
  so for its own sends from the trading account Skarb also unlocks that account. Skarb's lock
  after a send locks every account, the trading account included, and core unlocks it again
  before each swap, redeem and refund
  ([`client/core/wallet.go:151`][dex:client/core/wallet.go:151]). Turning trading on is refused
  while core is initialized; otherwise Skarb calls `InitializeClient` with the seed derived
  below (DEX [`client/core/core.go:4442`][dex:client/core/core.go:4442]). The call is the same
  for a new wallet and a restored one, so a restore into a fresh profile starts DEX init the
  same way.
- **The DEX app seed.** A 15-word DEX mnemonic (DEX
  [`client/mnemonic/seed.go:94`][dex:client/mnemonic/seed.go:94]):
  - its 18 bytes of entropy are the first 18 bytes of HMAC-SHA256, keyed with the ASCII string
    `Skarb DEX app seed v1`, of the trading wallet's seed bytes. Those bytes come from the
    mnemonic Skarb stores encrypted with the spending passphrase for both created and restored
    wallets (SKARB
    [`libwallet/assets/wallet/wallet_shared.go:380`][skarb:libwallet/assets/wallet/wallet_shared.go:380],
    [`:511`][skarb:libwallet/assets/wallet/wallet_shared.go:511]), decoded by Skarb's own
    `DecodeSeedMnemonic` for the wallet's seed type
    ([`libwallet/assets/wallet/wallet_utils.go:227`][skarb:libwallet/assets/wallet/wallet_utils.go:227]):
    12 or 24 words are BIP39, 33 words are the PGP word list. The seed type is read from the
    word count;
  - its birthday is a fixed date, written in step 5 and no later than the first Skarb release
    that offers trading, so the native BTC and LTC wallets rescan from that day after a
    restore. A hex DEX seed would carry no birthday
    ([`client/core/core.go:4504-4519`][dex:client/core/core.go:4504-4519]), and those wallets
    would rescan from DCRDEX's default birthday, 2021-06-02
    ([`client/asset/btc/btc.go:139-140`][dex:client/asset/btc/btc.go:139-140]);
  - a wallet with no stored mnemonic (watch-only, or one whose mnemonic an older version
    deleted after backup), or one restored from a hex seed, whose seed type the stored text
    does not tell, cannot be the trading wallet.
- **DEX login at start.** With trading on, Skarb's startup passphrase is the DEX app password:
  the one prompt at start also calls `Login`, and Skarb opens its wallet pages only after it
  returns without error. Removing the startup passphrase is refused while trading is on;
  changing it calls `ChangeAppPass`
  ([`client/core/core.go:3493`][dex:client/core/core.go:3493]).
- **Spending passphrase.** A change of the trading wallet's spending passphrase also sets the
  trading account's passphrase to the new one and calls `SetWalletPassword` for VAR
  ([`client/core/core.go:3912`][dex:client/core/core.go:3912]), with the startup passphrase
  asked in the same dialog. If either fails, Skarb restores the wallet as it was before the
  change: its spending passphrase, the trading account's passphrase, its stored mnemonic
  encrypted under the old one, and its passphrase type, from copies of both passphrases kept for
  the purpose. Its own rollback on a database error restores nothing: it reuses passphrase bytes
  the first change has already zeroed (SKARB
  [`libwallet/assets/wallet/wallet_shared.go:712`][skarb:libwallet/assets/wallet/wallet_shared.go:712]).
- **A send's coins.** From the build inside Skarb's `Broadcast` of a send from the trading
  account until it is published or fails, Skarb locks its inputs, so a DEX funding call in
  between cannot select them. At that build Skarb also checks the coins the user picked, and
  refuses the send if any is locked.
- **No tickets from the trading account.** Skarb's ticket purchase and its ticket buyer refuse
  the trading account (SKARB
  [`libwallet/assets/dcr/ticket.go:161`][skarb:libwallet/assets/dcr/ticket.go:161],
  [`:638`][skarb:libwallet/assets/dcr/ticket.go:638]).
- **Delete, reset and switch.** Deleting the trading wallet and switching it each reset the DEX
  data, so core is next initialized from the new trading wallet's seed. Deleting, resetting and
  switching are refused while a match is active, a bond is unrefunded or a BTC or LTC balance is
  at or above that chain's dust limit. The refusal names each blocker and its way out: an active
  match, wait for it to finish; bonds, turn renewal off, and the last one can refund once its
  lock time passes on its chain, the date the refusal shows. On mainnet that is up to about 60
  days after the bond was posted: a new bond locks for twice the server's bond expiry (30 days,
  DEX [`dex/asset.go:22`][dex:dex/asset.go:22]) plus buffers
  ([`client/core/bond.go:159-160`][dex:client/core/bond.go:159-160]). Core refunds it at its
  first check after that, while Skarb runs logged in and has reached the server since it
  started. If it has not, the refusal names a second way out: disable the account, which core
  allows with unspent bonds when no order is active
  ([`client/core/account.go:69-78`][dex:client/core/account.go:69-78]); core then refunds its
  bonds after their lock times without the server
  ([`client/core/bond.go:724-743`][dex:client/core/bond.go:724-743]). A BTC or LTC
  balance: send it out. Otherwise they go ahead after a warning that names what a seed restore
  does not bring back, including a BTC or LTC remainder below the dust limit, which only the old
  trading wallet's seed recovers.
- **Bond renewal.** Skarb has a control that turns bond renewal off, setting the target tier
  to 0 ([`client/core/bond.go:1137`][dex:client/core/bond.go:1137]); core then posts no new
  bond, and the ones posted refund after their lock times, on the same conditions.
- **User-facing text:** trade from one install at a time; a lost DEX database loses swaps in
  flight and expired bonds; keep the computer on with Skarb running until swaps complete.

**Tests**, in `skarb-wallet`, those that trade against this repository's VAR, BTC, LTC and
`dcrdex` simnet harnesses:

- **Item 11, order.** The trading wallet receives a transaction with an expiry; an order placed
  within coinbase maturity is not funded from its output: it is funded from other coins or
  refused.
- **Item 14, `TestDEXAppSeedVector`.** For each seed type (12, 24 and 33 words), a fixed
  trading-wallet seed gives a fixed DEX mnemonic, written in the test. A hex-restored wallet is
  refused.
- **Item 13.** An order booked, Skarb restarted: Skarb opens no wallet page until `Login` has
  returned without error. A ticket purchase from the trading account is refused, by hand and by
  the ticket buyer. A DEX `FundOrder` between a send's build and its publish selects none of the
  send's inputs. A send whose picked coin a `FundOrder` locked after the coin list was shown is
  refused.
- **Item 14.** Delete, reset and switch, each tried with an active match, with an unrefunded
  bond and with a BTC or LTC balance at the dust limit: each is refused, naming that blocker and
  its way out (for a bond, its refund date: its lock time). With bond renewal turned off, core
  posts no new bond, and once the last one has refunded the refusal for it lifts. With a bond
  unrefunded and Skarb started while the server is stopped, the refusal names disabling the
  account; once it is
  disabled, the bond refunds after its lock time passes on chain, the server still stopped. If
  it does not, the outcome goes to the client before this step changes. With a balance
  below the dust limit and nothing else: each warns, naming the remainder, then goes ahead. With
  none of them: each warns, then goes ahead, and turning trading on with another wallet then
  initializes core from that wallet's seed. Turning trading on while core is initialized is refused.
  The cases with a bond (the refusal, renewal off, the disabled account's refund) each run
  three times, with a VAR, a BTC and an LTC bond the test posts through core; with a BTC or LTC
  bond the refusal names the bond among its blockers.
- **Trading account sends.** A send from the trading account in Skarb succeeds with the account
  locked. After Skarb's lock following that send has run, a swap, a redeem and a refund from the
  account each succeed.
- **Item 15, passphrases.** Removing the startup passphrase with trading on is refused. After
  changing it, Skarb's next start logs in to the DEX with the new one. After changing the
  trading wallet's spending passphrase and locking the wallet: an order, a login, and a swap,
  redeem and refund each succeed. A spending-passphrase change given a wrong startup passphrase
  leaves the wallet as it was: the old spending and trading-account passphrases, and the stored
  mnemonic opening with the old one.

**Done when** these tests pass.

## Step 6. Skarb stays up while a swap needs it, `skarb-wallet`

Item 15's guards.

- **Quit warning.** Skarb's inherited quit check warns only for a signal, never for a closed
  window, and is stubbed off today (SKARB [`ui/window.go:266-269`][skarb:ui/window.go:266-269]).
  Gio, Skarb's UI toolkit, reports a closed window only after the window is gone, so the close
  cannot be held. With an active match Skarb then keeps core running and opens a new window
  with the warning (which swap, until when); it quits only once the user confirms. A signal
  shows the same warning before anything stops. On macOS, Gio's app menu quits (Cmd+Q) with no
  event to Skarb, so Skarb carries a Gio patch that asks it before the app terminates, vendored
  as it already vendors Gio's `cpu` and `shader` modules.
- **Sync off.** Turning off the trading wallet's sync with an active match shows the same
  warning and waits for confirmation.
- **Idle-sleep inhibitor.** While any match is active, Skarb holds the operating system's
  idle-sleep inhibitor: a power assertion on macOS, a power request (`PowerSetRequest`) on
  Windows, logind's idle inhibitor on Linux. It releases it after the last match ends. Today Skarb has
  none on desktop (SKARB [`device/device_unsupported.go:18`][skarb:device/device_unsupported.go:18]).
  If the call fails at runtime, trading stays on and Skarb shows a persistent warning that it
  cannot keep the computer awake; idle sleep then is one of item 15's accepted risks.
- **Desktop only.** The DEX code builds only without the `android` and `ios` tags, so a mobile
  build has no trading.

**Tests**, in `skarb-wallet`:

- **Quit and sync off.** With an active match, a window close, Cmd+Q on macOS, a signal and sync
  off each show the swap and its deadline and wait for confirmation; core keeps running until
  then. With no active match, each goes ahead at once.
- **Inhibitor.** With an active match the computer stays awake past its idle-sleep timeout,
  and the inhibitor shows in the system's list (`pmset -g
  assertions`, `powercfg /requests` from an elevated prompt, `systemd-inhibit --list`), and it
  is gone after the last match ends. A forced inhibitor failure shows the warning and leaves
  trading on.
- **Mobile.** `GOOS=android` and `GOOS=ios` dependency lists of Skarb's main package contain no
  `decred.org/dcrdex/client/core`.

**Done when** these tests pass on macOS, Windows and Linux.

## Step 7. Restore from the trading wallet's seed, `skarb-wallet`

Item 14's restore case, against simnet harnesses: this repository's VAR, BTC, LTC and `dcrdex`
harnesses. A Skarb profile trades, holds an active bond and BTC and LTC balances, and is then
closed and restored from the trading wallet's seed into a fresh profile.

**Tests:**

- The bond is found and later refunded; the BTC and LTC balances show.
- **A measurement, not a pass or fail:** a bond broadcast but not yet accepted by the server at
  the moment of the restore, and whether core finds it. If it does not, the outcome goes to the
  client before item 14 changes.

The test and the measurement each run three times, a restore each, with a VAR, a BTC and an
LTC bond.

**Done when** the test passes in each run, and the measurement's outcomes are written into item
14.

## Placed, or dropped with a reason

- **A fee bump for a redeem near its deadline: dropped.** Step 4's margin narrows the maker's
  case to a redeem sent before the margin and still unconfirmed when the taker can refund, which
  is one of item 15's accepted risks. A VAR redeem cannot be replaced: the Monetarium node
  refuses a second spend of a contract (invariants item 15). The taker redeems as soon as it
  sees the maker's redeem, so a running Skarb is hours ahead of the maker's 20 hours; Skarb not
  running is item 15's accepted risk.
- **A stale VAR wallet password after a spending-passphrase change:** step 5.
- **Two installs restored from one seed:** an accepted risk, step 5's user-facing text.
- **A bond not yet accepted at restore:** step 7.
- **A DEX funding call between a send's coin listing and its publish** (item 13's second gap):
  step 5, by the lock from the send's build and the check of picked coins at that build.
- **PoC items through Skarb's seam:** step 3. Only step 3's named tests are repeated.

## What is needed from the client

- The bond amounts on the Monetarium DEX server, where the user bonds in VAR, BTC or LTC: the
  proposal is 1/400 of the markets' VAR lot, as on dex.decred.org (0.1 DCR at a 40 DCR lot),
  and in BTC and LTC the same value at the rate on the day the amounts are set.
- The Skarb developer's testnet run of a Skarb build on step 1's release candidate.
- Who cuts step 1's wallet tag, which ships with the next node release, and its version.
- The Skarb maintainer's agreement and review for steps 2, 5, 6 and 7, and Skarb releases after
  step 2 and once steps 5-7 are done.

## Order and dependencies

- **Step 1** first. Step 1's testnet run needs **step 2**'s branch, which the Skarb developer
  builds on step 1's release candidate; step 2's release needs the tag.
- **Steps 3 and 4** live in this repository and need neither, so they can run alongside steps
  1 and 2.
- **Step 5** needs steps 2, 3 and 4. **Steps 6 and 7** need step 5.
- **The Skarb release that offers trading** needs steps 5-7.

[dex:client/asset/mon/mon.go:284]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/asset/mon/mon.go#L284
[dex:client/asset/mon/wallet.go:38]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/asset/mon/wallet.go#L38
[dex:client/core/core.go:3493]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/core.go#L3493
[dex:client/core/core.go:3912]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/core.go#L3912
[dex:client/core/core.go:3949-3951]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/core.go#L3949-L3951
[dex:client/core/core.go:4442]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/core.go#L4442
[dex:client/core/core.go:4504-4519]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/core.go#L4504-L4519
[dex:client/core/trade.go:1644-1650]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/trade.go#L1644-L1650
[dex:client/core/trade.go:1683-1684]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/trade.go#L1683-L1684
[dex:client/mnemonic/seed.go:94]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/mnemonic/seed.go#L94
[skarb:device/device_unsupported.go:18]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/device/device_unsupported.go#L18
[skarb:go.mod:127-132]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/go.mod#L127-L132
[skarb:libwallet/assets/dcr/ticket.go:161]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/libwallet/assets/dcr/ticket.go#L161
[skarb:libwallet/assets/dcr/ticket.go:638]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/libwallet/assets/dcr/ticket.go#L638
[skarb:libwallet/assets/wallet/wallet_shared.go:380]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/libwallet/assets/wallet/wallet_shared.go#L380
[skarb:libwallet/assets/wallet/wallet_shared.go:511]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/libwallet/assets/wallet/wallet_shared.go#L511
[skarb:libwallet/phase1_stubs.go:1-6]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/libwallet/phase1_stubs.go#L1-L6
[skarb:ui/window.go:266-269]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/window.go#L266-L269
[dex:client/core/trade.go:1159-1164]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/trade.go#L1159-L1164
[dex:client/core/trade.go:1626-1650]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/trade.go#L1626-L1650
[skarb:libwallet/assets/wallet/wallet_shared.go:712]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/libwallet/assets/wallet/wallet_shared.go#L712
[skarb:libwallet/assets/wallet/wallet_utils.go:227]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/libwallet/assets/wallet/wallet_utils.go#L227
[dex:client/asset/btc/btc.go:139-140]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/asset/btc/btc.go#L139-L140
[wallet:wallet/wallet.go:2400]: https://github.com/monetarium/monetarium-wallet/blob/2c5d237e7403ff46684b07327a5d2d2f83afc878/wallet/wallet.go#L2400
[dex:client/core/bond.go:159-160]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/bond.go#L159-L160
[dex:client/core/bond.go:1137]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/bond.go#L1137
[dex:dex/asset.go:22]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/dex/asset.go#L22
[dex:client/core/account.go:69-78]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/account.go#L69-L78
[dex:client/core/bond.go:724-743]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/bond.go#L724-L743
[wallet:p2p/peering.go:1255-1259]: https://github.com/monetarium/monetarium-wallet/blob/2c5d237e7403ff46684b07327a5d2d2f83afc878/p2p/peering.go#L1255-L1259
[wallet:wallet/udb/addressmanager.go:1566-1574]: https://github.com/monetarium/monetarium-wallet/blob/2c5d237e7403ff46684b07327a5d2d2f83afc878/wallet/udb/addressmanager.go#L1566-L1574
[wallet:wallet/udb/addressmanager.go:1614-1617]: https://github.com/monetarium/monetarium-wallet/blob/2c5d237e7403ff46684b07327a5d2d2f83afc878/wallet/udb/addressmanager.go#L1614-L1617
[dex:client/core/wallet.go:151]: https://github.com/monetarium/monetarium-dex/blob/59769704d308499eec5ea4840916a5067f923256/client/core/wallet.go#L151
