#!/usr/bin/env bash
# Checks a running VAR harness (harness.sh): the chain is past the SKA1 emission's
# maturity, the trading wallets hold the budget, alpha has its tx index and the
# server_fees account, beta follows alpha, and 20 further blocks are mined, each
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

# Budget per trading wallet, from harness.sh: 500 VAR in the mix plus 300 x 0.01 VAR,
# and 4 x 1000 SKA1.
VAR_BUDGET=503
SKA1_BUDGET=4000
for W in trading1 trading2; do
  VAR=$(./$W getbalance default 1 0 | jq -r '.balances[0].spendable')
  SKA1=$(./$W getbalance default 1 1 | jq -r '.balances[0].spendable')
  [ "$(echo "${VAR} == ${VAR_BUDGET}" | bc)" = 1 ] || fail "$W VAR ${VAR}, want ${VAR_BUDGET}"
  [ "$(echo "${SKA1} == ${SKA1_BUDGET}" | bc)" = 1 ] || fail "$W SKA1 ${SKA1}, want ${SKA1_BUDGET}"
  # The shape: 300 small VAR outputs, and 4 SKA1 outputs each above the largest VAR one.
  SMALL=$(./$W listunspent 1 9999999 '[]' '' 0 | jq '[.[] | select((.amount | tonumber) == 0.01)] | length')
  [ "${SMALL}" = 300 ] || fail "$W has ${SMALL} outputs of 0.01 VAR, want 300"
  MAX_VAR=$(./$W listunspent 1 9999999 '[]' '' 0 | jq '[.[].amount | tonumber] | max')
  SKA1_OUTS=$(./$W listunspent 1 9999999 '[]' '' 1 | jq -c '[.[].amount | tonumber]')
  [ "$(echo "${SKA1_OUTS}" | jq length)" = 4 ] || fail "$W has SKA1 outputs ${SKA1_OUTS}, want 4"
  [ "$(echo "${SKA1_OUTS}" | jq --argjson m "${MAX_VAR}" 'all(. > $m)')" = true ] ||
    fail "$W SKA1 outputs ${SKA1_OUTS} not all above the largest VAR output ${MAX_VAR}"
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
