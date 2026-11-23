# VAR port: invariants the proof of concept must keep

Properties the VAR port of DCRDEX must hold where Monetarium differs from Decred, so DCRDEX's
own DCR tests would not catch a violation. Each one gets its test written before the code it
guards. Items 1-10 belong to the proof of concept (PoC); item 11 and later ones belong to the
Skarb stage, in [`../skarb/invariants.md`](../skarb/invariants.md).

This file states what must be true and, per item, the setup that exposes a violation. How each
test runs (network or unit test, parameters, pass condition) is the PoC plan's job,
[`plan.md`](plan.md).

The claims come from reading the code on both sides; the points marked (checked) were
confirmed by hand. Nothing was built; the only thing run was the node mempool test in item 8.
Versions read, each citation linking to the file at that commit:

- NODE = `monetarium-node` `e1b5d170` (main)
- WAL = `monetarium-wallet` `42aa5102` (main); the mainnet VSP runs `v1.3.14`
- DEX = this repository at `v1.0.6` (`9521e9af`), the DCRDEX version Skarb pins. The DEX side
  was first read on `decred/dcrdex` master `fc634606`; [DEX references against upstream
  master](#dex-references-against-upstream-master) lists what moved and what differs.

Short names: client `dcr.go` = DEX `client/asset/dcr/dcr.go`; client `spv.go` = DEX
`client/asset/dcr/spv.go`; server `dcr.go` = DEX `server/asset/dcr/dcr.go`; `script.go` = DEX
`dex/networks/dcr/script.go`.

What counts as an invariant here: a failure that is silent or shows up late. Failures that are
loud on first use are left to the ordinary port: the version handshake and net magic, amounts
now sent as strings instead of floats, the extra `SpendLimit` argument of `purchaseticket`.

## Funds at risk

### 1. Only a VAR output counts as VAR value

- **Side:** both. Applies to the client's audit of the counterparty's contract and the
  server's checks of contracts, funding coins and bonds.
- **Why it can break:** it holds today only because the Monetarium wire decoder sets `Value = 0` on
  SKA outputs (NODE [`wire/msgtx.go:1785`][node:wire/msgtx.go:1785]), and verbose RPC leaves `Value`
  out for SKA. Fields a porter would reach for carry SKA value: `TxOut.GetValue()` returns SKA atoms
  (NODE [`wire/msgtx.go:610-617`][node:wire/msgtx.go:610-617]), verbose `Vin.AmountIn` is a float in
  SKA coins (NODE
  [`internal/rpcserver/rpcserver.go:1141-1150`][node:internal/rpcserver/rpcserver.go:1141-1150]),
  `listunspent` `amount` is in the output's own coin. A contract paid to the right P2SH with SKA1,
  accepted by the auditor, means the auditor funds its side and loses it.
- **Exposed by:** a counterparty contract that pays the exact P2SH with an SKA1 output whose
  `SKAValue` equals the expected VAR atoms, and a variant scaled ×1e10, fed to client
  `AuditContract` (RPC and SPV) and server `Contract`/`FundingCoin`/`ParseBondTx`.

### 2. Every DEX transaction signs and verifies under Monetarium's rules

- **Side:** both. Swap, redeem, refund, bond, bond refund, send. Upstream master also has
  private redeem and refund; `v1.0.6` does not, and they fall under this item once an upstream
  merge brings them.
- **Why it can break:** the Monetarium sighash commits to coin type and SKA value fields (NODE
  [`txscript/sighash.go:393-399`][node:txscript/sighash.go:393-399],
  [`:425-432`][node:txscript/sighash.go:425-432]), and every output in the serialized prefix starts
  with a coin-type byte (NODE [`wire/msgtx.go:1844-1850`][node:wire/msgtx.go:1844-1850]). A path
  left on Decred's `wire` or `txscript` signs invalid signatures or computes another txid. The
  default trade test covers only swap and redeem, so a refund path left on Decred code fails only
  when a refund is due.
- **Exposed by:** each kind built, signed, run through Monetarium's script engine, its local
  txid compared with the one `sendrawtransaction` returns; and a dependency check that no VAR
  client or server package imports `github.com/decred/dcrd/wire`, `.../txscript` or
  `.../txscript/sign`.

### 3. No DEX transaction pays above the node's fee ceiling

- **Side:** client, and the server's VAR market config.
- **Why it can break:** the node rejects a VAR transaction whose fee exceeds max(size × 1000
  atoms/B, 1 000 000 atoms) unless high fees are allowed (NODE
  [`internal/fees/feecalc.go:134`][node:internal/fees/feecalc.go:134],
  [`:693-696`][node:internal/fees/feecalc.go:693-696], checked), so the 1000 atoms/B cap applies
  only to transactions of 1000 bytes or more; dcrd allows 10 000 × min relay. In RPC mode DCRDEX
  broadcasts its swaps, redeems, refunds and sends (client
  [`dcr.go:5842`][dex:client/asset/dcr/dcr.go:5842]) and its bonds and bond refunds
  ([`:4459`][dex:client/asset/dcr/dcr.go:4459], [`:4637`][dex:client/asset/dcr/dcr.go:4637]) with
  high fees not allowed; in SPV mode the flag is ignored (client
  [`spv.go:740-744`][dex:client/asset/dcr/spv.go:740-744], checked). The market falls back to
  `MaxFeeRate` (DEX [`server/market/market.go:2315-2316`][dex:server/market/market.go:2315-2316]).
  The node reports a too-high fee as `ErrInsufficientFee` (NODE
  [`internal/mempool/mempool.go:1894-1897`][node:internal/mempool/mempool.go:1894-1897]).
- **Exposed by:** a DEX transaction whose fee exceeds max(size × 1000, 1 000 000) atoms,
  reached through the market's `MaxFeeRate` or the client's swap and redeem fee bumps.

### 4. Size, fee and dust arithmetic uses Monetarium's transaction sizes

- **Side:** client, and the server's minimum lot and bond sizes.
- **Why it can break:** a Monetarium output is 1 byte larger than Decred's (coin type) and each
  input witness 1 byte larger (NODE [`wire/msgtx.go:513-526`][node:wire/msgtx.go:513-526],
  [`:553-570`][node:wire/msgtx.go:553-570]); DCRDEX's constants are Decred's
  ([`script.go:50-53`][dex:dex/networks/dcr/script.go:50-53],
  [`:67`][dex:dex/networks/dcr/script.go:67], [`:84-87`][dex:dex/networks/dcr/script.go:84-87]).
  Four effects:
  - **Redeem and refund fees** have 0 bytes of margin at min relay: the 36-byte output
    constant against 37 is offset only by 1 byte of slack in `DERSigLength`
    ([`script.go:90`][dex:dex/networks/dcr/script.go:90]). One more byte and the transaction is 10
    atoms short and refused.
  - **The funding reserve per lot** can fall short for a split-funded order of 19 or more
    one-lot swaps at `MaxFeeRate` when every signature has a 33-byte R (DEX
    [`dex/calc/fees.go:7-15`][dex:dex/calc/fees.go:7-15]); the last swap fails.
  - **Dust:** the node's threshold at 10 atoms/B is 6060 atoms (NODE
    [`internal/mempool/policy.go:312-326`][node:internal/mempool/policy.go:312-326]; the comment
    there still says Decred's 6030), DCRDEX's 6030, so change of 6030-6059 atoms is added and then
    refused.
  - **The server's minimum lot and bond sizes** use the same 36-byte output
    ([`script.go:155-181`][dex:dex/networks/dcr/script.go:155-181], checked) and leave 6030 atoms
    against the node's 6060 when a lot or bond sits at the minimum.
- The explorer's fee rate and the harness markets' `maxFeeRate` sit exactly at min relay (10
  atoms/B); the node's `estimatesmartfee` returns at least 15 on simnet once it has built a block
  template (NODE [`internal/fees/feecalc.go:313-317`][node:internal/fees/feecalc.go:313-317],
  checked), so RPC-mode redeems and refunds do not reach these edges.
- **Exposed by:** a redeem and a refund signed with a 33-byte R and a 32-byte S at 10 atoms/B,
  submitted to `mond`; a 20-lot split-funded order filled one lot per epoch at `MaxFeeRate`
  10; a swap leaving 6040 atoms of change at 10 atoms/B; a lot or bond at the server's minimum.

### 5. Every DEX transaction confirms in time under SKA load

- **Side:** client; the lock-time choice is the server's.
- **Why it can break:** VAR shares block space with SKA. Under SKA load VAR's share is 10% of the
  block (NODE
  [`internal/blockalloc/allocator.go:118-120`][node:internal/blockalloc/allocator.go:118-120],
  checked), plus a 10% share of any unused space
  ([`:172-179`][node:internal/blockalloc/allocator.go:172-179]): about 37 500 of the default 375 000
  bytes per block (NODE [`config.go:77`][node:config.go:77]). Two consequences:
  - **Redeems and refunds** compete for that share with other VAR transactions, and the driver
    cannot raise the fee of a transaction it has broadcast: the node refuses any other spend of
    the contract as a double spend (NODE
    [`internal/mempool/mempool.go:1055-1058`][node:internal/mempool/mempool.go:1055-1058]).
  - **A swap larger than the room VAR's share leaves never confirms.** The node
    fills the share with votes, automatic revocations and tickets before any regular
    transaction (NODE
    [`internal/mining/txpriorityqueue.go:103-134`][node:internal/mining/txpriorityqueue.go:103-134])
    and skips a transaction that does not fit what is left, in every block
    ([`internal/mining/mining.go:2458-2471`][node:internal/mining/mining.go:2458-2471]). 5 votes
    and up to 20 new tickets a block alone (NODE
    [`chaincfg/mainnetparams.go:526-536`][node:chaincfg/mainnetparams.go:526-536]) take about
    7 950 bytes, which leaves about 29 500. `Swap` puts every funding
    coin of the order into one transaction (client
    [`dcr.go:3097`][dex:client/asset/dcr/dcr.go:3097], checked), and the only size cap is the
    node's 100 000-byte standard size at broadcast (NODE
    [`internal/mempool/policy.go:361-363`][node:internal/mempool/policy.go:361-363]);
    `v1.0.6` has no size check of its own.
- How long VAR lock times should be is decided after this test.
- **Exposed by:** a simnet SKA1 mempool kept above 337 500 bytes per block plus VAR traffic,
  while the `success`, `nomakerredeem` and `notakerswap` trade tests run with short lock times
  (upstream master adds `notakerredeem`, which `v1.0.6` lacks); and an order funded by many
  small coins under the same load.

## Failed swap or rejected order

### 6. Funding and change use VAR coins only

- **Side:** client, RPC and SPV mode.
- **Why it can break:** listing unspent outputs without a coin type returns VAR and every SKA type
  together (WAL [`wallet/wallet.go:4562-4568`][wallet:wallet/wallet.go:4562-4568]), SKA outputs are
  marked spendable ([`:4690-4697`][wallet:wallet/wallet.go:4690-4697]), and DCRDEX has no coin
  check. In RPC mode the wallet signs an SKA input under VAR outputs and the node rejects the
  broadcast. In SPV mode publishing reports success once a peer takes the bytes (WAL
  [`spv/backend.go:482-500`][wallet:spv/backend.go:482-500]), peers drop the transaction, and the
  match is revoked against the user. The account balance is VAR-only, so it looks right while
  selection is wrong.
- **Exposed by:** a trading wallet holding SKA1 outputs larger than any VAR output, through
  order funding (standard and split), send, withdraw and account transfer in both modes; no
  SKA output may be selected or locked.

### 7. Fee rates come from Monetarium, in the right units

- **Side:** both.
- **Why it can break:** the node's `estimatesmartfee` returns atoms/kB as an integer string (NODE
  [`internal/rpcserver/rpcserver.go:1569-1578`][node:internal/rpcserver/rpcserver.go:1569-1578]);
  DCRDEX decodes a coins/kB float and multiplies by 1e8 (server
  [`dcr.go:412-425`][dex:server/asset/dcr/dcr.go:412-425]). A partial fix is off by 10^8 or lands at
  1 atom/B, below min relay. In SPV mode DCRDEX asks Decred's dcrdata for a rate (client
  [`dcr.go:1363-1384`][dex:client/asset/dcr/dcr.go:1363-1384]).
  `monetarium.online/insight/api/utils/estimatefee` serves the same route in the same format
  (`{"2":0.0001}`, checked 2026-11-17).
- **Exposed by:** the server's and client's rates compared with `estimatesmartfee`; an SPV
  wallet with the fee source unreachable, and with a Decred-shaped rate below 10 atoms/B.

### 8. Funding does not rely on staker-fee outputs

- **Side:** client; the server for funding-coin checks.
- **Why it can break:** VAR staker fees are paid by a separate staker-fee transaction, which any
  miner can respend into a new output to the same script without the owner's key ("augment") (NODE
  [`internal/blockchain/validate.go:3933-3975`][node:internal/blockchain/validate.go:3933-3975],
  checked). The owner keeps the value, but the specific coin disappears. Only the staker-fee
  transaction of a later vote that pays the same address can respend it.
  - **A wallet voting its own tickets** pays every vote's staker fee to one consolidation
    address of the ticket's account: by default the account's first external address, or
    whatever address `setvotefeeconsolidationaddress` set (WAL
    [`wallet/chainntfns.go:760-785`][wallet:wallet/chainntfns.go:760-785], checked). With the
    default, each of its later votes' staker-fee transactions can respend the earlier output,
    in the account the DEX funds orders from.
  - **A VSP voting wallet** pays a ticket's staker fees to the ticket's commitment address, i.e.
    the buyer's (WAL `v1.3.14`
    [`wallet/chainntfns.go:731-768`][wallet-v1.3.14:wallet/chainntfns.go:731-768], checked; the
    mainnet VSP runs `v1.3.14`). The wallet takes a fresh commitment address for each ticket it
    buys (WAL [`wallet/createtx.go:1810-1828`][wallet:wallet/createtx.go:1810-1828],
    [`:2224`][wallet:wallet/createtx.go:2224], checked), so its own VSP tickets never share one.
    DCRDEX buys tickets with such addresses in its primary account (client
    [`spv.go:1065-1084`][dex:client/asset/dcr/spv.go:1065-1084]). A ticket bought by anyone with
    the wallet's address as its commitment pays its staker fee there too.

  Within one block template a regular spend already placed wins; one still in the mempool loses and
  becomes a double spend (NODE
  [`internal/mining/mining.go:880-906`][node:internal/mining/mining.go:880-906],
  [`:2603`][node:internal/mining/mining.go:2603]).
- **Only miners, only inside a block.** The mempool runs script validation on a staker-fee respend
  and rejects an unsigned one (NODE
  [`internal/mempool/mempool.go:1907`][node:internal/mempool/mempool.go:1907]; the skip at
  [`internal/blockchain/scriptval.go:267`][node:internal/blockchain/scriptval.go:267] applies to
  blocks only). Checked with the node's mempool test harness on mainnet params: the unsigned respend
  was rejected and the owner's signed spend of the same output accepted.
- On mainnet the server already rejects stake outputs younger than 256 blocks (see "Not
  invariants"), so the live case is an aged staker-fee output respent while an order is booked
  or a swap is pending.
- **Exposed by:** a DCRDEX RPC wallet that votes its own tickets, with staker-fee outputs in its
  funding account; its tickets vote while an order is booked.

### 9. `var` resolves to 9508 in every process

- **Side:** both.
- **Why it can break:** the symbol `var` is already BIP id 233 (DEX
  [`dex/bip-id.go:278`][dex:dex/bip-id.go:278]), and the symbol→id map is built by iterating another
  map ([`:16-25`][dex:dex/bip-id.go:16-25], checked), so with 9508 also named `var` the winner
  changes per process. The server resolves markets through it; when 233 wins it fails with "no asset
  registered for var (233)".
- **Exposed by:** rebuilding the map 1000 times; starting the server 20 times.

### 10. Monetarium network parameters everywhere

- **Side:** both.
- **Why it can break:** any Decred chain parameter left in the port (addresses, HD ids, network
  checks) passes silently on simnet and testnet, where Monetarium's address prefixes equal Decred's
  (NODE [`chaincfg/simnetparams.go:545-559`][node:chaincfg/simnetparams.go:545-559],
  [`chaincfg/testnetparams.go:542-545`][node:chaincfg/testnetparams.go:542-545], checked). Mainnet
  prefixes differ.
- **Exposed by:** client and server, with mainnet parameters, given a Decred mainnet address.

## Skarb stage, not the PoC

Item 11 and the Skarb stage's other invariants are in
[`../skarb/invariants.md`](../skarb/invariants.md).

## Not invariants

- **Server stricter than the node on stake-output maturity.** The server requires 256 blocks for any
  stake output (server [`dcr.go:1067-1073`][dex:server/asset/dcr/dcr.go:1067-1073]); the mainnet
  node requires 1 for votes' outputs, because the treasury agenda can never activate (its vote
  window ended 2021-08-01, NODE
  [`chaincfg/mainnetparams.go:318-319`][node:chaincfg/mainnetparams.go:318-319], checked; Monetarium
  has no treasury). Costs only rejected orders; simnet does not show it.
- **Input values on the server** feed only the fee rate (server
  [`dcr.go:822-856`][dex:server/asset/dcr/dcr.go:822-856]); no swap step reads a redemption's
  value. Not a risk.
- **DCRDEX's VSP list is Decred's.** `ListVSPs` fetches it from `api.decred.org` (client
  [`dcr.go:5794-5806`][dex:client/asset/dcr/dcr.go:5794-5806]), so staking through the DEX wallet
  could pick a Decred VSP; the ticket is published before the VSP fee fails. Staking through
  DCRDEX is outside the PoC, and Skarb has its own VSP client.
- **SKA activity is invisible or shown as VAR** in the DEX wallet: an SKA receipt shows as a
  0-VAR receive, and an address that received only SKA is handed out again (it counts VAR
  only). Display only; for the Skarb screens.
- **Harness facts** (for the plan): SKA1 on simnet is emitted only at heights 150-250 with a fixed
  key (NODE [`chaincfg/simnetparams.go:653-668`][node:chaincfg/simnetparams.go:653-668],
  [`internal/blockchain/ska_emission.go:42-45`][node:internal/blockchain/ska_emission.go:42-45],
  checked); the Decred chain archive cannot be reused (no premine); stake validation starts at 144,
  so voting wallets must run; the server's node runs `--txindex`.

## Open

None that blocks the PoC tests; item 5's lock-time choice follows its test. Settled on
2026-11-17 by a test: the mempool rejects other parties' staker-fee respends, so the race in
item 8 is in-block only.

## DEX references against upstream master

The DEX side was read on `decred/dcrdex` master `fc634606` and re-pointed to `v1.0.6` here.
Every citation not listed below sits on the same lines in both: `dex/calc/fees.go`,
`dex/bip-id.go` and server `dcr.go`.

Moved, same code:

| Cited for | master `fc634606` | `v1.0.6` |
| --- | --- | --- |
| 3: swap, redeem, refund and send broadcast | client `dcr.go:7042` | client `dcr.go:5842` |
| 3: bond refund broadcast | client `dcr.go:5350` | client `dcr.go:4459` |
| 3: bond broadcast (`SendTransaction`) | client `dcr.go:5528` | client `dcr.go:4637` |
| 3: SPV ignores `allowHighFees` | client `spv.go:743-747` | client `spv.go:740-744` |
| 3: market falls back to `MaxFeeRate` | `server/market/market.go:2320-2321` | `:2315-2316` |
| 4: input overhead | `script.go:55-58` | `script.go:50-53` |
| 4: output overhead | `script.go:72` | `script.go:67` |
| 4: swap size base | `script.go:89-92` | `script.go:84-87` |
| 4: `DERSigLength` | `script.go:95` | `script.go:90` |
| 4: minimum lot and bond | `script.go:199-225` | `script.go:155-181` |
| 5: `Swap` funding inputs | client `dcr.go:3896` | client `dcr.go:3097` |
| 8: ticket purchase account | client `spv.go:1066-1085` | client `spv.go:1065-1084` |
| Not invariants: VSP list | client `dcr.go:6685-6697` | client `dcr.go:5794-5806` |

Differs on `v1.0.6`:

- **Item 2:** private redeem and refund (`MakePrivateContract` and its spends in `script.go`)
  exist only on master.
- **Item 4:** master checks every output for dust before broadcast (client
  `dcr.go:7015-7039`); `v1.0.6` has no such check, and change is kept or dropped by
  `IsDustVal` with the 36-byte output (client `dcr.go:5177`, `:5360`). The item holds on both.
- **Item 5:** master checks a transaction against a 100 kB `MaxStandardTxSize` before
  broadcast (client `dcr.go:6997-7012`); `v1.0.6` has no such check, so the node's own limit
  is the only one. The `notakerredeem` trade test exists only on master.
- **Item 7:** the client's fee oracle (master client `dcr.go:1438-1459`, `v1.0.6`
  `:1363-1384`) asks `explorer.dcrdata.org` and `testnet.dcrdata.org` on `v1.0.6`,
  `dcrdata.decred.org` and `testnet.decred.org` on master. Both are Decred's; the item holds
  either way.

[dex:client/asset/dcr/dcr.go:1363-1384]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/client/asset/dcr/dcr.go#L1363-L1384
[dex:client/asset/dcr/dcr.go:3097]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/client/asset/dcr/dcr.go#L3097
[dex:client/asset/dcr/dcr.go:4459]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/client/asset/dcr/dcr.go#L4459
[dex:client/asset/dcr/dcr.go:4637]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/client/asset/dcr/dcr.go#L4637
[dex:client/asset/dcr/dcr.go:5794-5806]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/client/asset/dcr/dcr.go#L5794-L5806
[dex:client/asset/dcr/dcr.go:5842]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/client/asset/dcr/dcr.go#L5842
[dex:client/asset/dcr/spv.go:1065-1084]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/client/asset/dcr/spv.go#L1065-L1084
[dex:client/asset/dcr/spv.go:740-744]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/client/asset/dcr/spv.go#L740-L744
[dex:dex/bip-id.go:16-25]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/dex/bip-id.go#L16-L25
[dex:dex/bip-id.go:278]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/dex/bip-id.go#L278
[dex:dex/calc/fees.go:7-15]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/dex/calc/fees.go#L7-L15
[dex:dex/networks/dcr/script.go:155-181]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/dex/networks/dcr/script.go#L155-L181
[dex:dex/networks/dcr/script.go:50-53]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/dex/networks/dcr/script.go#L50-L53
[dex:dex/networks/dcr/script.go:67]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/dex/networks/dcr/script.go#L67
[dex:dex/networks/dcr/script.go:84-87]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/dex/networks/dcr/script.go#L84-L87
[dex:dex/networks/dcr/script.go:90]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/dex/networks/dcr/script.go#L90
[dex:server/asset/dcr/dcr.go:1067-1073]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/server/asset/dcr/dcr.go#L1067-L1073
[dex:server/asset/dcr/dcr.go:412-425]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/server/asset/dcr/dcr.go#L412-L425
[dex:server/asset/dcr/dcr.go:822-856]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/server/asset/dcr/dcr.go#L822-L856
[dex:server/market/market.go:2315-2316]: https://github.com/monetarium/monetarium-dex/blob/9521e9af8344064a3051e40a109518d8e94eba72/server/market/market.go#L2315-L2316
[node:chaincfg/mainnetparams.go:318-319]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/chaincfg/mainnetparams.go#L318-L319
[node:chaincfg/mainnetparams.go:526-536]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/chaincfg/mainnetparams.go#L526-L536
[node:chaincfg/simnetparams.go:545-559]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/chaincfg/simnetparams.go#L545-L559
[node:chaincfg/simnetparams.go:653-668]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/chaincfg/simnetparams.go#L653-L668
[node:chaincfg/testnetparams.go:542-545]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/chaincfg/testnetparams.go#L542-L545
[node:config.go:77]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/config.go#L77
[node:internal/blockalloc/allocator.go:118-120]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/blockalloc/allocator.go#L118-L120
[node:internal/blockalloc/allocator.go:172-179]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/blockalloc/allocator.go#L172-L179
[node:internal/blockchain/scriptval.go:267]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/blockchain/scriptval.go#L267
[node:internal/blockchain/ska_emission.go:42-45]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/blockchain/ska_emission.go#L42-L45
[node:internal/blockchain/validate.go:3933-3975]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/blockchain/validate.go#L3933-L3975
[node:internal/fees/feecalc.go:134]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/fees/feecalc.go#L134
[node:internal/fees/feecalc.go:313-317]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/fees/feecalc.go#L313-L317
[node:internal/fees/feecalc.go:693-696]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/fees/feecalc.go#L693-L696
[node:internal/mempool/mempool.go:1055-1058]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mempool/mempool.go#L1055-L1058
[node:internal/mempool/mempool.go:1894-1897]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mempool/mempool.go#L1894-L1897
[node:internal/mempool/mempool.go:1907]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mempool/mempool.go#L1907
[node:internal/mempool/policy.go:312-326]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mempool/policy.go#L312-L326
[node:internal/mempool/policy.go:361-363]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mempool/policy.go#L361-L363
[node:internal/mining/mining.go:2603]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mining/mining.go#L2603
[node:internal/mining/mining.go:880-906]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mining/mining.go#L880-L906
[node:internal/mining/mining.go:2458-2471]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mining/mining.go#L2458-L2471
[node:internal/mining/txpriorityqueue.go:103-134]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/mining/txpriorityqueue.go#L103-L134
[node:internal/rpcserver/rpcserver.go:1141-1150]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/rpcserver/rpcserver.go#L1141-L1150
[node:internal/rpcserver/rpcserver.go:1569-1578]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/internal/rpcserver/rpcserver.go#L1569-L1578
[node:txscript/sighash.go:393-399]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/txscript/sighash.go#L393-L399
[node:txscript/sighash.go:425-432]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/txscript/sighash.go#L425-L432
[node:wire/msgtx.go:1785]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/wire/msgtx.go#L1785
[node:wire/msgtx.go:1844-1850]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/wire/msgtx.go#L1844-L1850
[node:wire/msgtx.go:513-526]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/wire/msgtx.go#L513-L526
[node:wire/msgtx.go:553-570]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/wire/msgtx.go#L553-L570
[node:wire/msgtx.go:610-617]: https://github.com/monetarium/monetarium-node/blob/e1b5d1700798ce63f252f39354432a146f8e429c/wire/msgtx.go#L610-L617
[wallet-v1.3.14:wallet/chainntfns.go:731-768]: https://github.com/monetarium/monetarium-wallet/blob/8a5621869e7399572e19ed304f1c6bf50c6ed804/wallet/chainntfns.go#L731-L768
[wallet:wallet/chainntfns.go:760-785]: https://github.com/monetarium/monetarium-wallet/blob/42aa5102b0a29e9e741cad6532d8fce749f120fa/wallet/chainntfns.go#L760-L785
[wallet:wallet/createtx.go:1810-1828]: https://github.com/monetarium/monetarium-wallet/blob/42aa5102b0a29e9e741cad6532d8fce749f120fa/wallet/createtx.go#L1810-L1828
[wallet:wallet/createtx.go:2224]: https://github.com/monetarium/monetarium-wallet/blob/42aa5102b0a29e9e741cad6532d8fce749f120fa/wallet/createtx.go#L2224
[wallet:spv/backend.go:482-500]: https://github.com/monetarium/monetarium-wallet/blob/42aa5102b0a29e9e741cad6532d8fce749f120fa/spv/backend.go#L482-L500
[wallet:wallet/wallet.go:4562-4568]: https://github.com/monetarium/monetarium-wallet/blob/42aa5102b0a29e9e741cad6532d8fce749f120fa/wallet/wallet.go#L4562-L4568
[wallet:wallet/wallet.go:4690-4697]: https://github.com/monetarium/monetarium-wallet/blob/42aa5102b0a29e9e741cad6532d8fce749f120fa/wallet/wallet.go#L4690-L4697
