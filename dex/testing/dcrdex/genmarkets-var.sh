#!/usr/bin/env bash
# Writes markets.json for the VAR markets only: var_btc, and var_ltc when the LTC
# harness runs. genmarkets.sh hands off here when the VAR harness runs, so the
# VAR runs need no DCR harness. The BTC harness must be running. NODERELAY is
# not supported: no asset entry here has a nodeRelayID.

TEST_ROOT=~/dextest
FILEPATH=${TEST_ROOT}/dcrdex/markets.json
EPOCH_DURATION=${EPOCH:-15000}
# 100 for the runs under SKA load (plan.md, step 9): the node's estimate is at
# least 15 atoms/B there, and swaps at 10 would pay less than the VAR load.
VAR_MAX_FEE_RATE=${VAR_MAX_FEE_RATE:-10}

~/dextest/ltc/harness-ctl/alpha getblockchaininfo &> /dev/null
LTC_ON=$?

echo "Writing markets.json with the VAR markets only"

# 1 VAR lots. The lowest rate the trade tests place is 100 rate steps, which must
# clear the server's minimum rate (quote dust per base lot): 47200 for BTC at its
# maxFeeRate of 100, 9440 for LTC at 20.
cat > "${FILEPATH}" <<EOF
{
    "markets": [
        {
            "base": "VAR_simnet",
            "quote": "BTC_simnet",
            "lotSize": 100000000,
            "rateStep": 1000,
            "epochDuration": ${EPOCH_DURATION},
            "marketBuyBuffer": 1.2,
            "parcelSize": 4
EOF

if [ $LTC_ON -eq 0 ]; then
    cat << EOF >> "${FILEPATH}"
        },
        {
            "base": "VAR_simnet",
            "quote": "LTC_simnet",
            "lotSize": 100000000,
            "rateStep": 1000,
            "epochDuration": ${EPOCH_DURATION},
            "marketBuyBuffer": 1.2,
            "parcelSize": 4
EOF
else echo "Litecoin is not running. Configuring dcrdex markets without LTC."
fi

# The regXPub is the alpha wallet's server_fees account; the VAR harness uses the
# DCR harness's alpha seed, so it equals the DCR entry's.
cat << EOF >> "${FILEPATH}"
        }
    ],
    "assets": {
        "VAR_simnet": {
            "bip44symbol": "var",
            "network": "simnet",
            "maxFeeRate": ${VAR_MAX_FEE_RATE},
            "swapConf": 1,
            "configPath": "${TEST_ROOT}/var/alpha/alpha_node.conf",
            "regXPub": "spubVWKGn9TGzyo7M4b5xubB5UV4joZ5HBMNBmMyGvYEaoZMkSxVG4opckpmQ26E85iHg8KQxrSVTdex56biddqtXBerG9xMN8Dvb3eNQVFFwpE",
            "bondAmt": 50000000,
            "bondConfs": 1
        },
        "BTC_simnet": {
            "bip44symbol": "btc",
            "network": "simnet",
            "maxFeeRate": 100,
            "swapConf": 1,
            "configPath": "${TEST_ROOT}/btc/alpha/alpha.conf",
            "bondAmt": 100000,
            "bondConfs": 1
EOF

if [ $LTC_ON -eq 0 ]; then
    cat << EOF >> "${FILEPATH}"
        },
        "LTC_simnet": {
            "bip44symbol": "ltc",
            "network": "simnet",
            "maxFeeRate": 20,
            "swapConf": 2,
            "configPath": "${TEST_ROOT}/ltc/alpha/alpha.conf",
            "bondAmt": 1000000,
            "bondConfs": 1
EOF
fi

cat << EOF >> "${FILEPATH}"
        }
    }
}
EOF
