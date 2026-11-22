#!/usr/bin/env bash
# Tmux script that sets up a Monetarium (VAR) simnet harness. Layout follows
# dex/testing/dcr/harness.sh, so the trade tests and the dcrdex harness find it the
# same way. Monetarium simnet has no premine, so the chain is mined from genesis.
#
# Binaries: monetarium-node, monetarium-wallet and monetarium-ctl from VAR_BIN, which is
# required. The wallet must be monetarium-wallet main at 42aa5102 or later (see
# docs/var-port/plan.md, Versions).
set -e

SESSION="var-harness"
RPC_USER="user"
RPC_PASS="pass"
WALLET_PASS=abc

ALPHA_NODE_PORT="19660"
ALPHA_NODE_RPC_PORT="19661"
ALPHA_WALLET_RPC_PORT="19662"
BETA_NODE_PORT="19670"
BETA_NODE_RPC_PORT="19671"
TRADING_WALLET1_PORT="19681"
TRADING_WALLET2_PORT="19682"
SPV_WALLET_PORT="19683"

# The harness seed: it owns the mining address and the SKA1 emission address
# (simnet sends the whole SKA1 emission to SsWKp7wtdTZYabYFYSc9cnxhwFEjA5g4pFc).
ALPHA_WALLET_SEED="b280922d2cffda44648346412c5ec97f429938105003730414f10b01e1402eac"
ALPHA_MINING_ADDR="SsXciQNTo3HuV5tX3yy4hXndRWgLMRVC7Ah"
TRADING_WALLET1_SEED="31cc0eeb220aa5b1f1ab3b6c47529d737976af1a556b156a665408f1711c962f"
TRADING_WALLET2_SEED="3db72efa55b9e6cce9c27dde9bea848c6199004f9b1ae2add3b04389495edb9c"
SPV_WALLET_SEED="8a3f1e5c7b2d4906e1f8a7c3b5d2e4f60718293a4b5c6d7e8f90a1b2c3d4e5f6"

# Simnet SKA1 emission key (the private key is 3; its public key is in the node's
# simnet params). The emission window is heights 150-250.
SKA1_EMISSION_PRIV="0000000000000000000000000000000000000000000000000000000000000003"
SKA1_EMISSION_HEIGHT=150

# Budget per trading wallet: 500 VAR in the DCR harness's mix of amounts, 300
# outputs of 0.01 VAR, and SKA1 outputs larger than the largest VAR output: 1000 SKA1
# is larger in coins (a driver that reads the amount as VAR sees 1000 VAR), and 5 SKA1
# in atoms (a driver that parses the amount at 1e18 atoms per coin reads 5e18, while
# 1000 SKA1, 1e21 atoms, does not fit in 64 bits).
# spvwallet gets the same, without the small outputs.
VAR_MIX="10 18 5 7 1 15 3 25 24 24 24 24 24 24 24 24 24 24 24 24 24 24 24 24 24 8"
SMALL_OUTPUTS=300
SKA1_OUTPUTS="1000 1000 1000 1000 5"
# alpha must hold at least this much spendable VAR before it distributes.
ALPHA_VAR_NEEDED=1600

NODES_ROOT=~/dextest/var

# No PATH fallback: an installed release wallet lacks fixes the invariant tests need,
# and its version string cannot show that.
[ -n "${VAR_BIN:-}" ] || { echo "set VAR_BIN to the directory with the pinned binaries" >&2; exit 1; }
VAR_BIN=$(cd "${VAR_BIN}" && pwd) # absolute, so tmux windows do not depend on the cwd
NODE="${VAR_BIN}/monetarium-node"
WALLET="${VAR_BIN}/monetarium-wallet"
CTL="${VAR_BIN}/monetarium-ctl"
for b in "$NODE" "$WALLET" "$CTL"; do
  [ -x "$b" ] || { echo "missing binary: $b" >&2; exit 1; }
done
for t in jq bc tmux; do command -v $t >/dev/null || { echo "$t is required" >&2; exit 1; }; done
echo "node:   $("$NODE" --version)"
echo "wallet: $("$WALLET" --version)"
echo "ctl:    $("$CTL" --version)"
# The commit this run's log belongs to, "dirty" when the harness scripts differ from it.
if REV=$(git -C "$(dirname "$0")" rev-parse --short HEAD 2>/dev/null); then
  git -C "$(dirname "$0")" diff --quiet HEAD -- . || REV="${REV} dirty"
else
  REV=unknown # no git, or not a checkout
fi
echo "harness: ${REV}"

SHELL=$(command -v bash)
export SHELL

if tmux has-session -t ${SESSION} 2>/dev/null; then
  echo "harness already running: stop it with ${NODES_ROOT}/harness-ctl/quit" >&2; exit 1
fi
if [ -d "${NODES_ROOT}" ]; then
  rm -fR "${NODES_ROOT}"
fi
mkdir -p "${NODES_ROOT}/alpha" "${NODES_ROOT}/beta" "${NODES_ROOT}/harness-ctl"
HCTL="${NODES_ROOT}/harness-ctl"

echo "Writing ctl scripts and configs"
################################################################################
# Node ctl configs and scripts
################################################################################

for N in alpha:${ALPHA_NODE_RPC_PORT} beta:${BETA_NODE_RPC_PORT}; do
  NAME=${N%%:*} PORT=${N#*:}
  cat > "${NODES_ROOT}/${NAME}/${NAME}_node-ctl.conf" <<EOF
rpcuser=${RPC_USER}
rpcpass=${RPC_PASS}
rpccert=${NODES_ROOT}/${NAME}/rpc.cert
rpcserver=127.0.0.1:${PORT}
EOF
done

# ./alpha is the alpha wallet (node calls pass through to the alpha node), as in
# the DCR harness. ./alpha_node and ./beta_node are the nodes.
cat > "${HCTL}/alpha_node" <<EOF
#!/usr/bin/env bash
"${CTL}" -C "${NODES_ROOT}/alpha/alpha_node-ctl.conf" "\$@"
EOF
cat > "${HCTL}/beta_node" <<EOF
#!/usr/bin/env bash
"${CTL}" -C "${NODES_ROOT}/beta/beta_node-ctl.conf" "\$@"
EOF

# Past height 143 a block waits for all 5 votes on the tip: simnet needs 3 of 5 from
# stake validation height 144, and alpha owns every ticket. Waiting for 3 would leave
# late votes in the mempool, where they count toward the next block's 3 but are stale. From height 34 alpha buys 5 tickets per block
# while it holds fewer than the target pool, 320 (pool size 64 x 5 per block).
# The wallet's ticket buyer is off: it grows the pool far past the target, and
# the ticket price with it, until most VAR is locked in tickets.
cat > "${HCTL}/mine-alpha" <<'EOF'
#!/usr/bin/env bash
set -e
cd "$(dirname "$0")"
NUM=1
case $1 in
  ''|*[!0-9]*) ;;
  *) NUM=$1 ;;
esac
waitfor() { # waitfor <desc> <cmd...>: retry for 60s
  local desc=$1; shift
  for _ in $(seq 120); do "$@" >/dev/null 2>&1 && return 0; sleep 0.5; done
  echo "mine-alpha: timeout: $desc" >&2; exit 1
}
votes_ready() { [ "$(./alpha_node getrawmempool false votes | jq length)" -ge 5 ]; }
wallet_on() { [ "$(./alpha getbestblock | jq -r .hash)" = "$1" ]; }
LOCKED= # this mine-alpha holds the load lock
unlock() { if [ -n "$LOCKED" ]; then rmdir ../load/lock; LOCKED=; fi; }
trap unlock EXIT
# Count heights, not generate calls: a block can be replaced at the same height,
# e.g. when the background miner races another mine-alpha.
TARGET=$(( $(./alpha_node getblockcount) + NUM ))
while HEIGHT=$(./alpha_node getblockcount) && [ "$HEIGHT" -lt "$TARGET" ]; do
  # Without purchases the node stops building blocks, so a failure is shown, not hidden.
  if [ "$HEIGHT" -ge 34 ] && [ "$(./alpha getstakeinfo | jq '(.live // 0) + (.immature // 0)')" -lt 320 ] &&
    ! ./alpha purchaseticket default 999999 1 5 >/dev/null; then
    echo "mine-alpha: purchaseticket failed at height $HEIGHT" >&2
  fi
  if [ "$HEIGHT" -ge 143 ]; then waitfor "5 votes at height $HEIGHT" votes_ready; fi
  # SKA1 and VAR load before the block, while load.sh is on (plan.md, step 9). One
  # loaded block at a time: another mine-alpha (the background miner) would mine
  # between this step and this block, from a mempool this step did not top up.
  if [ -e ../load/on ]; then
    waitfor "the load lock" mkdir ../load/lock
    LOCKED=1
    if [ "$(./alpha_node getblockcount)" != "$HEIGHT" ]; then unlock; continue; fi
    ./load step
  fi
  ./alpha_node regentemplate >/dev/null
  sleep 0.05
  OK=1
  OUT=$(./alpha_node generate 1 2>&1) || OK=
  unlock
  if [ -z "$OK" ]; then
    # Another mine-alpha (the background miner) is mid-block: go round again.
    case $OUT in *"already discrete mining"*) sleep 0.5; continue ;; esac
    echo "$OUT" >&2; exit 1
  fi
  # the alpha wallet must see the new tip before it can vote on it
  waitfor "alpha wallet on tip" wallet_on "$(./alpha_node getbestblockhash)"
done
EOF

# ./load on|off|step|check|report|sweep: dex/testing/var/load.sh, which mine-alpha runs while it is on.
cat > "${HCTL}/load" <<EOF
#!/usr/bin/env bash
"$(cd "$(dirname "$0")" && pwd)/load.sh" "\$@"
EOF

cat > "${HCTL}/fund" <<EOF
#!/usr/bin/env bash
./alpha sendtoaddress \$@
sleep 0.5
./mine-alpha 1
EOF

cat > "${HCTL}/quit" <<EOF
#!/usr/bin/env bash
for w in miner w-alpha w-trading1 w-trading2 w-spvwallet; do tmux send-keys -t $SESSION:\$w C-c 2>/dev/null; done
sleep 0.5
tmux send-keys -t $SESSION:alpha C-c
tmux send-keys -t $SESSION:beta C-c
tmux wait-for alphavar
tmux wait-for betavar
tmux kill-session -t $SESSION
EOF

# Alpha node config, for the dcrdex server's markets.json.
cat > "${NODES_ROOT}/alpha/alpha_node.conf" <<EOF
rpcuser=${RPC_USER}
rpcpass=${RPC_PASS}
rpccert=${NODES_ROOT}/alpha/rpc.cert
rpclisten=127.0.0.1:${ALPHA_NODE_RPC_PORT}
EOF

################################################################################
# Wallets
################################################################################

# write_wallet <name> <rpc port> [extra conf lines...]: <name>/<name>.conf, the file
# the trade tests read, its ctl config and the harness-ctl/<name> script. The extra
# lines set how it syncs: RPC_SYNC or SPV_SYNC.
write_wallet() {
  local NAME=$1 PORT=$2; shift 2
  local DIR="${NODES_ROOT}/${NAME}"
  mkdir -p "${DIR}"
  cat > "${DIR}/${NAME}.conf" <<EOF
simnet=1
nogrpc=1
appdata=${DIR}
logdir=${DIR}/log
debuglevel=debug
username=${RPC_USER}
password=${RPC_PASS}
rpclisten=127.0.0.1:${PORT}
rpccert=${DIR}/rpc.cert
pass=${WALLET_PASS}
EOF
  for line in "$@"; do echo "$line" >> "${DIR}/${NAME}.conf"; done
  cat > "${DIR}/${NAME}-ctl.conf" <<EOF
rpcuser=${RPC_USER}
rpcpass=${RPC_PASS}
rpccert=${DIR}/rpc.cert
rpcserver=127.0.0.1:${PORT}
EOF
  cat > "${HCTL}/${NAME}" <<EOF
#!/usr/bin/env bash
"${CTL}" -C "${DIR}/${NAME}-ctl.conf" --wallet "\$@"
EOF
}

RPC_SYNC=("rpcconnect=127.0.0.1:${ALPHA_NODE_RPC_PORT}" "cafile=${NODES_ROOT}/alpha/rpc.cert")
SPV_SYNC=(spv=1 "spvconnect=127.0.0.1:${ALPHA_NODE_PORT}" "spvconnect=127.0.0.1:${BETA_NODE_PORT}")
write_wallet alpha ${ALPHA_WALLET_RPC_PORT} "${RPC_SYNC[@]}" enablevoting=1
# The many-small-outputs budget needs 300 fresh addresses, past the default gap limit.
write_wallet trading1 ${TRADING_WALLET1_PORT} "${RPC_SYNC[@]}" gaplimit=1000
write_wallet trading2 ${TRADING_WALLET2_PORT} "${RPC_SYNC[@]}" gaplimit=1000
# For the item 6 test, which checks the external wallet's own SPV publish path.
write_wallet spvwallet ${SPV_WALLET_PORT} "${SPV_SYNC[@]}"
chmod +x "${HCTL}"/*

waitfor() { # waitfor <desc> <cmd...>: retry for 60s
  local desc=$1; shift
  for _ in $(seq 120); do "$@" >/dev/null 2>&1 && return 0; sleep 0.5; done
  echo "timeout: $desc" >&2; exit 1
}

# create_wallet <name> <seed>: create from the seed, then run it in
# the window. Answers: use the configured private passphrase, no public
# passphrase, existing seed, the seed.
create_wallet() {
  local NAME=$1 SEED=$2 WIN="${SESSION}:w-$1" DIR="${NODES_ROOT}/$1"
  printf 'y\nn\ny\n%s\n' "${SEED}" | "${WALLET}" -C "${DIR}/${NAME}.conf" --create > "${DIR}/create.log" 2>&1 ||
    { cat "${DIR}/create.log" >&2; exit 1; }
  tmux new-window -t ${SESSION} -n "w-${NAME}" $SHELL
  tmux send-keys -t ${WIN} "cd ${DIR}" C-m
  tmux send-keys -t ${WIN} "\"${WALLET}\" -C ${NAME}.conf" C-m
  waitfor "${NAME} wallet rpc" "${HCTL}/${NAME}" getbestblock
}

################################################################################
# Start harness
################################################################################

SECONDS=0
echo "Starting harness"
tmux new-session -d -s $SESSION -n harness-ctl $SHELL
tmux send-keys -t $SESSION:harness-ctl "cd ${HCTL}" C-m

# --rejectnonstd: simnet accepts non-standard transactions by default, which skips
# the dust, size and non-standard-input checks that mainnet applies.
tmux new-window -t $SESSION -n alpha $SHELL
tmux send-keys -t $SESSION:alpha "cd ${NODES_ROOT}/alpha" C-m
tmux send-keys -t $SESSION:alpha "\"${NODE}\" --appdata=${NODES_ROOT}/alpha \
--rpcuser=${RPC_USER} --rpcpass=${RPC_PASS} \
--miningaddr=${ALPHA_MINING_ADDR} --rpclisten=127.0.0.1:${ALPHA_NODE_RPC_PORT} \
--txindex --rejectnonstd --listen=127.0.0.1:${ALPHA_NODE_PORT} \
--debuglevel=debug --whitelist=127.0.0.0/8 --whitelist=::1 \
--simnet; tmux wait-for -S alphavar" C-m

tmux new-window -t $SESSION -n beta $SHELL
tmux send-keys -t $SESSION:beta "cd ${NODES_ROOT}/beta" C-m
tmux send-keys -t $SESSION:beta "\"${NODE}\" --appdata=${NODES_ROOT}/beta \
--rpcuser=${RPC_USER} --rpcpass=${RPC_PASS} \
--listen=127.0.0.1:${BETA_NODE_PORT} --rpclisten=127.0.0.1:${BETA_NODE_RPC_PORT} \
--rejectnonstd --connect=127.0.0.1:${ALPHA_NODE_PORT} \
--debuglevel=debug --whitelist=127.0.0.0/8 --whitelist=::1 \
--simnet; tmux wait-for -S betavar" C-m

waitfor "alpha node rpc" "${HCTL}/alpha_node" getbestblockhash
waitfor "beta node rpc" "${HCTL}/beta_node" getbestblockhash

# The wallet's address discovery needs blocks 1 and 2 to exist when it starts.
"${HCTL}/alpha_node" generate 2 >/dev/null

echo "Creating wallets"
create_wallet alpha ${ALPHA_WALLET_SEED}
create_wallet trading1 ${TRADING_WALLET1_SEED}
create_wallet trading2 ${TRADING_WALLET2_SEED}
create_wallet spvwallet ${SPV_WALLET_SEED}

# Fee account on alpha for the dcrdex server's regXPub.
"${HCTL}/alpha" createnewaccount server_fees >/dev/null

cd "${HCTL}"
height() { ./alpha_node getblockcount; }

echo "Mining to height $((SKA1_EMISSION_HEIGHT + 1)) (votes from 144)"
./mine-alpha $((SKA1_EMISSION_HEIGHT + 1 - $(height)))

echo "Emitting SKA1 at height $(height)"
./alpha importemissionkey em ${SKA1_EMISSION_PRIV} ${WALLET_PASS} "" 1 >/dev/null
EMISSION_HEX=$(./alpha createauthorizedemission 1 em ${WALLET_PASS} | jq -r .transaction)
# The emission calls leave the wallet locked, which stops voting and ticket buying.
./alpha walletpassphrase ${WALLET_PASS} 0
EMISSION=$(./alpha_node sendrawtransaction "${EMISSION_HEX}")
for _ in 1 2 3 4; do
  ./mine-alpha 1
  ./alpha_node getrawmempool | grep -q "${EMISSION}" || break
done
if ./alpha_node getrawmempool | grep -q "${EMISSION}"; then
  echo "SKA1 emission ${EMISSION} not mined" >&2; exit 1
fi
echo "Mining past the emission's 16-block maturity"
./mine-alpha 16

# Simnet VAR halves every 128 blocks, so whether alpha can fund the budget is
# checked, not assumed.
alpha_var() { ./alpha getbalance default 1 0 | jq -r '.balances[0].spendable'; }
while [ "$(echo "$(alpha_var) < ${ALPHA_VAR_NEEDED}" | bc)" = 1 ]; do
  [ "$(height)" -lt 600 ] || { echo "alpha has $(alpha_var) VAR at height $(height)" >&2; exit 1; }
  ./mine-alpha 10
done
echo "alpha: $(alpha_var) VAR spendable at height $(height)"

# sendmany takes amounts as strings, and one output per address. All SKA1 outputs
# go in one transaction: alpha's SKA1 change stays unconfirmed until it is mined.
echo "Funding trading wallets"
T1=$(./trading1 getnewaddress)
T2=$(./trading2 getnewaddress)
S=$(./spvwallet getnewaddress)
for AMT in ${VAR_MIX}; do
  ./alpha sendmany default "{\"${T1}\":\"${AMT}\",\"${T2}\":\"${AMT}\",\"${S}\":\"${AMT}\"}" >/dev/null
done
SMALL="{"
SKA1="{"
for W in trading1 trading2; do
  for _ in $(seq ${SMALL_OUTPUTS}); do SMALL="${SMALL}\"$(./$W getnewaddress)\":\"0.01\","; done
done
for W in trading1 trading2 spvwallet; do
  for AMT in ${SKA1_OUTPUTS}; do SKA1="${SKA1}\"$(./$W getnewaddress)\":\"${AMT}\","; done
done
./alpha sendmany default "${SMALL%,}}" >/dev/null
./alpha sendmany default "${SKA1%,}}" 1 "" 1 >/dev/null
sleep 0.5
./mine-alpha 2
TIP=$(./alpha_node getbestblockhash)
for W in trading1 trading2 spvwallet; do
  waitfor "$W on tip" eval "[ \"\$(./$W getbestblock | jq -r .hash)\" = ${TIP} ]"
done

for W in trading1 trading2 spvwallet; do
  echo "$W: $(./$W getbalance default 1 0 | jq -r '.balances[0].spendable') VAR," \
    "$(./$W getbalance default 1 1 | jq -r '.balances[0].spendable') SKA1"
done
echo "Harness ready at height $(height) in ${SECONDS}s"

# Background miner, as in the DCR harness. 'export NOMINER=1' to disable.
if [ -z "$NOMINER" ]; then
  tmux new-window -t $SESSION -n miner $SHELL
  tmux send-keys -t $SESSION:miner "cd ${HCTL}" C-m
  # a plain loop: watch(1) is not installed on macOS by default
  tmux send-keys -t $SESSION:miner "while true; do ./mine-alpha 1; sleep 15; done" C-m
fi

tmux select-window -t $SESSION:harness-ctl
# No terminal (CI): leave the session running detached.
if [ -t 1 ]; then
  tmux attach-session -t $SESSION
fi
