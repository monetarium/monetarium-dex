#!/usr/bin/env bash
# SKA1 and VAR load on the VAR harness (docs/var-port/plan.md, step 9). While it is
# on, mine-alpha runs `load.sh step` before every block. A step tops alpha's mempool
# up with more SKA1 transactions than SKA1's share of the default 375 000-byte block
# (337 500 bytes) takes, and more VAR transactions than VAR's 37 500-byte share
# takes. The VAR ones pay alpha's estimatesmartfee rate, the rate the DEX uses.
#
#   load.sh on                start loading before every block
#   load.sh off               stop
#   load.sh step              top up once (mine-alpha runs it while the load is on)
#   load.sh check FROM [TO]   fail unless every block FROM..TO was mined under load:
#                             SKA1 load in it within one load transaction of 337 500
#                             bytes or above, and SKA1 and VAR load left over in the
#                             mempool after it
#   load.sh report FROM [TO [KIND...]]
#                             blocks from broadcast to confirmation for every VAR
#                             swap, redeem and refund mined in FROM..TO or still in
#                             the mempool; fail if one took or will take more than 3
#                             blocks, if there is none at all, or if a KIND given
#                             (swap, redeem, refund) has none; each with the rate it
#                             paid and the VAR load's rate at its broadcast
#   load.sh sweep on|off      while on, every step also sends one VAR transaction at
#                             each of SWEEP_PCTS percent of the VAR load's rate
#   load.sh sweep report      blocks from broadcast to confirmation of every sweep
#                             transaction, and a summary per percentage
#
# Outputs pay P2SH addresses from loadaddrs, whose scripts nobody holds. A P2SH
# output counts no signature operation; a P2PKH one counts one, and the 5000 a
# block allows are fewer than the outputs SKA1's share holds.
set -euo pipefail

HCTL=~/dextest/var/harness-ctl
DIR=~/dextest/var/load
SRC=$(cd "$(dirname "$0")" && pwd)

SKA1_SHARE=337500
SKA1_TARGET=450000 # SKA1 load kept in the mempool before each block
VAR_TARGET=50000   # VAR load kept in the mempool before each block
SKA1_OUTS=2000     # outputs per SKA1 transaction: about 72 000 bytes, under the 100 000 standard size
VAR_OUTS=100       # outputs per VAR transaction: about 3 400 bytes
# VAR per output: the wallet's dust limit for a P2SH output rises with the tx fee,
# about 600 atoms per atom/B, so 0.001 VAR holds up to about 160 atoms/B.
VAR_OUT_AMT=0.001
TEMPLATE_WAIT=1    # seconds for the node to rebuild its block template after a step
SKA1_COINS=50      # SKA1 coins alpha splits off for the load, 1e9 SKA1 each
MAX_BLOCKS=3       # report: most blocks from broadcast to confirmation
SWEEP_PCTS="50 75 90 95 100 105 110 125 150" # sweep: rates in percent of the load's
SWEEP_AMT=0.01     # sweep: VAR per transaction, one output
MIN_RATE=10000     # atoms/kB: the node's minimum relay fee; the sweep pays no less

alpha() { "${HCTL}/alpha" "$@"; }
node() { "${HCTL}/alpha_node" "$@"; }
die() { echo "load.sh: $*" >&2; exit 1; }

# kb <atoms/kB>: the rate as settxfee takes it, VAR/kB.
kb() { printf '%d.%08d' $(($1 / 100000000)) $(($1 % 100000000)); }

# paid: "atoms/B" a verbose VAR transaction on stdin paid, its fee over its size.
paid() { jq -r '(([.vin[].amountin] | add) - ([.vout[].value] | add)) * 1e8 / (.hex | length / 2) | . * 10 | round / 10'; }

# load_rate <height>: atoms/B the VAR load paid at the last step at that height, the
# estimate as read then. The estimate moves within a block (20-40 atoms/B seen).
load_rate() { awk -v h="$1" '$1 == h { e = $2 } END { if (e == "") print "?"; else printf "%.1f\n", e / 1000 }' "${DIR}/estimates"; }

# amounts <n> <amount>: sendmany's amounts object, n outputs of amount.
amounts() { head -n "$1" "${DIR}/addrs" | jq -R --arg a "$2" '{(.): $a}' | jq -s -c add; }

# send <coin> <n> <amount>: one load transaction, logged as "txid coin size". It
# spends confirmed coins only: chained on unconfirmed change, the wallet can pick a
# change output its previous send already spent, and refuse its own transaction
# as a double spend.
send() {
  local coin=$1 txid
  txid=$(amounts "$2" "$3" | alpha sendmany default - 1 "" "$coin")
  echo "${txid} ${coin} $(($(node getrawtransaction "${txid}" | wc -c) / 2))" >>"${DIR}/txs"
}

# load_bytes <coin>: bytes of that coin's load among the txids on stdin.
load_bytes() { awk -v c="$1" 'FILENAME == ARGV[1] { if ($2 == c) s[$1] = $3; next } $1 in s { t += s[$1] } END { print t + 0 }' "${DIR}/txs" -; }

step() {
  [ -e "${DIR}/on" ] || exit 0 # turned off while mine-alpha waited for the lock
  local tip mempool block fee ska_left var_left pct rate txid
  tip=$(node getblockcount)
  mempool=$(node getrawmempool true)
  # Entry heights of everything else in the mempool, for report.
  jq -r 'to_entries[] | "\(.key) \(.value.height)"' <<<"${mempool}" >>"${DIR}/seen"
  ska_left=$(jq -r 'keys[]' <<<"${mempool}" | load_bytes 1)
  var_left=$(jq -r 'keys[]' <<<"${mempool}" | load_bytes 0)
  block=$(node getblock "$(node getblockhash "${tip}")" | jq -r '.tx[]')
  echo "${tip} $(load_bytes 1 <<<"${block}") $(load_bytes 0 <<<"${block}") ${ska_left} ${var_left}" >>"${DIR}/blocks"
  while [ "${ska_left}" -lt "${SKA1_TARGET}" ]; do
    send 1 "${SKA1_OUTS}" 1
    ska_left=$((ska_left + $(tail -1 "${DIR}/txs" | cut -d' ' -f3)))
  done
  # The VAR load at the estimate; settxfee 0 then hands alpha's fee back to the wallet.
  fee=$(node estimatesmartfee 2 | jq -r .feerate) # atoms/kB
  echo "${tip} ${fee}" >>"${DIR}/estimates"
  alpha settxfee "$(kb "${fee}")" >/dev/null
  while [ "${var_left}" -lt "${VAR_TARGET}" ]; do
    send 0 "${VAR_OUTS}" "${VAR_OUT_AMT}"
    var_left=$((var_left + $(tail -1 "${DIR}/txs" | cut -d' ' -f3)))
  done
  # Under the load lock, as the sweep shares alpha's fee setting with the VAR load.
  if [ -e "${DIR}/sweep-on" ]; then
    for pct in ${SWEEP_PCTS}; do
      rate=$((fee * pct / 100))
      [ "${rate}" -ge "${MIN_RATE}" ] || rate=${MIN_RATE}
      alpha settxfee "$(kb "${rate}")" >/dev/null
      txid=$(amounts 1 "${SWEEP_AMT}" | alpha sendmany default - 1 "" 0)
      echo "${txid} ${pct} ${tip}" >>"${DIR}/sweep"
    done
  fi
  alpha settxfee 0 >/dev/null
  # The node builds templates in the background: with the mempool this full,
  # mine-alpha's own regentemplate and 50 ms left 3 blocks in 11 without this step's sends.
  node regentemplate >/dev/null
  sleep "${TEMPLATE_WAIT}"
}

# check FROM [TO]: reads the rows step wrote, "height ska1_mined var_mined ska1_left var_left".
check() {
  local from=$1 to=${2:-} h row tx fail=0
  # The SKA1 condition allows one load transaction under the share: the share is
  # not a multiple of their size.
  tx=$(awk '$2 == 1 && $3 > m { m = $3 } END { print m + 0 }' "${DIR}/txs")
  [ -n "${to}" ] || to=$(tail -1 "${DIR}/blocks" | cut -d' ' -f1)
  [ "${to:-0}" -ge "${from}" ] || die "no block from ${from} mined under load yet"
  for ((h = from; h <= to; h++)); do
    # The first step at a height: a second one (a retried block) measured after its top-up.
    row=$(awk -v h="$h" '$1 == h { print; exit }' "${DIR}/blocks")
    if [ -z "${row}" ]; then
      echo "block $h: not mined under load"; fail=1; continue
    fi
    read -r _ ska var ska_left var_left <<<"${row}"
    echo "block $h: SKA1 load ${ska} B, VAR load ${var} B; left over ${ska_left} B SKA1, ${var_left} B VAR"
    if [ $((ska + tx)) -le "${SKA1_SHARE}" ] || [ "${ska_left}" -eq 0 ] || [ "${var_left}" -eq 0 ]; then fail=1; fi
  done
  [ "${fail}" = 0 ] || die "blocks ${from}..${to} were not all mined under load"
}

# kind <verbose tx>: swap, redeem, refund or other. Redeems and refunds spend a
# contract (its script starts with OP_IF, 63) with OP_1 or OP_0 before it; a swap
# pays a P2SH output and, unlike a bond, has no OP_RETURN output.
kind() {
  jq -r '
    [.vin[].scriptSig.asm // "" | split(" ")] as $sigs
    | if any($sigs[]; length == 5 and .[3] == "1" and (.[4] | startswith("63"))) then "redeem"
      elif any($sigs[]; length == 4 and .[2] == "0" and (.[3] | startswith("63"))) then "refund"
      elif any(.vout[]; .scriptPubKey.type == "scripthash") and all(.vout[]; .scriptPubKey.type != "nulldata") then "swap"
      else "other" end'
}

report() {
  local from=$1 to=${2:-} tip h txid tx k entry at
  for k in "${@:3}"; do case $k in swap | redeem | refund) ;; *) die "unknown kind: $k" ;; esac; done
  tip=$(node getblockcount)
  [ -n "${to}" ] || to=${tip}
  # "txid height" of every regular transaction mined in FROM..TO, then "txid 0"
  # for the mempool; load and sweep transactions left out.
  for ((h = from; h <= to; h++)); do
    node getblock "$(node getblockhash "$h")" | jq -r --arg h "$h" '.tx[] | "\(.) \($h)"'
  done >"${DIR}/report-txs"
  node getrawmempool | jq -r '.[] | "\(.) 0"' >>"${DIR}/report-txs"
  cat "${DIR}/txs" "${DIR}/sweep" >"${DIR}/report-skip"
  awk 'FILENAME == ARGV[1] { skip[$1]; next } !($1 in skip)' "${DIR}/report-skip" "${DIR}/report-txs" | while read -r txid h; do
    tx=$(node getrawtransaction "${txid}" 1)
    [ "$(jq '.vout[0].cointype' <<<"${tx}")" = 0 ] || continue
    k=$(kind <<<"${tx}")
    [ "${k}" != other ] || continue
    entry=$(awk -v t="${txid}" '$1 == t { print $2; exit }' "${DIR}/seen")
    if [ "$h" = 0 ]; then
      # Judged whenever it entered: the next block, tip + 1, is its earliest.
      [ -n "${entry}" ] || entry=${tip} # entered after the last step
      at="$(paid <<<"${tx}") atoms/B (load at $(load_rate "${entry}"))"
      echo "${k} ${txid} at ${at} broadcast at ${entry}, unconfirmed at ${tip}"
      [ $((tip + 1 - entry)) -le "${MAX_BLOCKS}" ] || echo FAIL
    else
      # Not seen by a step: it entered after the last one, at the tip below its block.
      [ -n "${entry}" ] || entry="$((h - 1)) (not seen)"
      at="$(paid <<<"${tx}") atoms/B (load at $(load_rate "${entry%% *}"))"
      echo "${k} ${txid} at ${at} broadcast at ${entry}, mined at ${h}: $((h - ${entry%% *})) blocks"
      [ $((h - ${entry%% *})) -le "${MAX_BLOCKS}" ] || echo FAIL
    fi
    echo "count ${k}"
  done >"${DIR}/report"
  grep -v '^count \|^FAIL' "${DIR}/report" || true
  for k in swap redeem refund; do echo "${k}: $(grep -c "^count ${k}$" "${DIR}/report" || true)"; done
  ! grep -q '^FAIL' "${DIR}/report" || die "a swap, redeem or refund took or will take more than ${MAX_BLOCKS} blocks"
  grep -q '^count ' "${DIR}/report" || die "no swap, redeem or refund in blocks ${from}..${to}"
  for k in "${@:3}"; do grep -q "^count ${k}$" "${DIR}/report" || die "no ${k} in blocks ${from}..${to}"; done
}

# sweep_report: one line per sweep transaction, "pct txid rate load_rate entry mined
# blocks" (mined - for one still unconfirmed, blocks then counted to the tip), then per
# percentage the count, the mined ones' mean and most blocks, and the unconfirmed.
sweep_report() {
  local tip txid pct entry tx mined
  tip=$(node getblockcount)
  while read -r txid pct entry; do
    tx=$(node getrawtransaction "${txid}" 1)
    mined=$(jq -r '.blockheight // 0' <<<"${tx}")
    if [ "${mined}" -gt 0 ]; then
      echo "${pct} ${txid} $(paid <<<"${tx}") $(load_rate "${entry}") ${entry} ${mined} $((mined - entry))"
    else
      echo "${pct} ${txid} $(paid <<<"${tx}") $(load_rate "${entry}") ${entry} - $((tip - entry))"
    fi
  done <"${DIR}/sweep" | tee "${DIR}/sweep-report"
  echo "pct n mean max unconfirmed (blocks; unconfirmed counted to tip ${tip})"
  awk '{ n[$1]++; if ($6 == "-") { u[$1]++; next } m[$1]++; s[$1] += $7; if ($7 > x[$1]) x[$1] = $7 }
    END { for (p in n) printf "%s %d %.1f %d %d\n", p, n[p], m[p] ? s[p] / m[p] : 0, x[p], u[p] }' "${DIR}/sweep-report" | sort -n
}

case ${1:-} in
  on)
    mkdir -p "${DIR}"
    [ -s "${DIR}/addrs" ] || (cd "${SRC}/../../.." && go run ./dex/testing/var/loadaddrs "${SKA1_OUTS}") >"${DIR}/addrs"
    if [ ! -e "${DIR}/split" ]; then
      # SKA1 coins for confirmed-only sends: alpha holds a few large ones, and a
      # step makes about 5 SKA1 sends. Each mined load transaction gives one back.
      for ((i = 0; i < SKA1_COINS; i++)); do alpha getnewaddress default ignore; done |
        jq -R '{(.): "1000000000"}' | jq -s -c add | alpha sendmany default - 1 "" 1 >/dev/null
      "${HCTL}/mine-alpha" 1
      touch "${DIR}/split"
    fi
    touch "${DIR}/txs" "${DIR}/seen" "${DIR}/blocks" "${DIR}/estimates" "${DIR}/sweep" "${DIR}/on"
    ;;
  off)
    rm -f "${DIR}/on"
    ;;
  step) step ;;
  check) [ $# -ge 2 ] || die "usage: load.sh check FROM [TO]"; check "${@:2}" ;;
  report) [ $# -ge 2 ] || die "usage: load.sh report FROM [TO [KIND...]]"; report "${@:2}" ;;
  sweep)
    case ${2:-} in
      on) touch "${DIR}/sweep-on" ;;
      off) rm -f "${DIR}/sweep-on" ;;
      report) sweep_report ;;
      *) die "usage: load.sh sweep on|off|report" ;;
    esac
    ;;
  *) sed -n '2,/^set -euo/p' "$0" | sed '$d'; exit 2 ;;
esac
