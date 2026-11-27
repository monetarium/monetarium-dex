# Skarb stage: trading screens

The screens over what [`plan.md`](plan.md) steps 3-7 build, desktop only. One server, VARDEX, so
there is no server choice. Out of this plan: charts, Cryptopower's "Centralized Exchange" tab, and
mobile (plan.md step 6 builds no DEX code into it).

Each step below is one screen or one group of screens in `skarb-wallet`, and ends when its named
tests pass and its named check is done. Every step needs step 1 and plan.md step 5, which brings DEX
core into Skarb; a step needs no other unless its **Needs** line or a plan.md step it cites says so.
Tests run on Gio-free functions; a step that moves coins also has a test against this repository's
VAR, BTC, LTC and `dcrdex` simnet harnesses, as plan.md step 5's do. Each new user-facing text is a
key in `en.go` and `uk.go`, the locales Skarb shows
([`ui/values/strings_test.go:10-18`][skarb:ui/values/strings_test.go:10-18]);
**`TestTradingStringsLocalized`** checks that every key the trading screens use is in both.

Versions read, each citation linking to the file at that commit:

- DEX = this repository, `main` at `c5ba192e`
- SKARB = `skarb-wallet` `97cac0ab` (main)
- CP = Cryptopower `8c4ca8b6` (master), whose DEX screens (`ui/page/dcrdex`) Skarb removed; the
  basis for the screens below. Bison's web screens (DEX `client/webserver`) are a second reference.

Amounts stay in atoms from the text the user types to the call into core: VAR, BTC and LTC have 8
decimals, a decimal string is parsed into atoms without `float64`, and a rate is a message rate
rounded down to the market's rate step. CP converts through `float64`
(CP [`ui/page/dcrdex/market.go:1732-1756`][cp:ui/page/dcrdex/market.go:1732-1756]).

## Where the screens sit

Skarb's sidebar has Overview, then the wallet list; a selected wallet expands into its tabs (SKARB
[`ui/page/root/home_page.go:534-614`][skarb:ui/page/root/home_page.go:534-614],
[`ui/page/wallet/single_wallet_main_page.go:85-108`][skarb:ui/page/wallet/single_wallet_main_page.go:85-108]).
Once trading is on and its turn-on progress is done (step 1), the sidebar gets a "Trading" group
between Overview and the wallet list: Markets (the market stats, the order book and the order form,
steps 4-6), Orders (step 7), BTC, LTC (step 3) and Bond (step 2). Before that, and with trading off,
the group is absent.

## Step 1. Turning trading on, the Trading group and the badge

- **Turn-on.** A wallet's Settings tab gets a Trading section with "Trade with this wallet". Its
  dialog shows plan.md step 5's user-facing text (one install at a time, a lost DEX database, after
  a restore, a bond in its last ~30 days or one not yet stored by the server, keeping the computer
  on, the first BTC and LTC sync) before the user confirms, and plan.md step 5's refusals in place
  of the confirm button: a wallet with no stored mnemonic, a watch-only one or one restored from a
  hex seed; core already initialized, which offers "Reset trading data" in the refusal itself; that
  reset logs in first, then runs the checks below. With no startup passphrase set, the dialog says
  one is required and opens app settings' startup passphrase, as plan.md step 5 requires.
- **Progress.** After the confirm, until `Login` has returned, the three wallets are created and
  synced and discovery has returned (plan.md step 7), the body shows the stage: logging in, each
  wallet's sync progress, then restoring the trading account (plan.md step 7's restoring state). The
  Trading group appears once it is done. Trading counts as on once `InitializeClient` has returned,
  and a turn-on cut short after that resumes at the next start from the stage reached.
- **Badge.** A "Trading" badge next to the trading wallet's name in its sidebar entry (the caption
  row, SKARB [`ui/page/root/home_page.go:659`][skarb:ui/page/root/home_page.go:659]) and in its Info
  tab's header. One wallet at most carries it.
- **Reset and switch.** The trading wallet's Trading section has "Reset trading data" (plan.md step
  5's reset); another wallet's has "Trade with this wallet instead" (plan.md step 5's switch).
  Removing the trading wallet uses Skarb's own dialog
  ([`ui/page/wallet/wallet_settings_page.go:581`][skarb:ui/page/wallet/wallet_settings_page.go:581]).
  Each shows plan.md step 5's refusal as one list, a line per blocker with its way out and the
  screen that holds it: an active match, wait for it to finish (Orders); a bond, turn renewal off
  and its refund date and, when Skarb has not reached the server since it started, disable the
  account (Bond); a BTC or LTC balance, send it out (that coin's page). Without a blocker, each
  shows plan.md step 5's warning, then goes ahead on confirm. A confirmed reset turns trading off:
  the Trading group and the badge go. A confirmed switch first checks the new wallet's seed refusals
  (no stored mnemonic, watch-only, hex seed), then resets and opens the new wallet's turn-on dialog.

**Tests:**

- **`TestBlockerLines`.** Given an active match, a bond with a lock time, a bond broadcast and not
  yet confirmed, and a BTC balance for which core's estimate of one send of all of it returns a fee,
  the refusal has one line for each, naming its way out, its screen and, for a bond, its refund
  date. With none, or with only a BTC balance for which that estimate returns its dust error, the
  warning shows instead, naming that remainder.
- **`TestTurnOnStage`.** With `InitializeClient` not yet returned, trading is off; with it returned,
  then logged in, wallets created, wallets synced, and discovery returned, the next start resumes at
  logging in, wallet creation, sync, discovery, and the Trading group respectively.
- `TestTradingStringsLocalized`.

**Check**, by hand on the harnesses: turning trading on with a fresh wallet shows the progress
stages, then the Trading group; the badge is on that wallet only. Quitting during the BTC sync and
starting again resumes at the sync. A reset removes the Trading group and the badge; a switch then
shows the new wallet's turn-on dialog.

## Step 2. Bond

**Needs** step 3 for a BTC or LTC bond.

- **Bond coin.** VAR, BTC or LTC, with the amount the server sets for each (`Exchange.BondAssets`,
  DEX [`client/core/types.go:710`][dex:client/core/types.go:710]; VARDEX's values are in plan.md,
  "Lot and bond") and the balance it needs: twice the amount plus core's fee buffer, since core
  holds back funds for the renewal bond (`BondsFeeBuffer`,
  [`client/core/bond.go:1336-1340`][dex:client/core/bond.go:1336-1340], and
  [`:300-303`][dex:client/core/bond.go:300-303]). A VAR bond comes from the trading account, funded
  from its wallet by an ordinary transaction (plan.md step 5); for a BTC or LTC bond the screen
  names the amount to deposit and links to that coin's Receive.
- **First bond.** `PostBond` with the chosen coin and amount and the startup passphrase
  ([`client/core/bond.go:1369`][dex:client/core/bond.go:1369], the password check at
  [`:1399-1402`][dex:client/core/bond.go:1399-1402]); a first bond keeps its tier by default
  ([`:1424-1427`][dex:client/core/bond.go:1424-1427]), which is renewal on. The screen then shows
  the bond's confirmations up to the count the server requires, as CP's last onboarding step did
  (CP [`ui/page/dcrdex/dex_onboarding_page.go:765`][cp:ui/page/dcrdex/dex_onboarding_page.go:765]).
  Changing the coin later is `UpdateBondOptions` with the new asset
  ([`client/core/bond.go:1137`][dex:client/core/bond.go:1137]): the next renewal posts in it, and
  posted bonds stay until they refund.
- **Status.** Bonded and effective tier; each live bond with its coin, amount, the server's expiry
  and its refund date (its lock time); each expired bond waiting for its refund, with that date.
- **Renewal.** plan.md step 5's control: on is target tier 1, off is 0. Its text for off: no new
  bond is posted, and posted bonds refund after their lock times while Skarb runs logged in and has
  reached the server since it started.
- **Disable and enable account.** "Disable account", plan.md step 5's second way out, sits here and
  asks for the startup passphrase (`ToggleAccountStatus`,
  [`client/core/account.go:51`][dex:client/core/account.go:51]). Once disabled, "Enable account"
  takes its place and also asks for the startup passphrase (`ToggleAccountStatus` with disable
  false).
- **Suspended.** The server takes the account's penalties off its bonded tier
  ([`server/account/account.go:187-189`][dex:server/account/account.go:187-189]) and refuses its
  orders below tier 1 ([`server/market/orderrouter.go:234`][dex:server/market/orderrouter.go:234],
  [`:343`][dex:server/market/orderrouter.go:343]); core refuses them first
  ([`client/core/core.go:5971-5972`][dex:client/core/core.go:5971-5972]). Matches already made go
  on. When the account has penalties and its effective tier is below 1, the Bond screen shows
  "Account suspended": the penalties, the score, the last penalty's broken rule and time (core's
  `Penalized` notification,
  [`client/core/locale_ntfn.go:277-280`][dex:client/core/locale_ntfn.go:277-280]), and the way out,
  an extra bond for each penalty, with its amount and the balance it needs, counted as for the first
  bond. With less available, the control does not submit: core checks the balance only when the tier
  or the bond coin changes ([`client/core/bond.go:1263-1264`][dex:client/core/bond.go:1263-1264]),
  and a failed post is a log line only.
- **Extra bond.** The control sets core's penalty compensation (`UpdateBondOptions` with
  `PenaltyComps`, [`client/core/types.go:212-218`][dex:client/core/types.go:212-218]) to the
  account's current penalties; it does not post a one-off bond (`PostBond`). Why:
  - core's renewal posts enough bonds for the target tier, plus bonds to cover penalties up to
    `PenaltyComps` ([`client/core/bond.go:433-454`][dex:client/core/bond.go:433-454]). With
    `PenaltyComps` at 0, a one-off bond covers the penalty only until the renewal: the one-off bond
    takes the current bond's lock time when that one is far enough out
    ([`:1557-1585`][dex:client/core/bond.go:1557-1585]), and once both lapse core replaces only
    the target tier, so the account is suspended again while its renewed bond stays locked;
  - with `PenaltyComps` set, core posts the extra bond itself and renews it while the server reports
    the penalties. Once the server reports none, it posts no more, and the extra bonds refund at
    their lock times;
  - raising the target tier would also keep the account trading, but keeps the extra bond locked
    after the penalties clear.

  The setting stays: a later penalty, up to the count set, gets a bond posted with no prompt. The
  Bond screen shows it ("cover up to N penalties", N extra bonds at most) and lets the user set it
  back to 0. Core posts no bond at target tier 0
  ([`client/core/bond.go:646-648`][dex:client/core/bond.go:646-648]), so with renewal off the
  control also turns renewal on, and says so. The same call sets the most core may keep bonded
  (`MaxBondedAmt`) to core's own minimum for the tier and the count, 4 bonds for each
  ([`:1239`][dex:client/core/bond.go:1239]): core keeps its cap when only `PenaltyComps` rises (a
  lower one lowers the cap) ([`:1319-1322`][dex:client/core/bond.go:1319-1322]), and at renewal it
  posts no more than the cap allows ([`:670-676`][dex:client/core/bond.go:670-676]).

  **Run on the harnesses** on 2026-11-27, at DEX `c5ba192e`, before this step is built: three
  accounts at tier 1 with VAR bonds, each suspended by one failed match as maker on a `dcrdex`
  server started with `--penaltythreshold=4`. Core refused each one's orders. With `PenaltyComps` at
  0 it still renewed the bond, and the account stayed suspended but for the time between the new
  bond's confirmation and the old one's expiry, when it could trade. A one-off `PostBond` took the
  first bond's lock time and let the account trade until the renewal, after which it was suspended
  again. With `PenaltyComps` at 1, core posted the extra bond, again at the first bond's lock time,
  and at the renewal posted two bonds' worth, and the account traded throughout. The cap stayed at 4
  bonds, which that renewal needed exactly. Not run: BTC and LTC bonds, two penalties, a second
  renewal, a penalty clearing.

**Tests:**

- **`TestBondChoices`.** For fixed bond assets and fee buffers, the three choices show each amount
  and the balance each needs.
- **`TestSuspendedView`.** At a bonded tier of 1 with 1 penalty the suspended view shows, naming 1
  penalty and one bond's amount; with 0 penalties it does not.
- **`TestExtraBondForm`.** The control's form sets `PenaltyComps` to the penalties and
  `MaxBondedAmt` to 4 bonds for each of the tier and the penalties, and sets the target tier to 1
  only when it was 0.
- **`TestExtraBondKeepsTrading`**, against the harnesses, with the `dcrdex` server started with
  `--penaltythreshold=4` so that one failed match as maker (score -4) makes one penalty: an account
  at tier 1, suspended so, has an order placed through core refused; after the extra-bond control,
  an order placed through core succeeds; after the first bond's renewal, it still succeeds. Run
  three times, with a VAR, a BTC and an LTC bond, and once more with a VAR bond and two failed
  matches (two penalties), which the cap above stops without its raise.

**Check**, by hand on the harnesses: a first bond in each coin, its confirmations, then renewal off.
Disabling the account and enabling it again brings the order form back.

## Step 3. The BTC and LTC wallet pages

Over DCRDEX's native SPV wallets in core (plan.md step 5). Each page has:

- **Info:** the balance, split as core reports it: available, immature, locked in orders, in swap
  contracts, in bonds, reserved for bond renewal (`BondReserves`) (DEX
  [`client/core/types.go:106-118`][dex:client/core/types.go:106-118], `AssetBalance`
  [`client/core/core.go:7180`][dex:client/core/core.go:7180]), and the sync state.
- **Receive:** `NewDepositAddress` ([`client/core/core.go:4019`][dex:client/core/core.go:4019]),
  shown as Skarb's own Receive shows an address.
- **Send:** address, amount, an option to take the fee from the amount, the fee estimate
  (`EstimateSendTxFee`, [`:5562`][dex:client/core/core.go:5562]) and the startup passphrase
  (`Send`, [`:5396`][dex:client/core/core.go:5396]). plan.md step 5's refusal sends the user here.
- **Transactions:** `TxHistory` ([`:5846`][dex:client/core/core.go:5846]), newest first, paged.

**Tests:**

- **`TestCoinAmountParse`.** For 8 decimals, decimal text gives atoms exactly ("0.00000001" is 1,
  "21" is 2 100 000 000); a ninth decimal, a sign or an exponent is refused.
- **`TestCoinPageSend`**, against the harnesses, for BTC and for LTC: a deposit to the Receive
  address shows in Info; a send of all of it with the fee taken from the amount leaves no balance,
  and plan.md step 5's refusal for that coin's balance lifts.

## Step 4. Markets

- **Content.** The pairs the server lists (VAR/BTC, VAR/LTC); for the one picked: price, its USD
  value, 24h change, high, low and volume, from core's spot price (`msgjson.Spot`, DEX
  [`dex/msgjson/types.go:1288-1298`][dex:dex/msgjson/types.go:1288-1298]); "—" while the market has
  no trades.
- **24h change.** CP shows high minus low as the change and the fraction as the percent (CP
  [`ui/page/dcrdex/market.go:667-668`][cp:ui/page/dcrdex/market.go:667-668],
  [`:700`][cp:ui/page/dcrdex/market.go:700]), so a 5% rise reads 0.05%. `Change24` is the rise as a
  fraction of the rate 24 hours ago (DEX
  [`dex/candles/candles.go:177`][dex:dex/candles/candles.go:177]). Skarb shows the change
  as `Rate − Rate / (1 + Change24)`, rounded to the nearest atom of the quote coin, and the percent
  as `100 × Change24` to two decimals (Bison shows it to one,
  [`client/webserver/site/src/js/markets.ts:3619`][dex:client/webserver/site/src/js/markets.ts:3619]).
- **Volume.** In VAR, `Vol24`; in the quote coin, `calc.BaseToQuote(Rate, Vol24)`
  ([`dex/calc/convert.go:24`][dex:dex/calc/convert.go:24]), the `Vol24 × Rate / 10⁸` Bison computes
  in floats
  ([`client/webserver/site/src/js/wallets.ts:1581`][dex:client/webserver/site/src/js/wallets.ts:1581]).
  CP divides by the rate instead (CP
  [`ui/page/dcrdex/market.go:671-672`][cp:ui/page/dcrdex/market.go:671-672]), a third fix.
- **USD value.** The price times the quote coin's USD rate from core
  (`FiatConversionRates`, DEX [`client/core/core.go:10443`][dex:client/core/core.go:10443]). Core
  turns its rate sources on at start unless they are stored as off
  ([`:1732-1747`][dex:client/core/core.go:1732-1747]); Skarb sets them with
  `ToggleRateSourceStatus` ([`:10479`][dex:client/core/core.go:10479]) to follow its own setting: on
  only while Skarb's currency conversion is on and privacy mode is off (SKARB
  [`libwallet/assets_config.go:175-182`][skarb:libwallet/assets_config.go:175-182]). Otherwise the
  price shows with no USD value. Known limit: at core's first start, when trading is turned on, core
  fetches the rates once before Skarb can turn its sources off.

**Tests:**

- **`TestSpotStats`.** For a spot at 0.00002100 BTC per VAR with `Change24` 0.05 and a 24h volume
  of 1 000 VAR: the change is +0.00000100 BTC, the percent +5.00%, the quote volume 0.021 BTC. With
  `Change24` −0.2 at the same rate: −0.00000525 BTC and −20.00%. CP's formulas fail each.

## Step 5. The order book

**Needs** step 4.

The 9 best bids and the 9 best asks, as CP showed (CP
[`ui/page/dcrdex/market.go:43-45`][cp:ui/page/dcrdex/market.go:43-45],
[`:1150-1178`][cp:ui/page/dcrdex/market.go:1150-1178]): price and VAR quantity, the asks above the
bids, empty rows where a side has fewer. From core's book feed (`SyncBook`, DEX
[`client/core/bookie.go:535`][dex:client/core/bookie.go:535]) and `BestNOrders`
([`client/orderbook/orderbook.go:480`][dex:client/orderbook/orderbook.go:480]), on the Markets
screen beside the order form.

**Tests:**

- **`TestBookRows`.** 12 asks and 3 bids give the 9 lowest asks, the lowest next to the bids, and
  3 bids best first, then 6 empty rows.

## Step 6. The order form

**Needs** steps 2, 3, 4 and 5, and ships with step 7: without it a booked order has no cancel and
step 1's refusal names a screen that is not there.

- **Kinds.** Buy or sell; limit or market. CP offered both kinds on desktop and market only on the
  phone (CP [`ui/page/dcrdex/market.go:142-157`][cp:ui/page/dcrdex/market.go:142-157]).
- **Quantity** in whole lots, with the lot size ("1 lot = 200 VAR") and the most lots the wallet can
  fund (`MaxBuy` at the limit price, or at the best ask for a market buy; `MaxSell`, DEX
  [`client/core/core.go:5081`][dex:client/core/core.go:5081],
  [`:5143`][dex:client/core/core.go:5143]). The order's quantity is lots × lot size.
- **Limit price**, prefilled with the best price on the other side of the book: the best ask for a
  buy, the best bid for a sell, as for the phone, so an order at it fills now as far as the book
  holds it and books the rest. It is set when the form opens and on every side or market change,
  replacing a typed price; a book update leaves a typed price as it is; empty while that side of the
  book is empty. CP prefilled the mid-gap for a buy and the last trade's rate for a sell (CP
  [`ui/page/dcrdex/market.go:1983-2000`][cp:ui/page/dcrdex/market.go:1983-2000]), which books an
  order that may not fill.
- **Limit text:** a limit order stays in the book only while Skarb runs and is online; once Skarb
  has been offline for the server's broadcast timeout, the server removes it (DEX
  [`server/auth/auth.go:389-392`][dex:server/auth/auth.go:389-392],
  [`server/dex/dex.go:943`][dex:server/dex/dex.go:943]). The text names no figure, since core does
  not export the server's timeout (DCRDEX's default is 12 minutes,
  [`server/cmd/dcrdex/config.go:56`][dex:server/cmd/dcrdex/config.go:56]).
- **Market buy.** Core and the server read a market buy's quantity in the quote coin
  ([`client/core/core.go:6243-6264`][dex:client/core/core.go:6243-6264],
  [`server/market/orderrouter.go:583-589`][dex:server/market/orderrouter.go:583-589]); CP sent the
  lots' VAR quantity for every kind (CP
  [`ui/page/dcrdex/market.go:1744`][cp:ui/page/dcrdex/market.go:1744]). Skarb sends enough of the
  quote coin for the matcher to fill N lots: over the ask levels the N lots take (`BestFill`, DEX
  [`client/orderbook/orderbook.go:698`][dex:client/orderbook/orderbook.go:698]), the sum of each
  level's `rate × base / 10⁸` for the lots taken from it, rounded up to the atom where
  `calc.BaseToQuote` rounds down. The matcher converts what is left back to VAR at each level's rate
  ([`server/matcher/match.go:453-478`][dex:server/matcher/match.go:453-478]), and with a truncated
  sum the last lot falls short. The volume-weighted price (`VWAP`,
  [`client/orderbook/orderbook.go:532`][dex:client/orderbook/orderbook.go:532]) is rounded down, so
  it serves the shown estimate only. If the asks hold fewer than N lots, the form says how many are
  on offer and does not submit. The server refuses a market buy below its market-buy buffer, in lots
  at the mid-gap (1.2 in this repository's harness config,
  [`dex/testing/dcrdex/genmarkets-var.sh:31`][dex:dex/testing/dcrdex/genmarkets-var.sh:31]), so the
  form's smallest market buy is the lot count that meets it on the current book, shown as its
  minimum: 2 lots at 1.2 while the best ask is under 1.2 times the mid-gap.
- **What you give and get**, each in its coin: a limit order's at its price rounded down to the rate
  step; a market order's at the book's prices for N lots, marked as an estimate; with the swap and
  redeem fee estimates (`PreOrder`, [`client/core/core.go:5669`][dex:client/core/core.go:5669]).
- **Confirm.** A dialog repeats what you give and get and the fees (for a market buy, the quantity
  passed to `Trade` is the one the dialog showed, not recomputed from the book), and asks for the
  startup passphrase, passed to `Trade`
  ([`client/core/core.go:5868`][dex:client/core/core.go:5868]). An empty password works only while
  both wallets are unlocked ([`:5954-5958`][dex:client/core/core.go:5954-5958],
  [`:2198-2201`][dex:client/core/core.go:2198-2201]), and Skarb's lock after a send locks the
  trading account (plan.md step 5). CP also asked each time (CP
  [`ui/page/dcrdex/market.go:1679-1716`][cp:ui/page/dcrdex/market.go:1679-1716]). Core's refusal, if
  any, shows in the dialog.
- **In place of the form**, as CP had overlays
  ([`ui/page/dcrdex/market.go:809-848`][cp:ui/page/dcrdex/market.go:809-848]): the server is not
  reached; no bond posted yet, with a link to Bond; a bond waiting for confirmations, with the
  count; suspended (step 2); the market's BTC or LTC wallet still syncing. CP's login and add-wallet
  overlays are not needed: plan.md step 5 logs in at start and creates the wallets at turn-on.

**Tests:**

- **`TestOrderFormAmounts`.** On a market with 200 VAR lots and a rate step of 1, at 3 lots and the
  price typed as "0.00000123" BTC: a limit order's quantity is 60 000 000 000 atoms and its rate the
  typed price rounded down to the rate step, with no `float64` on the way; on a fixed book with asks
  of 1 lot at 0.00000123 and 2 lots at 0.00000124 BTC, a market buy of 3 lots has a quantity of
  0.00074200 BTC, the sum of the two levels; with 2 lots of asks it does not submit; with a buffer
  of 1.2 and a best bid of 0.00000122, the minimum market buy is 2 lots.
- **`TestLimitPrefill`.** A buy gets the best ask, a sell the best bid, an empty side nothing; a
  book update after the user has typed a price leaves it.
- **`TestOrderKinds`**, against the harnesses: a limit buy, a limit sell, a market buy and a market
  sell of 2 lots each, against standing orders a second core places at known rates: each fills 2
  lots, and the market buy's quantity is the quote of those 2 lots. After Skarb's lock following a
  send from the trading account, an order with the passphrase given in the dialog succeeds.

## Step 7. My orders and history, with the swap state

**Needs** step 6, and ships with it.

- **Open orders:** CP's columns (CP
  [`ui/page/dcrdex/market.go:1299-1300`][cp:ui/page/dcrdex/market.go:1299-1300]): kind and side,
  pair, age, price, quantity, filled, settled and status, with a cancel button on a booked order
  (`Cancel`, DEX [`client/core/core.go:6619`][dex:client/core/core.go:6619]). **History:** canceled,
  revoked or executed orders with no active match; an order with an active match stays open (CP's
  lists, [`ui/page/dcrdex/market.go:1906-1941`][cp:ui/page/dcrdex/market.go:1906-1941]). A limit
  order the server removed while Skarb was offline shows there as revoked: core takes the status the
  server reports when it reconnects
  ([`client/core/core.go:1052-1113`][dex:client/core/core.go:1052-1113]).
- **Swap state.** CP showed none beyond the settled percent. Each order opens into its matches (DEX
  [`client/core/types.go:245-261`][dex:client/core/types.go:245-261]): maker or taker, quantity,
  rate, the state in words (from the match status,
  [`dex/order/match.go:76-97`][dex:dex/order/match.go:76-97], with revoked and refunded), the
  transactions so far (swap, the counterparty's swap, redeems, refund), and for an active match two
  times, from the match time and the lock times ([`dex/asset.go:65-80`][dex:dex/asset.go:65-80]):
  the redeem deadline, by which Skarb must redeem the counterparty's contract (for a maker, the
  taker's lock time less plan.md step 4's margin of 1/8 of it, 7 hours after the match on mainnet;
  for a taker, the maker's lock time, 20 hours after), and the refund time of the user's own
  contract (20 hours after the match for a maker, 8 for a taker). Core's `Match` carries no lock
  time, so Skarb computes both.

**Tests:**

- **`TestMatchStateLines`.** Each match status gives its words; a revoked match with a refund coin
  reads refunded; on mainnet a maker's active match matched at T shows a redeem deadline of T + 7
  hours and a refund time of T + 20 hours, a taker's T + 20 and T + 8 hours.
- **`TestOrderLists`.** Booked orders and any order with an active match are open; canceled, revoked
  and executed orders with no active match are history.

**Check**, by hand on the harnesses: a match moves through its states on screen to complete; a
limit order booked, Skarb quit for longer than the harness's 1-minute broadcast timeout, then
restarted, shows in history as revoked.

## Where plan.md's user-facing texts sit

Each text is built in the plan.md step named in the first column; the Screen column says where it
shows.

| plan.md | Text | Screen |
| --- | --- | --- |
| Step 5 | Turn-on refused: no stored mnemonic, watch-only, hex seed; core already initialized, with "Reset trading data" as its way out | Turn-on dialog (step 1) |
| Step 5 | A startup passphrase is required | Turn-on dialog, then app settings' startup passphrase (SKARB [`ui/page/settings/app_settings_page.go:419`][skarb:ui/page/settings/app_settings_page.go:419]) |
| Step 5 | One install at a time, a lost DEX database, after a restore, a bond in its last ~30 days or one not yet stored by the server, keep the computer on, the first BTC and LTC sync | Turn-on dialog, before the confirm |
| Step 5 | The login stage; a failed login or wallet unlock, with its error | Start screen, in place of the wallet pages ([`ui/page/start_page.go:254-259`][skarb:ui/page/start_page.go:254-259]) |
| Step 5 | Removing the startup passphrase is refused while trading is on | App settings' startup passphrase switch |
| Step 5 | A spending passphrase change asks for the startup passphrase; on failure the wallet is as before | The wallet's change spending passphrase dialog ([`ui/page/wallet/wallet_settings_page.go:440`][skarb:ui/page/wallet/wallet_settings_page.go:440]) |
| Step 5 | A send refused because an order locked a picked coin | Send confirm dialog ([`ui/page/send/send_confirm_modal.go:100`][skarb:ui/page/send/send_confirm_modal.go:100]) |
| Step 5 | A send from the trading account whose amount and fee together exceed core's available VAR is refused, naming the available amount; with renewal on, VAR the configured bond asset and no bond reserve reported yet, any send from it is refused, naming that | Send confirm dialog |
| Step 5 | A ticket purchase from the trading account is refused | Manual purchase dialog ([`ui/page/staking/manual_purchase_modal.go:69`][skarb:ui/page/staking/manual_purchase_modal.go:69]); the ticket buyer's account choice ([`ui/page/staking/stake_modal.go:197`][skarb:ui/page/staking/stake_modal.go:197]) |
| Step 5 | Delete, reset and switch: the refusal with each blocker's way out; the warning | Remove wallet dialog; the Trading section's reset and switch (step 1) |
| Step 5 | Bond renewal off and what follows | Bond (step 2) |
| Step 5 | Disable the account, when Skarb has not reached the server | Bond (step 2), named by the refusal |
| Step 6 | Quit with an active match: which swap, until when | plan.md step 6's own warning window, with the redeem deadline step 7 shows; while the user's swap or redeem is due, also that quitting can fail the match and draw a strike |
| Step 6 | Sync off with an active match | The wallet's sync toggle ([`ui/page/root/home_page.go:255`][skarb:ui/page/root/home_page.go:255]), same warning |
| Step 6 | Skarb cannot keep the computer awake | A banner on every screen of the Trading group |
| Step 7 | Restoring the trading account | Turn-on progress (step 1) |

Passphrase prompts: the spending and startup passphrases at turn-on (step 1), the startup passphrase
at start (plan.md step 5); the startup passphrase for an order (step 6), a BTC or LTC send (step 3),
a first bond, disabling and enabling the account (step 2), and a spending passphrase change (plan.md
step 5); the spending passphrase for Skarb's own sends from the trading account, as today. Turning
renewal on or off and the extra bond take none.

## Placed, or dropped with a reason

- **Charts:** out of this release.
- **Server choice:** one server, VARDEX, so CP's server picker and add-server flow
  (CP [`ui/page/dcrdex/market.go:576`][cp:ui/page/dcrdex/market.go:576]) go.
- **CP's "Centralized Exchange" tab and mobile:** out.
- **Immediate-or-cancel** (CP's checkbox, `TifNow`): dropped; a market order fills only now, as far
  as the book holds it, and its rest is not booked, and the two kinds are the agreed scope.
- **A one-off extra bond (`PostBond`):** dropped for penalty compensation, step 2.
- **CP's full order book view:** it was never built (CP
  [`ui/page/dcrdex/market.go:1621-1623`][cp:ui/page/dcrdex/market.go:1621-1623]); dropped.
- **USD rates at core's first start:** a known limit, step 4.

## Order and dependencies

- **Step 1** first: the Trading group holds the rest.
- **Steps 2, 3 and 4** need only step 1; step 2's BTC and LTC bonds need step 3.
- **Step 5** needs step 4; **step 6** needs steps 2, 3, 4 and 5; **step 7** needs step 6, and the
  two ship together.

## Not run

Besides step 2's own list, read from code at the versions above, not run: CP's three markets errors
and its market-buy quantity; an order the server removed while Skarb was offline showing as revoked
after a restart; core's refusal of an order with an empty password once Skarb has locked the trading
account.

[cp:ui/page/dcrdex/dex_onboarding_page.go:765]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/dex_onboarding_page.go#L765
[cp:ui/page/dcrdex/market.go:1150-1178]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L1150-L1178
[cp:ui/page/dcrdex/market.go:1299-1300]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L1299-L1300
[cp:ui/page/dcrdex/market.go:142-157]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L142-L157
[cp:ui/page/dcrdex/market.go:1679-1716]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L1679-L1716
[cp:ui/page/dcrdex/market.go:1732-1756]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L1732-L1756
[cp:ui/page/dcrdex/market.go:1906-1941]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L1906-L1941
[cp:ui/page/dcrdex/market.go:1983-2000]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L1983-L2000
[cp:ui/page/dcrdex/market.go:43-45]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L43-L45
[cp:ui/page/dcrdex/market.go:576]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L576
[cp:ui/page/dcrdex/market.go:1621-1623]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L1621-L1623
[cp:ui/page/dcrdex/market.go:667-668]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L667-L668
[cp:ui/page/dcrdex/market.go:671-672]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L671-L672
[cp:ui/page/dcrdex/market.go:700]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L700
[cp:ui/page/dcrdex/market.go:1744]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L1744
[cp:ui/page/dcrdex/market.go:809-848]: https://github.com/crypto-power/cryptopower/blob/8c4ca8b64d23d77a2503f7a79e72fe39442f87c2/ui/page/dcrdex/market.go#L809-L848
[dex:client/core/account.go:51]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/account.go#L51
[dex:client/core/bond.go:1137]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L1137
[dex:client/core/bond.go:1336-1340]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L1336-L1340
[dex:client/core/bond.go:300-303]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L300-L303
[dex:client/core/bond.go:1369]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L1369
[dex:client/core/bond.go:1399-1402]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L1399-L1402
[dex:client/core/bond.go:1424-1427]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L1424-L1427
[dex:client/core/bond.go:1557-1585]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L1557-L1585
[dex:client/core/bond.go:433-454]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L433-L454
[dex:client/core/bond.go:646-648]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L646-L648
[dex:client/core/bookie.go:535]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bookie.go#L535
[dex:client/core/core.go:1052-1113]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L1052-L1113
[dex:client/core/core.go:10443]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L10443
[dex:client/core/core.go:10479]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L10479
[dex:client/core/core.go:1732-1747]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L1732-L1747
[dex:client/core/core.go:2198-2201]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L2198-L2201
[dex:client/core/core.go:4019]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L4019
[dex:client/core/core.go:5081]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L5081
[dex:client/core/core.go:5143]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L5143
[dex:client/core/core.go:5396]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L5396
[dex:client/core/core.go:5562]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L5562
[dex:client/core/core.go:5669]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L5669
[dex:client/core/core.go:5846]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L5846
[dex:client/core/core.go:5868]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L5868
[dex:client/core/core.go:5954-5958]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L5954-L5958
[dex:client/core/core.go:5971-5972]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L5971-L5972
[dex:client/core/core.go:6243-6264]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L6243-L6264
[dex:client/core/core.go:6619]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L6619
[dex:client/core/core.go:7180]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/core.go#L7180
[dex:client/core/locale_ntfn.go:277-280]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/locale_ntfn.go#L277-L280
[dex:client/core/types.go:106-118]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/types.go#L106-L118
[dex:client/core/types.go:212-218]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/types.go#L212-L218
[dex:client/core/types.go:245-261]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/types.go#L245-L261
[dex:client/core/types.go:710]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/types.go#L710
[dex:client/orderbook/orderbook.go:480]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/orderbook/orderbook.go#L480
[dex:client/orderbook/orderbook.go:532]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/orderbook/orderbook.go#L532
[dex:client/webserver/site/src/js/markets.ts:3619]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/webserver/site/src/js/markets.ts#L3619
[dex:client/webserver/site/src/js/wallets.ts:1581]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/webserver/site/src/js/wallets.ts#L1581
[dex:dex/asset.go:65-80]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/dex/asset.go#L65-L80
[dex:dex/candles/candles.go:177]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/dex/candles/candles.go#L177
[dex:dex/msgjson/types.go:1288-1298]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/dex/msgjson/types.go#L1288-L1298
[dex:dex/order/match.go:76-97]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/dex/order/match.go#L76-L97
[dex:dex/testing/dcrdex/genmarkets-var.sh:31]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/dex/testing/dcrdex/genmarkets-var.sh#L31
[dex:server/account/account.go:187-189]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/server/account/account.go#L187-L189
[dex:server/auth/auth.go:389-392]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/server/auth/auth.go#L389-L392
[dex:server/cmd/dcrdex/config.go:56]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/server/cmd/dcrdex/config.go#L56
[dex:server/dex/dex.go:943]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/server/dex/dex.go#L943
[dex:server/market/orderrouter.go:234]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/server/market/orderrouter.go#L234
[dex:server/market/orderrouter.go:343]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/server/market/orderrouter.go#L343
[dex:server/market/orderrouter.go:583-589]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/server/market/orderrouter.go#L583-L589
[skarb:libwallet/assets_config.go:175-182]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/libwallet/assets_config.go#L175-L182
[skarb:ui/page/root/home_page.go:255]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/root/home_page.go#L255
[skarb:ui/page/root/home_page.go:534-614]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/root/home_page.go#L534-L614
[skarb:ui/page/root/home_page.go:659]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/root/home_page.go#L659
[skarb:ui/page/send/send_confirm_modal.go:100]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/send/send_confirm_modal.go#L100
[skarb:ui/page/settings/app_settings_page.go:419]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/settings/app_settings_page.go#L419
[skarb:ui/page/staking/manual_purchase_modal.go:69]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/staking/manual_purchase_modal.go#L69
[skarb:ui/page/staking/stake_modal.go:197]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/staking/stake_modal.go#L197
[skarb:ui/page/start_page.go:254-259]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/start_page.go#L254-L259
[skarb:ui/page/wallet/single_wallet_main_page.go:85-108]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/wallet/single_wallet_main_page.go#L85-L108
[skarb:ui/page/wallet/wallet_settings_page.go:440]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/wallet/wallet_settings_page.go#L440
[skarb:ui/page/wallet/wallet_settings_page.go:581]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/page/wallet/wallet_settings_page.go#L581
[skarb:ui/values/strings_test.go:10-18]: https://github.com/monetarium/skarb-wallet/blob/97cac0abbb2bfbcc118f0683dbe68685b18c6443/ui/values/strings_test.go#L10-L18
[dex:client/core/bond.go:1239]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L1239
[dex:client/core/bond.go:1319-1322]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L1319-L1322
[dex:client/core/bond.go:670-676]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L670-L676
[dex:dex/calc/convert.go:24]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/dex/calc/convert.go#L24
[dex:client/orderbook/orderbook.go:698]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/orderbook/orderbook.go#L698
[dex:server/matcher/match.go:453-478]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/server/matcher/match.go#L453-L478
[dex:client/core/bond.go:1263-1264]: https://github.com/monetarium/monetarium-dex/blob/c5ba192eea790672e1a3037daa03159f4fac5b4e/client/core/bond.go#L1263-L1264
