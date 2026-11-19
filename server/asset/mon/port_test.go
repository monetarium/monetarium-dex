//go:build !dcrlive

// Tests for the values the port takes from the Monetarium node rather than
// from dcrd: config defaults, the RPC API version, and the registered driver.

package mon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"decred.org/dcrdex/dex"
	"decred.org/dcrdex/dex/calc"
	dexmon "decred.org/dcrdex/dex/networks/mon"
	"decred.org/dcrdex/server/asset"
	"github.com/monetarium/monetarium-node/dcrutil"
	chainjson "github.com/monetarium/monetarium-node/rpc/jsonrpc/types"
	"github.com/monetarium/monetarium-node/wire"
)

// TestNodeConfigDefaults checks loadConfig's defaults against the node's own:
// RPC ports 9509, 19509 and 19956, and its app data dir and config file.
func TestNodeConfigDefaults(t *testing.T) {
	old := chainParams // loadConfig sets it
	t.Cleanup(func() { chainParams = old })

	path := filepath.Join(t.TempDir(), "monetarium.conf")
	if err := os.WriteFile(path, []byte("rpcuser=u\nrpcpass=p\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nodeDir := dcrutil.AppDataDir("monetarium", false)
	for net, listen := range map[dex.Network]string{
		dex.Mainnet: "localhost:9509",
		dex.Testnet: "localhost:19509",
		dex.Simnet:  "localhost:19956",
	} {
		cfg, err := loadConfig(path, net)
		if err != nil {
			t.Fatalf("%s: %v", net, err)
		}
		if cfg.RPCListen != listen {
			t.Errorf("%s: default rpclisten %s, want %s", net, cfg.RPCListen, listen)
		}
		if want := filepath.Join(nodeDir, "rpc.cert"); cfg.RPCCert != want {
			t.Errorf("%s: default rpccert %s, want %s", net, cfg.RPCCert, want)
		}
	}
	if want := filepath.Join(nodeDir, "monetarium.conf"); defaultConfigPath != want {
		t.Errorf("default config path %s, want %s", defaultConfigPath, want)
	}
}

// TestCheckNodeVersion checks the node's JSON-RPC API key and version:
// monetarium-node v1.3.14 reports monetariumjsonrpcapi 8.3.0.
func TestCheckNodeVersion(t *testing.T) {
	v := func(key string, major, minor uint32) map[string]chainjson.VersionResult {
		return map[string]chainjson.VersionResult{key: {Major: major, Minor: minor}}
	}
	for _, tt := range []struct {
		name     string
		versions map[string]chainjson.VersionResult
		ok       bool
	}{
		{"v1.3.14", v("monetariumjsonrpcapi", 8, 3), true},
		{"newer minor", v("monetariumjsonrpcapi", 8, 4), true},
		{"older minor", v("monetariumjsonrpcapi", 8, 2), false},
		{"next major", v("monetariumjsonrpcapi", 9, 0), false},
		{"dcrd's key", v("dcrdjsonrpcapi", 8, 3), false},
	} {
		if _, err := checkNodeVersion(tt.versions); (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok = %t", tt.name, err, tt.ok)
		}
	}
}

// TestConnectNodeVersion drives Connect, through its node relay path, against
// a stand-in node that answers getcurrentnet and version, and refuses
// getinfo. Connect must stop at the version check for a version it does not
// support, and pass it for monetarium-node v1.3.14's.
func TestConnectNodeVersion(t *testing.T) {
	for _, tt := range []struct {
		name    string
		key     string
		major   uint32
		minor   uint32
		wantErr string
	}{
		{"v1.3.14", "monetariumjsonrpcapi", 8, 3, "getinfo"},
		{"dcrd's key", "dcrdjsonrpcapi", 8, 3, "missing 'monetariumjsonrpcapi'"},
		{"next major", "monetariumjsonrpcapi", 9, 0, "incompatible JSON-RPC version"},
	} {
		node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Method string          `json:"method"`
				ID     json.RawMessage `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resp := map[string]any{"id": req.ID, "error": nil}
			switch req.Method {
			case "getcurrentnet":
				resp["result"] = uint32(wire.SimNet)
			case "version":
				resp["result"] = map[string]chainjson.VersionResult{tt.key: {Major: tt.major, Minor: tt.minor}}
			default:
				resp["result"] = nil
				resp["error"] = map[string]any{"code": -1, "message": "not served by the test node"}
			}
			json.NewEncoder(w).Encode(resp)
		}))
		be := unconnectedDCR(&asset.BackendConfig{Logger: tLogger, RelayAddr: strings.TrimPrefix(node.URL, "http://")},
			&config{Network: dex.Simnet, RPCUser: "u", RPCPass: "p"})
		_, err := be.Connect(context.Background())
		node.Close()
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: Connect error %v, want one containing %q", tt.name, err, tt.wantErr)
		}
	}
}

// TestDriver checks the driver as the server's asset registry sees it.
func TestDriver(t *testing.T) {
	var found bool
	for _, a := range asset.Assets() {
		if a.AssetID != 9508 {
			continue
		}
		found = true
		if a.Symbol != "var" || a.Name != "Monetarium" {
			t.Errorf("asset 9508 is %q %q, want var Monetarium", a.Symbol, a.Name)
		}
		if c := a.UnitInfo.Conventional; c.Unit != "VAR" || c.ConversionFactor != 1e8 {
			t.Errorf("conventional unit %s at %d, want VAR at 1e8", c.Unit, c.ConversionFactor)
		}
	}
	if !found {
		t.Fatal("no driver registered for 9508")
	}

	// The server's lot and bond minimums are Monetarium's (item 4).
	lot, bond, _ := asset.Minimums(9508, 10)
	if lot != dexmon.MinLotSize(10) || bond != dexmon.MinBondSize(10) {
		t.Errorf("minimums at 10 atoms/B: lot %d, bond %d; want %d, %d",
			lot, bond, dexmon.MinLotSize(10), dexmon.MinBondSize(10))
	}
}

// TestValidateOrderFunding checks that the order's required funds use
// Monetarium's swap sizes, which are larger than Decred's.
func TestValidateOrderFunding(t *testing.T) {
	const swapVal, maxSwaps = 1e8, 3
	nfo := &dex.Asset{MaxFeeRate: 10}
	req := calc.RequiredOrderFunds(swapVal, dexmon.P2PKHInputSize, maxSwaps,
		dexmon.InitTxSizeBase, dexmon.InitTxSize, nfo.MaxFeeRate)
	be := &Backend{}
	if !be.ValidateOrderFunding(swapVal, req, 0, dexmon.P2PKHInputSize, maxSwaps, nfo) {
		t.Errorf("%d atoms refused for a %d-atom order", req, uint64(swapVal))
	}
	if be.ValidateOrderFunding(swapVal, req-1, 0, dexmon.P2PKHInputSize, maxSwaps, nfo) {
		t.Errorf("%d atoms accepted for a %d-atom order that needs %d", req-1, uint64(swapVal), req)
	}
}
