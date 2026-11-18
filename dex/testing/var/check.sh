#!/usr/bin/env bash
# Checks a running VAR harness (harness.sh): the chain is past the SKA1 emission's
# maturity, the trading wallets and spvwallet hold the budget, spvwallet syncs over
# SPV, alpha has its tx index and the server_fees account, beta follows alpha, the
# node refuses the four negative controls, and 20 further blocks are mined, each
# with all 5 votes.
set -e
cd ~/dextest/var/harness-ctl

FAIL=0
fail() { echo "FAIL: $*"; FAIL=1; }

# harness.sh mines to 151, mines the emission (152 at the earliest), mines 16 for its
# maturity and 2 after funding.
MIN_HEIGHT=170
START=$(./alpha_node getblockcount)
[ "${START}" -ge ${MIN_HEIGHT} ] || fail "height ${START}, want at least ${MIN_HEIGHT}"

# Budget per wallet, from harness.sh: 500 VAR in the mix plus 300 x 0.01 VAR (none
# for spvwallet), and 4 x 1000 + 5 SKA1.
SKA1_BUDGET=4005
for WB in trading1:503:300 trading2:503:300 spvwallet:500:0; do
  IFS=: read -r W VAR_BUDGET SMALL_BUDGET <<<"${WB}"
  VAR=$(./$W getbalance default 1 0 | jq -r '.balances[0].spendable')
  SKA1=$(./$W getbalance default 1 1 | jq -r '.balances[0].spendable')
  [ "$(echo "${VAR} == ${VAR_BUDGET}" | bc)" = 1 ] || fail "$W VAR ${VAR}, want ${VAR_BUDGET}"
  [ "$(echo "${SKA1} == ${SKA1_BUDGET}" | bc)" = 1 ] || fail "$W SKA1 ${SKA1}, want ${SKA1_BUDGET}"
  # The shape: the small VAR outputs, and 5 SKA1 outputs, each above the largest VAR one
  # in atoms; at least one also above it in coins, and at least one below 2^63 atoms,
  # which a driver parsing amounts at 1e18 atoms per coin into an int64 reads whole.
  # Amounts stay strings, compared in bc.
  SMALL=$(./$W listunspent 1 9999999 '[]' '' 0 | jq '[.[] | select((.amount | tonumber) == 0.01)] | length')
  [ "${SMALL}" = "${SMALL_BUDGET}" ] || fail "$W has ${SMALL} outputs of 0.01 VAR, want ${SMALL_BUDGET}"
  MAX_VAR=$(./$W listunspent 1 9999999 '[]' '' 0 | jq -r '.[].amount' | sort -g | tail -1)
  SKA1_OUTS=$(./$W listunspent 1 9999999 '[]' '' 1 | jq -r '[.[].amount] | join(" ")')
  [ "$(wc -w <<<"${SKA1_OUTS}")" -eq 5 ] || fail "$W has SKA1 outputs ${SKA1_OUTS}, want 5"
  ABOVE_IN_COINS=0 IN_INT64=0
  for A in ${SKA1_OUTS}; do
    [ "$(echo "${A} * 10^18 > ${MAX_VAR} * 10^8" | bc)" = 1 ] ||
      fail "$W SKA1 output ${A} not above the largest VAR output ${MAX_VAR} in atoms"
    [ "$(echo "${A} > ${MAX_VAR}" | bc)" = 1 ] && ABOVE_IN_COINS=1
    [ "$(echo "${A} * 10^18 < 2^63" | bc)" = 1 ] && IN_INT64=1
  done
  [ ${ABOVE_IN_COINS} = 1 ] || fail "$W has no SKA1 output above the largest VAR output ${MAX_VAR} in coins"
  [ ${IN_INT64} = 1 ] || fail "$W has no SKA1 output below 2^63 atoms: a driver parsing listunspent's amount at 1e18 atoms per coin into an int64 overflows on every one"
  echo "$W: ${VAR} VAR, ${SKA1} SKA1"
done

# ./fund, which the trade tests use: alpha pays itself, so the budget stays as is.
FUND_TX=$(./fund "$(./alpha getnewaddress)" 1 | head -1)
[ "$(./alpha gettransaction "${FUND_TX}" | jq .confirmations)" -ge 1 ] 2>/dev/null ||
  fail "./fund transaction ${FUND_TX} is not mined"

# alpha serves the dcrdex server: its node needs the tx index to find a mined
# transaction by txid, and its wallet the server_fees account for the regXPub.
TX=$(./trading1 listunspent | jq -r '.[0].txid')
./alpha_node getrawtransaction "${TX}" >/dev/null 2>&1 || fail "alpha_node cannot find confirmed tx ${TX}: no --txindex?"
./alpha getmasterpubkey server_fees >/dev/null 2>&1 || fail "alpha has no server_fees account"
# beta follows alpha.
[ "$(./beta_node getbestblockhash 2>/dev/null)" = "$(./alpha_node getbestblockhash)" ] ||
  fail "beta_node is not on alpha's tip"
# spvwallet syncs over SPV from both nodes.
[ "$(./spvwallet walletinfo | jq .spv)" = true ] || fail "spvwallet is not in SPV mode"
PEERS=$(./spvwallet getpeerinfo | jq length)
[ "${PEERS}" = 2 ] || fail "spvwallet has ${PEERS} SPV peers, want 2"

# Negative controls. Each transaction is signed by alpha, spends its own input and is
# valid but for one defect, is sent to alpha's node, and must fail with the node's
# error for that defect. The dust and size controls fail only because the nodes run
# with --rejectnonstd. Inputs are 4 of alpha's VAR outputs, locked so that ticket
# purchases cannot spend them.
INS=$(./alpha listunspent 1 9999999 '[]' '' 0 |
  jq -c '[.[] | select(.spendable and (.amount | tonumber) >= 2) | {txid, vout, tree, amount}] | .[:4]')
[ "$(jq length <<<"${INS}")" = 4 ] || fail "alpha has fewer than 4 VAR outputs of 2 VAR or more"
./alpha lockunspent false "$(jq -c 'map({txid, vout, tree})' <<<"${INS}")" >/dev/null
U0=$(jq -c '.[0]' <<<"${INS}") U1=$(jq -c '.[1]' <<<"${INS}")
U2=$(jq -c '.[2]' <<<"${INS}") U3=$(jq -c '.[3]' <<<"${INS}")
A1=$(./alpha getnewaddress) A2=$(./alpha getnewaddress)
atoms() { jq '.amount | tonumber * 1e8 | round' <<<"$1"; }
var() { jq -n "$1 / 1e8"; }
# Hex goes through stdin ('-'): the oversized one exceeds a Linux argument's length limit.
create() { # create <utxo> <outputs> [locktime]
  ./alpha_node createrawtransaction "$(jq -c '[{txid, vout, tree}]' <<<"$1")" "$2" ${3:+"$3"}
}
sign() { ./alpha signrawtransaction - | jq -r 'if .complete then .hex else error("unsigned") end'; }
send() { ./alpha_node sendrawtransaction - 2>&1; }
refused() { # refused <desc> <pattern> <node output>: the send failed with <pattern>
  grep -q "$2" <<<"$3" || fail "$1: want the node's '$2', got: $3"
}
# paying <utxo> <fee atoms> [locktime]: a signed one-output tx from <utxo> paying <fee>.
# A past locktime changes only the signature.
paying() { create "$1" "{\"${A1}\":$(var $(($(atoms "$1") - $2)))}" ${3:+"$3"} | sign; }
sized() { # sized <utxo> <fee atoms> <bytes>: paying, at a locktime that makes it <bytes> long
  local h k
  # Timestamp locktimes (from 500000000), all in the past: a rare signature length can
  # take hundreds of tries, more than heights below the tip allow.
  for ((k = 500000001; k <= 500000400; k++)); do
    h=$(paying "$1" "$2" "$k")
    [ $((${#h} / 2)) = "$3" ] && { echo "${h}"; return; }
  done
  echo "no signature gives a ${3}-byte transaction" >&2; return 1
}
# min_of: "<fee> <minimum>" from the node's insufficient-fee error
min_of() { sed -n 's/.*insufficient fee for coin type 0: \([0-9]*\) < \([0-9]*\) atoms.*/\1 \2/p'; }

# 1. A dust output of 1000 atoms; the fee is ample.
OUT=$(create "${U0}" "{\"${A1}\":$(var $(($(atoms "${U0}") - 1000 - 100000))),\"${A2}\":$(var 1000)}" |
  sign | send) && fail "dust output accepted: ${OUT}"
refused "dust output" "payment of 1000 is dust" "${OUT}"

# 2. A fee 1 atom below the minimum the node states is refused, the minimum itself is
# accepted. The node's minimum follows the size, and a new signature can be a byte
# longer or shorter, so both are signed at the size of the probe that read the minimum.
H=$(paying "${U1}" 1 0)
SIZE=$((${#H} / 2))
read -r _ MIN <<<"$(echo "${H}" | send | min_of)" || true
if [ -n "${MIN:-}" ]; then
  OUT=$(sized "${U1}" $((MIN - 1)) "${SIZE}" | send) && fail "fee 1 atom below the minimum ${MIN} accepted"
  refused "fee 1 atom below the minimum" "insufficient fee for coin type 0: $((MIN - 1)) < ${MIN} atoms" "${OUT}"
  OUT=$(sized "${U1}" "${MIN}" "${SIZE}" | send) || fail "fee at the node's minimum ${MIN} refused: ${OUT}"
else
  fail "a fee of 1 atom was not refused for the fee"
fi

# 3. A fee of 1 VAR, above the ceiling, with high fees not allowed.
OUT=$(paying "${U2}" 100000000 | send) && fail "fee above the ceiling accepted: ${OUT}"
refused "fee above the ceiling" "fee too high for coin type 0" "${OUT}"

# 4. Over 100 000 bytes: U3 split into 3000 P2SH outputs of 35 bytes. P2SH outputs
# count no sigops, so the node's 1000-sigop cap, which --rejectnonstd does not gate,
# stays out of the way. createrawtransaction takes one output per address, so the hex
# repeats the output of a 1-output template; a 2-output template gives its length. The
# prefix before the output count is version (4 bytes), input count (1) and the input (41).
N=3000
P1=$(./alpha createmultisig 1 "[\"${A1}\"]" | jq -r .address)
P2=$(./alpha createmultisig 1 "[\"${A2}\"]" | jq -r .address)
EACH=$(var $((($(atoms "${U3}") - 5000000) / N)))
T1=$(create "${U3}" "{\"${P1}\":${EACH}}")
T2=$(create "${U3}" "{\"${P1}\":${EACH},\"${P2}\":${EACH}}")
L=$((${#T2} - ${#T1}))
if [ "${T1:92:2}" = 01 ] && [ "${T2:92:2}" = 02 ]; then
  BIG="${T1:0:92}fd$(printf '%02x%02x' $((N & 255)) $((N >> 8)))$(printf "%.0s${T1:94:L}" $(seq $N))${T1:$((94 + L))}"
  OUT=$(echo "${BIG}" | sign | send) && fail "transaction over 100 000 bytes accepted"
  refused "transaction over 100 000 bytes" "is larger than max allowed size of 100000" "${OUT}"
else
  fail "unexpected createrawtransaction layout: ${T1:0:100}"
fi
# U1 is spent by the fee control's accepted transaction.
./alpha lockunspent true "$(jq -c '[.[0], .[2], .[3]] | map({txid, vout, tree})' <<<"${INS}")" >/dev/null

# Consensus already rejects a block with fewer than 3 votes, so check for all 5:
# alpha owns every ticket and mine-alpha waits for all of its votes.
START=$(./alpha_node getblockcount)
echo "height ${START}; mining 20 blocks"
./mine-alpha 20
END=$(./alpha_node getblockcount)
[ "${END}" -ge $((START + 20)) ] || fail "height ${END} after mining 20 from ${START}"
for H in $(seq $((START + 1)) "${END}"); do
  VOTERS=$(./alpha_node getblockheader "$(./alpha_node getblockhash "$H")" | jq .voters)
  [ "${VOTERS}" = 5 ] || fail "block $H has ${VOTERS} votes"
done

[ ${FAIL} = 0 ] && echo "PASS at height ${END}" || { echo "check.sh failed"; exit 1; }
