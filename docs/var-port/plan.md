# VAR port: proof-of-concept plan

The proof of concept (PoC) runs VAR/BTC and VAR/LTC swaps on simnet, including failed swaps that
end in refunds, driven by DCRDEX's `client/cmd/simnet-trade-tests`. Everything built here is kept
for VARDEX and Skarb:

- a Monetarium simnet harness, `dex/testing/var/`;
- SLIP-0044 id 9508 in `dex/bip-id.go`;
- `dex/networks/mon`, a port of `dex/networks/dcr`;
- a VAR server asset, `server/asset/mon`, a port of `server/asset/dcr`;
- a VAR client driver, `client/asset/mon`, a port of `client/asset/dcr`.

The module path and the existing package names stay DCRDEX's (`decred.org/dcrdex`), so upstream
merges stay clean.

**Naming.** The new Go packages, and the directories that hold them, are named `mon`. `var`
is a Go keyword, so it cannot be a package name, and DCRDEX names each directory after its
package. This follows Monetarium's own renaming of Decred's daemon names: the coin `DCR` becomes
`VAR`, while `dcrd` and `dcrwallet` become `mond` and `monwallet`. These two are Go package
names of the node and wallet RPC clients, taken from `monetarium-wallet`; they are not the
wallet's log tag (`MONW`) or its binary name (`monetarium-wallet`). It also
follows DCRDEX's convention for chains with tokens: `client/asset/eth` holds ETH and its
tokens, so SKA coins, if they are added later, sit in `mon` next to VAR. The symbol, the market
names (`var_btc`) and the harness (`dex/testing/var/`, `~/dextest/var`) stay `var`.

Each step below ends when its named tests pass. Each test for an item of
[`invariants.md`](invariants.md) is written before the code it guards and is first seen failing
for the reason the item names. Harness tests that confirm a bound in practice
(`TestChainedSwapsReserve`, `TestFeeCeilingNode`) are exempt, since simnet cannot force the
failing case. Each step is one PR or more into `main`.

Out of scope: Bison, VARDEX branding, SKA asset ids, staking through DCRDEX, Skarb's screens.

## Terms used below

Two layers are easy to confuse, so every test names both:

- **DEX wallet type.**
  - *RPC wallet*: the driver talks to an external `monetarium-wallet` over JSON-RPC (DCRDEX's
    `dcrwalletRPC` type, the trade tests' `core` type).
  - *Native SPV wallet*: the driver embeds the wallet library and syncs over P2P (the trade
    tests' `spv` type).
- **External wallet sync.** An RPC wallet's own daemon is either *RPC-synced* to a node
  (`--rpcconnect`) or *SPV-synced* (`--spv`).

"RPC mode" and "SPV mode" in `invariants.md` mean the DEX wallet type. All external wallets
in the harness are RPC-synced, except where a test says otherwise.

**Network.** Unit tests use simnet parameters unless the test names mainnet; the reused
client and server seams run on mainnet parameters today and switch to simnet in the port.
Harness tests run on simnet.

**Versions.**

- **Node modules and the node binary:** `monetarium-node` `v1.3.14`.
- **`monetarium-ctl`:** `v1.3.14`.
- **Wallet:** `monetarium-wallet` main at a pinned commit, `42aa5102` or later, for both the
  harness wallet binary and the library the native SPV wallet embeds. `v1.3.14` has neither the
  maturity fix nor the dust fix that `invariants.md` item 11 describes, so the PoC must not
  embed it. When a wallet release contains both, the pin moves to that release.

## Step 1. Simnet harness, `dex/testing/var/`

The harness comes first: it is the biggest unknown, and steps 7-10 cannot be tested without
it. It follows `dex/testing/dcr/harness.sh` in layout, so `simnet_trade.go` and the
`dcrdex` harness find it the same way. No chain archive is shipped: Monetarium simnet has no
premine and its own chain, so the harness mines from genesis.

**What it sets up.**

- **Environment:**
  - tmux session `var-harness`;
  - root `~/dextest/var`, wiped on start;
  - credentials `user`/`pass`, wallet passphrase `abc`;
  - ports in the 196xx range, unused by the other harnesses;
  - binaries from `VAR_BIN`, required: no fallback to `PATH`, where an installed release
    wallet would pass for the pinned one.
- **Nodes:** `alpha` (`--txindex` for the server, `--miningaddr`) and `beta` (connected to
  alpha). Both run with `--rejectnonstd`. Simnet accepts non-standard transactions by default,
  which skips the node's dust, 100 000-byte size and non-standard-input checks. Items 4 and 5
  depend on exactly those checks, so without the flag their tests could not fail.
- **Wallets**, all RPC-synced to alpha unless noted:
  - `alpha` holds the harness seed (mining address and the SKA1 emission address). It votes
    and buys 5 tickets per block from height 34 while it holds fewer than the target pool,
    320 tickets. The wallet's ticket buyer stays off: it grows the pool far past the target,
    and the ticket price with it, until most VAR is locked in tickets.
  - `trading1` and `trading2` are the trade tests' RPC wallets.
  - `spvwallet` is SPV-synced to alpha and beta. It is used by the item 6 test, which checks
    the external wallet's own SPV publish path.
- **Files and ctl scripts:** `<name>/<name>.conf` per wallet, the file `simnet_trade.go`
  reads. In `harness-ctl/`: `./alpha`, `./alpha_node`, `./beta_node`, `./trading1`,
  `./trading2`, `./spvwallet`, `./fund`, `./quit`, and `./mine-alpha N`. Past height 143,
  `./mine-alpha N` waits before each block until all 5 votes are in the mempool. Simnet needs
  3 of 5, but alpha owns every ticket, and votes left over from a block mined at 3 would count
  toward the next one.
- **Chain build:**
  1. Mine to 34, buy tickets, and mine to 151 with votes.
  2. Emit SKA1 inside its window (heights 150-250): `importemissionkey` with the simnet
     emission key, then `createauthorizedemission`, then `sendrawtransaction` on alpha.
     Unlock the alpha wallet again afterwards, because the emission calls lock it and that
     stops voting.
  3. Mine past the emission's 16-block maturity.
  4. Keep mining until alpha can fund the budget below. Simnet VAR halves every 128 blocks
     and totals under 16 384, so the budget is checked, not assumed.
  5. Distribute VAR and SKA1, then mine 2 blocks.
- **Budget per trading wallet:**
  - 500 VAR in the DCR harness's mix of amounts (10, 18, 5, 7, 1, 15, 3, 25, …);
  - 300 outputs of 0.01 VAR, for item 5's orders;
  - SKA1 outputs larger than the largest VAR output, for items 1 and 6: four of 1000 SKA1,
    larger in coins, for a driver that reads an SKA1 amount as VAR, and one of 5 SKA1, for
    one that parses `listunspent`'s amount at SKA1's 1e18 atoms per coin: 5 SKA1 reads as
    5e18 atoms, larger than any VAR output, while 1000 SKA1 (1e21 atoms) does not fit in
    64 bits.

  `spvwallet` gets the same VAR mix and SKA1 outputs.
- **Optional with `VSP=1`, used only by step 10:** `monetarium-vsp` (`vspm`) on simnet, a
  voting wallet behind it (`--enablevoting --manualtickets`), and a ticket-buying wallet that
  uses it. It lands with step 10.

**Done when** `dex/testing/var/check.sh` passes twice in a row from a clean `~/dextest/var`.
It asserts:

- the height reached, and 20 further blocks mined, each with all 5 votes;
- alpha's node finds a mined transaction by txid (`--txindex`), alpha's wallet has the
  `server_fees` account, and beta is on alpha's tip;
- the VAR and SKA1 balances of each trading wallet and `spvwallet` match the budget
  (`getbalance … <cointype>`), with 300 outputs of 0.01 VAR in each trading wallet, every
  SKA1 output above the largest VAR output in atoms, at least one above it in coins, and at
  least one below 2^63 atoms;
- `spvwallet` is in SPV mode with both nodes as peers;
- `./fund` gets its transaction mined;
- **negative controls**, each sent to alpha and each required to fail with the node's error:
  - a transaction with a dust output;
  - a fee 1 atom below the minimum relay fee, while the minimum itself is accepted;
  - a fee above the ceiling with high fees not allowed;
  - a transaction over 100 000 bytes.

If a later trade test fails only because of `--rejectnonstd`, that is a mainnet finding,
not a reason to drop the flag.

Settled by the first harness runs:

- **`monetarium-wallet --create` answers:** the DCR harness's `y`, `n`, `y`, then the seed,
  with the private passphrase taken from the config. The wallet discovers its addresses only
  if blocks 1 and 2 exist when it starts, so the harness mines them first.
- **Missing votes:** from height 144 the work subsidy is scaled by votes/5. The harness loses
  none: `./mine-alpha` waits for all 5 votes. At height 168 alpha holds
  over 4500 spendable VAR, so step 4 of the chain build mines no extra blocks.
- **`alpha/alpha-ctl.conf`:** the DCR harness's `create-wallet.sh` writes it. It points
  at the alpha wallet, and node calls such as `getblockchaininfo` (which the dcrdex harness
  probes) pass through the wallet to the node. The VAR harness does the same, and adds
  `./alpha_node` for calls straight to the node.
- **Build time:** about 5 minutes from a clean root to funded trading wallets at height 170,
  on a laptop. Step 1a starts without a cached chain.

## Step 1a. Invariant tests in CI

Every change to `main` goes through a PR, upstream DCRDEX merges included. The invariant tests
must run on each one, so this step comes before the harness tests exist; steps 7-10 add
theirs as they land. Upstream DCRDEX's CI runs no harness tests: `run_tests.sh` only compiles them
(`go test -c -tags harness`). This step is our addition.

- **Unit tests (steps 2-6): nothing to add.** `run_tests.sh` already runs
  `go test -race -short ./...` on every push and PR (`.github/workflows/build.yml`). The VAR
  unit tests must not skip themselves under `-short`.
- **Per-PR harness job (new),** on every PR into `main` and every push to `main`:
  1. Build `monetarium-node`, `monetarium-wallet` and `monetarium-ctl` at the versions pinned in
     Versions, cached by version.
  2. Start `dex/testing/var/harness.sh` detached: in CI it skips its final `tmux attach`,
     since a runner has no terminal.
  3. Run `check.sh`.
  4. From step 7 on, also run `go test -tags harness ./client/asset/mon/...`. This item is
     added with step 7, when `simnet_test.go` lands.
- **Nightly job (new):**
  - the BTC, LTC and `dcrdex` harnesses (with PostgreSQL, and `bitcoind`, `litecoind` and
    `vspm` installed on the runner), and the step 8 trade tests on `varbtc`, `varltc` and
    `varspvbtc`;
  - the step 9 load runs and the step 10 VSP run.

  These need nodes of other chains, or long runs, so they stay out of the per-PR job. A step 9
  or step 10 run reports but gates nothing until that step is done.
- **Gating:** the per-PR job becomes a required status check on `main` once it is green on
  `main`. That is a branch-protection change, made by the repo admin.
- **Imported workflows:** the docker release workflow runs on every published release and
  fails without Decred's secrets. The snap workflow runs only when started by hand. Both are
  disabled in the repository's Actions settings, not deleted, so upstream merges that touch
  them do not conflict.

**Done when** both of the following hold:

- the per-PR job is green on `main`;
- a test PR that drops `--rejectnonstd` from the harness makes the job fail on `check.sh`'s
  dust and size controls. The PR is then closed.

## Step 2. Asset id 9508, `dex/bip-id.go`

- **Item 9 test first:** `TestBipSymbolIDVar` (in `dex/bip-id_test.go`). It resets the
  lazily built symbol map and rebuilds it 1000 times. Each time `BipSymbolID("var")` must
  return 9508, and `BipIDSymbol(9508)` must return `var`.
  - It fails today because `var` resolves to 233 and `BipIDSymbol(9508)` returns "". With
    9508 added and 233 left in place, it fails intermittently.
  - This covers what the item's "start the server 20 times" would cover: the server and the
    client both resolve symbols through this one map.
- **Change:** add `9508: "var"` and delete the `233: "var"` entry. No DCRDEX asset uses id
  233. Deleting it keeps the upstream diff to two lines and invents no new symbol.

## Step 3. `dex/networks/mon`

This step ports the parameters, script helpers and size constants to Monetarium's `wire`,
`txscript` and `chaincfg`.

**Tests**, unit tests, written first:

- **Item 4, `TestVarSizes`.** Each size constant (`P2PKHOutputSize`, `P2SHOutputSize`,
  `TxInOverhead` and the input sizes, `InitTxSize`, the redeem, refund and bond sizes) must
  equal the `SerializeSize` of a transaction built with Monetarium's `wire`. It fails on
  Decred's constants: outputs are 1 byte short (37 against 36), and the input witness is
  1 byte short.
- **Item 4, `TestDustThreshold`.** A 37-byte P2PKH output at 10 atoms/B is dust below 6060
  atoms and not dust at 6060. The threshold is derived in the test from the node's formula,
  `(output size + 165) × 3 × rate`.
- **Item 4, `TestMinLotBondSize`.** `MinLotSize(10)` and `MinBondSize(10)` must leave an
  output the node does not call dust, after the redeem or the refund is paid.
- **Item 4, `TestReservePerLot`.** For a split-funded order of 1 to 30 one-lot swaps at
  `MaxFeeRate` 10, `calc.RequiredOrderFunds` with the ported sizes must be at least the sum
  of the chained swaps' worst-case fees, with every signature at 73 bytes. The test runs on
  simnet parameters.
- **Item 2, `TestNoDecredTxCode`.** The test runs `go list -deps` over `client/asset/mon`,
  `server/asset/mon` and `dex/networks/mon`. It fails if any `decred.org/dcrdex/...` package
  in that set imports `github.com/decred/dcrd/wire`, `github.com/decred/dcrd/txscript/v4` or
  `github.com/decred/dcrd/txscript/v4/sign`.
  - These packages do appear in the transitive set, through two importers:
    - the wallet library, whose `wallet` package imports Decred's `txscript/v4/stdaddr` for
      VSP addresses, which pulls in Decred's `txscript` and `wire`;
    - `dex/networks/btc`, a DCRDEX package that imports Decred's `txscript/v4`. The client
      driver reaches it through `client/asset/btc`, which `client/asset/dcr` imports for its
      transaction database. The test names it as an allowed importer, with that reason.
  - So the check is on who imports them, not on whether they are present. The test also
    lists every non-DCRDEX importer and fails when a new one appears. The test is added in
    this step and extended as each package lands.
  - The server builds and signs no transactions, so this check is its whole share of item 2.
    The client's share is `TestSignVerify` (step 5) and `TestBroadcastEachKind` (step 7).

**Done when** the four item 4 tests and `TestNoDecredTxCode` pass.

## Step 4. Server asset, `server/asset/mon`

The port of `server/asset/dcr`, tested with its `testBackend`/`testNode` pattern.
Fixtures are built with Monetarium's `wire`, and the verbose RPC results are shaped as the node
returns them: `Value` is absent for SKA outputs, `SKAValue` is a string, and `estimatesmartfee`
returns atoms/kB as an integer string.

**Tests**, written first:

- **Item 1:** `TestContractSKAOutput`, `TestFundingCoinSKAOutput`, `TestParseBondTxSKA`.
  - A contract, funding coin or bond output to the right script, paid in SKA1, must make
    `Contract`, `FundingCoin` and `ParseBondTx` return an error. Two variants: `SKAValue`
    equal to the expected VAR atoms, and the same scaled ×1e10.
  - **"Reject" means an error from the asset method.** A value of 0 left for the caller to
    catch is not enough: `FundingCoin` and `Contract` take no expected value, so the caller
    may not catch it.
- **Item 7, `TestFeeRateUnits`.** The `estimatesmartfee` answers `"10000"`, `"10001"`,
  `"15500"` and `"1000000"` must give 10, 11, 16 and 1000 atoms/B.
  - Both the server and the client round up (step 5 asserts the same table). A rate rounded
    down could fall below the node's minimum.
  - Any answer under 10 atoms/B returns 10.
- **Item 3, `TestVarMaxFeeRateBound`.** A VAR asset config with `maxFeeRate` above 600 atoms/B
  must be refused at load. The node's ceiling is max(size × 1000, 1 000 000) atoms, and a swap
  can pay more than size × rate: the driver leaves change it would call dust, under 606 × rate,
  in the fee. So the fee can reach (size + 643) × rate − 1. The tightest size is 1000 bytes,
  where the ceiling is still the flat 1 000 000, which allows at most 608 atoms/B
  (1 000 000 / 1643); 600 leaves a little margin. The asset backend is not given `maxFeeRate`
  at setup (`BackendConfig` has no such field): `server/dex` parses and checks it. So the bound
  and its test land in `server/dex`, next to both existing checks that `maxFeeRate` is not 0
  (in `loadMarketConf` and `NewDEX`).
- **Item 10:** `TestCheckSwapAddressDecred` and `TestValidateXPubDecred`, on mainnet
  parameters. A Decred mainnet address must be refused by `CheckSwapAddress`, and a Decred
  mainnet extended key by `ValidateXPub`. On simnet and testnet the prefixes are the same as
  Decred's, so only a mainnet test can fail.
- **Item 8, `TestFundingCoinStakerFee`.** An output of a staker-fee transaction must be
  refused by `FundingCoin` on any network. The coin can disappear in a block, so a funding
  coin that is one fails the swap later.

**Done when** these tests pass, together with the ported `server/asset/dcr` tests, adapted.

## Step 5. Client driver, RPC wallet, `client/asset/mon`

The port of `client/asset/dcr`, registered in `client/asset/importall`. It is tested with
`tNewWallet`/`tRPCClient`; item 6's fixtures list VAR and SKA outputs together, as the wallet
does when no coin type is given. Redeem, refund and bond refund already sign for real in these
tests: `createSig` signs redeem and refund with the key `AddressPrivKey` returns, and the bond
refund signs with the bond key its caller passes. Swap, send, withdraw, split and bond go
through the `signrawtransaction` stub, which today returns the transaction unsigned. That stub
gets a real signer.

**Tests**, written first:

- **Item 1, `TestAuditContractSKAOutput`.** Given a counterparty contract paid in SKA1 (both
  item 1 variants), `AuditContract` must return an error from each source it reads: the
  transaction data, the node's `gettxout`, which reports no coin type and a zero value for an
  SKA output, and the block a filter scan finds.
- **Item 2, `TestSignVerify`.**
  - Each kind the driver builds, built through the `ExchangeWallet` methods:
    - swap, redeem, refund, and the refund `Swap` signs in advance;
    - bond, the bond refund `MakeBondTx` signs in advance, and the bond refund `RefundBond`
      sends;
    - send, withdraw, split.
  - Each is run through Monetarium's script engine and must verify. The flags are the node's
    standard verify flags (`BaseStandardVerifyFlags` plus the agenda flags the node adds),
    copied into the test because the node does not export them (`internal/mempool` and its main
    package). The agenda flags are the SHA256 and treasury ones, which Monetarium's simnet
    params force on (mainnet and testnet force only SHA256); the test runs on simnet. Step 7's
    broadcast against the node catches any drift.
  - The stub's `signrawtransaction` signs every input as a P2PKH spend of one test key, so
    for swap, send, withdraw, split and bond the test checks the transaction the driver
    builds, not the wallet's signer; step 7 runs the wallet's.
  - The txid in the coin ID the method returns (for a split, its funding coins' txid) must
    equal the txid of the bytes passed to `sendrawtransaction` (for a bond, of `SignedTx`),
    decoded with Monetarium's `wire`.
  - `v1.0.6` has no private redeem or refund. They come under this test once an upstream
    merge brings them.
- **Item 3, `TestFeeRateCeiling`.** No path may build a transaction whose fee exceeds the
  node's ceiling, max(size × 1000, 1 000 000) atoms at its Monetarium serialized size. The
  wallet's fee rate limit is raised above 10 000 atoms/B, as a user may, so the high rates
  reach the driver:
  - swaps at the market's highest `MaxFeeRate`, 600, with the maximum swap fee bump (2.0×),
    of 5 and 6 inputs (about 900 and 1100 bytes), the leftover after the contract swept past
    what a change output would call dust at the bumped 1200 atoms/B;
  - a redeem with the maximum fee bump (2.0×) over a network rate of 10 000 atoms/B;
  - a refund at 10 000 atoms/B from its caller;
  - a send at 10 000 atoms/B;
  - a bond refund at the same network rate of 10 000 atoms/B;
  - orders at a market `MaxFeeRate` of 601, through `FundOrder` and `FundMultiOrder`, which
    must be refused at funding: the clamp would make their swaps pay under the market's rate.

  The test checks the fee of every transaction passed to `sendrawtransaction` against that
  ceiling, from the values its fixtures give the inputs. A clamp in the driver is the guard
  for both DEX wallet types, because the native SPV wallet ignores the high-fee flag. It
  has two parts. A transaction with no change output (redeem, refund, bond refund) pays at
  most the flat 1 000 000, or 600 atoms/B above 1666 bytes, under the ceiling at any size, so
  the redeem, refund and bond refund above must each pay exactly 1 000 000: they keep their
  room to outbid other transactions under SKA load. A batch of six redeems at the same
  rates, about 1840 bytes, must pay at least 600 atoms/B. A redeem of 0.06 VAR at the same
  rates must go through as well and pay exactly 1 000 000: its dust check runs at the rate
  the capped fee pays. A transaction that may get change is built at a rate of at most
  600 atoms/B, because change it calls dust stays in the fee; `TestFeeCaps` in
  `dex/networks/mon` checks both bounds at every size.
- **Item 4, `TestRedeemRefundAtMinRelay`.** A redeem and a refund at 10 atoms/B, each signed
  with a key ground for that transaction so its signature has a 33-byte R and a 32-byte S.
  Signatures are deterministic (RFC 6979), so the test grinds over a fixed key sequence and
  finds the same key on every run; a fixed key would stop giving those lengths whenever a
  change moved the fee. Each transaction must pay at least 10 atoms/B of its Monetarium
  serialized size, the node's minimum at a dynamic fee multiplier of 1. `DERSigLength` keeps
  one byte of slack over this signature, so a size estimate one byte short still passes and two
  bytes short fail.
- **Item 4, `TestDustChange`.** A swap whose change output, after paying its own fee, would
  hold 6040 atoms at 10 atoms/B, must either create no change output or create change of at
  least the node's dust threshold, 6060.
- **Item 5, `TestLargeSwapRefused`.**
  - Setup: a wallet holding only 300 outputs of 0.01 VAR, and orders of 2.4 VAR (242 inputs)
    and 2.1 VAR (211 inputs), at `MaxFeeRate` 24 with 0.1 VAR lots.
  - Pass: the driver must not broadcast a single-contract swap or a split larger than
    29 500 bytes. Under SKA load VAR's share of a default block is 37 500 bytes, and the node
    fills it with votes, automatic revocations and tickets before any regular transaction: 5
    votes and 20 new tickets alone take about 7 950 bytes. A regular transaction larger than
    what is left is skipped in every block while SKA fills it. A swap of several contracts,
    one per match, can still be larger, and this test does not build one.
  - Without the cap, coin selection picks 242 inputs for the 2.4 VAR order at `MaxFeeRate`
    24 with 0.1 VAR lots, a swap of about 40 500 bytes, and 211 for the 2.1 VAR order, about
    35 300 bytes: both must be refused, with and without a split.
  - An order of 1.7 VAR, 171 inputs and about 28 600 bytes, fits and must be swapped, with
    and without a split; so must the largest order `MaxOrder` offers.
  - The cap is in the coin selection for orders and for the multi-order split, so it does
    not refuse a send, a withdraw or a bond. `FundMultiOrder` with a split, for the 2.4 VAR
    order, must refuse it: neither its per-order selection nor its split may exceed the cap.
- **Item 6, `TestFundingIgnoresSKA`.** The stub's SKA1 outputs have step 1's shape (four of
  1000 SKA1 and one of 5 SKA1), plus one of 1000 SKA2, all confirmed, spendable P2PKH like the
  VAR ones, and every amount funded, sent or withdrawn is above the stub's largest VAR output
  (but for one multi-order of 1 VAR), so a driver that misread an SKA output as VAR would
  select one: coin selection takes the set that over-funds least, which the 5 SKA1 output
  enters. A second fixture adds an SKA1 output with 18 decimals, which a driver that parsed it
  as VAR would refuse, failing the whole list; a third, with the wallet configured with a
  trading account, moves the first fixture's SKA outputs into it. On each, every one of the
  following must succeed without selecting or locking an SKA outpoint:
  - `FundOrder` (standard and split; the split must be sent);
  - `FundMultiOrder` (with and without a split; every order must be funded);
  - `Send`;
  - `Withdraw`;
  - `MakeBondTx`.

  This covers the coins passed to `lockunspent` and the inputs of every built transaction.
  `FundingCoins`, asked for an SKA coin, must not find it.
- **Item 7, `TestFeeRateUnits`.** The same table as step 4, through `FeeRate`;
  `EstimateSmartFeeRate` returns an error for an answer that is not an unsigned integer, such
  as Decred's coins/kB. This covers an RPC wallet whose external wallet is RPC-synced. For the
  native SPV wallet, and for an RPC wallet whose external wallet is SPV-synced, the driver
  never calls `estimatesmartfee`, and its rates are `TestFeeOracle`'s (step 6).
- **Item 8, `TestStakerFeeNotSelected`.** Outputs of staker-fee transactions in the funding
  account must never be selected, whatever their age, through the paths item 6's test runs.
  The wallet lists one once matured, spendable, with an `OP_SSGEN`-tagged P2PKH script.
- **Item 10, `TestDecredAddressRejected`.** On mainnet parameters, a Decred mainnet address
  must be refused by:
  - `ValidateAddress`;
  - `Send` and `Withdraw`;
  - `Swap`, for the counterparty address it pays the contract to.

**Done when** these tests pass, together with the ported `client/asset/dcr` unit tests.

## Step 6. Client driver, native SPV wallet

The native SPV wallet (`spv.go`, `native_wallet.go`) on the wallet library at the pin above.
Today SPV is tested only at the method level (`tDcrWallet`), so this step adds a test seam:
an `ExchangeWallet` over the `tDcrWallet` mock. The mock's `SignTransaction` gets a real
signer, so item 2 holds here too.

**Tests**, written first:

- **Item 6:** `TestSPVFundingIgnoresSKA` and `TestTransferAccountIgnoresSKA`. The first
  repeats step 5's item 6 test over the SPV seam. The second covers the native wallet's
  account transfer. The first runs with step 5's SKA outputs and amounts, the second with
  step 5's SKA outputs only (it takes no amounts); neither may select or lock an SKA outpoint.
- **Item 7, `TestFeeOracle`.** The oracle URL becomes a wallet field the test can set; it was
  hard-coded.
  - The VAR driver's oracle is `monetarium.online/insight/api/utils/estimatefee` on mainnet
    and the testnet explorer's same route on testnet. The testnet route answered
    `{"2":0.0001}` when queried by hand on 2026-11-20. Simnet has no oracle.
  - The test points the fetch at a local stub server and runs four cases:
    - an unreachable source;
    - a Decred-shaped answer below 10 atoms/B (`{"2":0.00009}`);
    - a valid answer;
    - no oracle (simnet).
  - An answer must give a `FeeRate` of at least 10 atoms/B. An unreachable source or no oracle
    must give 0, which core reads as no rate and replaces with the server's.
  - In all four cases, a send given `FeeRate`'s answer must pay at least 10 atoms/B. So must a
    send given a suggestion, or a configured fallback rate, below 10.
- **Item 2:** `TestSignVerify` repeated over the SPV seam.
- **Item 8, `TestSPVStakerFeeNotSelected`.** Step 5's item 8 test over the SPV seam. The
  native wallet's account transfer must not sweep a staker-fee output either.

**Done when** these tests pass.

## Step 7. Driver against the harness

`client/asset/mon/simnet_test.go`, build tag `harness`, against step 1's harness. Each test
runs with an RPC wallet and with a native SPV wallet, unless noted.

- **Item 2, `TestBroadcastEachKind`.** Each step 5 kind is sent to alpha (`--rejectnonstd`)
  and must be accepted. The txid the node returns must equal the local one. For the refund
  kinds, the test uses a 1-minute lock time and mines until the median time of recent blocks
  passes it, as the trade tests do. Before that, the node refuses the refund as non-final.
- **Item 3, `TestFeeCeilingNode`.** A transaction of at least 1000 bytes, built with the
  port's helpers rather than the driver, whose clamp would stop it. It is sent to alpha with
  `sendrawtransaction`, high fees not allowed. It must be accepted at 1000 atoms/B and refused
  at 1001 with the node's high-fee error. This confirms the bound step 5 relies on.
  No wallet type applies.
- **Item 4:**
  - `TestDustChangeNode`: step 5's 6040-atom case, broadcast; it must be accepted.
  - `TestChainedSwapsReserve`: a 20-lot split-funded order, funded at `MaxFeeRate` 10, then
    swapped one lot at a time, 20 chained swaps. All 20 must be accepted and confirm.
  - Simnet cannot force 33-byte R values, so the arithmetic bound stays in step 3. This test
    checks that the reserve holds in practice.
- **Item 1, `TestSPVMinedSKAContract`.** Native SPV wallet, no transaction data, a contract
  paid in SKA1 and mined within `ContractSearchLimit`. `AuditContract` must return an error.
- **Item 6, `TestSKAWalletFunding`.** The trading wallet holds step 1's SKA1 outputs (four of
  1000 SKA1 and one of 5 SKA1). The native SPV wallet, which the test creates, is first sent
  the same five from alpha, and the test checks it holds them. Every amount funded, sent or
  withdrawn is above the largest VAR output of the wallet funding it (25 VAR in the trading
  wallets), so a misread SKA1 output would be picked before any VAR one. The test runs, in
  both DEX wallet types:
  - order funding (standard and split), send and withdraw;
  - account transfer (native SPV only).

  After each, the wallet's SKA1 balance must be unchanged, and no SKA outpoint may be locked
  or appear in any transaction's inputs.

  An RPC-wallet run against `spvwallet` covers the external wallet's own SPV publish path. It
  passes only if every transaction it publishes is accepted by alpha (in its mempool or a
  block).

**Done when** all of step 7's tests pass, twice, on a fresh harness.

## Step 8. Server market and trade tests

**Server harness.** When the VAR harness runs, `dex/testing/dcrdex/genmarkets.sh` hands off
to `genmarkets-var.sh`, which writes the VAR markets only, so the VAR runs need no DCR
harness. Its VAR asset entry: `maxFeeRate` 10, `swapConf` 1, the config of the alpha node, a
`regXPub` from a `server_fees` account in the alpha wallet, and a 0.5 VAR bond. Its markets
are `var_btc` (the BTC harness must run) and `var_ltc` (when the LTC harness runs), with VAR
as base: 1 VAR lots, which leaves a wide margin over `MinLotSize(10)`. Their rate step of
1000 puts the trade tests' lowest rate, 100 steps, above the server's minimum rate: 47200
for BTC, 9440 for LTC. Lock times are the build's 1 minute (taker) and 2 minutes (maker).
`server/asset/importall` gains the blank import of `server/asset/mon`, so that `dcrdex`
registers the VAR server asset.

**Trade tests.**

- `simnet_trade.go` gains a VAR case:
  - its harness-control switch and wallet-config builder (`~/dextest/var/<node>/<node>.conf`,
    wallet type `dcrwalletRPC`);
  - the import of the VAR driver.
- `simnet-trade-tests/run` gains `varbtc`, `varltc` and `varspvbtc` (native SPV on the VAR
  side). With no `--regasset`, the bond asset defaults to the base, so these runs post their
  bonds in VAR.
- The refund-fee expectation in `assertBalanceChanges` uses BTC sizes at 1 sat/vB. Its VAR
  case uses VAR's sizes at 10 atoms/B.

**Done when** all seven `v1.0.6` tests pass on `varbtc`, `varltc` and `varspvbtc`, each
run with client 1 as maker and then as taker (`orderstatus` runs once, as the tool does):

- `success`;
- `nomakerswap`;
- `notakerswap`: the maker refunds;
- `nomakerredeem`: both refund;
- `makerghost`;
- `orderstatus`;
- `resendpending`.

The refunds are the item 2 refund paths on a live market.

**Not backported: `notakerredeem`.** On upstream master it blocks only the taker's
notification to the server. The taker still redeems on-chain, and the trade ends
`MatchComplete`, so it exercises no refund. `notakerswap` and `nomakerredeem` are the refund
tests.

## Step 9. Item 5: confirmation under SKA load

**Form:** harness runs only; the property needs a full block template.

**Load:** `dex/testing/var/load.sh` runs two kinds of traffic before every mined block.

- **SKA1:** more than 337 500 bytes of SKA1 transactions in alpha's mempool, so SKA demand
  fills its share of the default 375 000-byte block. These are `sendmany` transactions with
  many outputs, each under 100 000 bytes.
- **VAR:** transactions filling VAR's 37 500-byte share at the fee rate alpha's
  `estimatesmartfee` reports, the rate the DEX itself uses.

Both go through wallet RPC to alpha.

**Market:** for these runs the VAR market's `maxFeeRate` is 100, not step 8's 10. The node's
estimate is at least 15 atoms/B on simnet, and the server caps the swap rate at `maxFeeRate`.
At 10, swaps would pay less than the VAR traffic they compete with.

**Run under load:**

- **Trade tests:** `success`, `nomakerredeem` and `notakerswap` on `varbtc`, RPC wallets on
  both clients, lock times 1 minute (taker) and 2 minutes (maker).
  - **Pass:** each test passes unchanged, within its own timeouts: 4 minutes per trade, and
    60 seconds from lock time to a refund coin.
  - **Pass:** every swap, redeem and refund confirms within 3 blocks of its broadcast. The
    trade tests alone do not check this: they count unconfirmed coins in balances and do not
    wait for a redeem to confirm.
  - **Measured:** blocks from broadcast to confirmation for each of them.
- **`TestLargeSwapUnderLoad`:** orders in step 5's 0.1 VAR lots, funded without a split
  through the driver's `FundOrder` at `MaxFeeRate` 100 (the market's under load) from 240
  outputs of 0.01 VAR, and swapped at the DEX's rate, the estimate capped at 100, from a wallet
  whose other VAR outputs are locked. Coin selection takes the largest coins first, so
  otherwise it would fund from a few big ones.
  - **Pass:** a 2.1 VAR order, 215 inputs at this rate, which the outputs cover, is refused at
    funding: its swap, about 35 700 bytes, would never confirm (step 5, item 5).
  - **Pass:** the largest order that funds, stepping down 0.01 VAR from 2.1, is within two
    inputs of the 29 500-byte cap by the driver's size estimate, and its swap confirms within
    3 blocks of its broadcast.
- **Fee-rate sweep:** time to confirm for VAR traffic at rates around the DEX's. Recorded for
  the VAR lock-time choice, which is made after this step; not a pass or fail condition.

The driver cannot raise the fee of a transaction it has broadcast. The node refuses any other
spend of the contract as a double spend. So no pass branch relies on a fee bump.

**Done when** the trade tests and `TestLargeSwapUnderLoad` pass under load, and the
measurements are written into this file.

## Step 10. Item 8: staker-fee outputs

**Form:** harness with `VSP=1`, added in this step, which also pins the `monetarium-vsp`
version in Versions. The DEX's RPC wallet is an external wallet that buys tickets
through `vspm`, into the account the DEX funds orders from. The native SPV wallet would get
staker-fee outputs only by staking through DCRDEX, which is out of scope. Its share of item 8
is the unit test in step 6.

**Network:** simnet. Stake outputs mature after 16 blocks there (256 on mainnet). Before the
port, the server accepts a staker-fee output once it is 16 blocks old. Step 4's
`TestFundingCoinStakerFee` makes it refuse one.

**`TestStakerFeeRespend`:**

- **Setup:** keep the RPC wallet's staker-fee outputs in the funding account. Book an order, then
  let another ticket of the same commitment address vote; repeat after a match.
- **Pass:**
  - FundOrder never selects a staker-fee output, checked over 20 blocks of votes;
  - the order stays booked;
  - its swap confirms;
  - the match is not revoked.

**Done when** `TestStakerFeeRespend` passes with the RPC wallet.

## Item 11: Skarb stage, not the PoC

**Network:** simnet, coinbase maturity 16.

**Tests:**

- A Skarb wallet that receives a transaction with an expiry must not select its output before
  maturity. The pass condition is "coin not selected": the order is then funded from other
  coins or refused.
- A Skarb-built transaction must carry no change the node calls dust: either no change output,
  or change of at least the node's threshold.

The DEX's own native SPV wallet avoids both problems by pinning a wallet commit past
`v1.3.14` (see Versions).

## Upstream merges

`client/asset/mon` and `server/asset/mon` start as copies of upstream's DCR asset, so a fix
upstream makes to DCR does not reach VAR on its own. Each upstream release comes in as one PR
into `main` that merges upstream's release tag. Upstream keeps one branch per minor line and
tags each release on it: `v1.0.x` on `release-v1.0`, and later `v1.1.0` on `release-v1.1`.
The PR merges the tag, not the branch head, which can be ahead of the last release. That PR:

- **Ports DCR changes into the VAR copies.** It takes every change upstream made since the
  upstream release last merged (for example `git diff v1.0.6 v1.1.0 -- <dirs>`), not since
  the merge base. A new minor line forked before `v1.0.0`, so its merge base would repeat
  every `v1.0.x` backport the VAR copies already carry. The directories:
  - `client/asset/dcr`, `server/asset/dcr`, `dex/networks/dcr` and `dex/testing/dcr`;
  - the trade-test files: `client/core/simnet_trade.go`, `client/cmd/simnet-trade-tests/run`
    and `dex/testing/dcrdex`.

  Each change is ported into its VAR copy, or the PR body says why it does not apply. Each
  ported hunk is checked against the coin model: a transaction is single-coin, its fee is
  paid in its own coin, and SKA amounts never pass through a float.
- **Re-checks `invariants.md` and this plan's `v1.0.6` references.** The invariants' "DEX
  references against upstream master" section is compared with the new base. `release-v1.1`
  already has private redeem and refund, which then come under item 2, and the
  `notakerredeem` test, so step 5's private-redeem line and step 8's "Not backported" note
  change with it.
- **Passes step 1a's per-PR job.** The nightly job then runs on the merged `main`.

## Order and dependencies

- **Step 1a:** needs step 1. Steps 2-6's unit tests run in the existing CI; steps 7-10 add
  their harness tests to the new jobs as they land.
- **Steps 2-6:** unit tests, each step on top of the one before. They need no harness, so
  they can proceed while step 1 settles.
- **Step 7:** needs steps 1 and 6.
- **Step 8:** needs steps 1, 4 and 6, plus the `dcrdex` and BTC/LTC harnesses.
- **Step 9:** needs steps 7 and 8.
- **Step 10:** needs step 8.
