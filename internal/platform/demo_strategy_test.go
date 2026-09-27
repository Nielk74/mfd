package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Nielk74/mfd/internal/etoro"
)

// Synthetic provider shapes exercise the strategy gate; these are not account
// records or market observations and never leave the httptest server.
func syntheticDemoServer(t *testing.T, cash, bid, ask string, pending bool) *httptest.Server {
	t.Helper()
	providerTime := time.Now().UTC().Format("2006-01-02T15:04:05.000")
	quoteTime := time.Now().UTC().Format("2006-01-02T15:04:05.000")
	costTime := time.Now().UTC().Format(time.RFC3339Nano)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case etoro.DemoAggregatePath:
			fmt.Fprintf(w, `{"cid":123456,"timestamp":%q,"accountCurrency":"USD","accountTotals":{"accountAvailableCash":%s,"accountTotalValue":100000,"accountCurrentPnl":0},"instrumentAggregates":[],"mirrors":[]}`, providerTime, cash)
		case etoro.DemoPnLPath:
			orders := "[]"
			if pending {
				orders = `[{"synthetic":true}]`
			}
			fmt.Fprintf(w, `{"clientPortfolio":{"positions":[],"mirrors":[],"orders":%s,"stockOrders":[],"entryOrders":[],"exitOrders":[],"ordersForOpen":[],"ordersForClose":[],"ordersForCloseMultiple":[]}}`, orders)
		case "/api/v1/market-data/search":
			if r.URL.Query().Get("internalSymbolFull") != "BTC" {
				t.Errorf("unexpected symbol filter")
			}
			_, _ = w.Write([]byte(`{"items":[{"instrumentId":100000,"internalSymbolFull":"BTC","isCurrentlyTradable":true,"isBuyEnabled":true}]}`))
		case "/api/v2/market-data/rates":
			fmt.Fprintf(w, `{"results":[{"instrumentId":100000,"bid":%s,"ask":%s,"date":%q,"quoteType":"realtime"}]}`, bid, ask, quoteTime)
		case etoro.DemoEligibilityPath:
			_, _ = w.Write([]byte(`{"currency":"usd","eligibilities":[{"instrumentId":100000,"symbol":"BTC","allowOpenPosition":true,"minPositionExposure":50,"leverageConfigs":[{"settlementType":"real","direction":"long","leverageValues":[1],"isPotential":false,"minPositionAmount":50}]}]}`))
		case etoro.DemoCostsPath:
			fmt.Fprintf(w, `{"instrumentId":100000,"lastUpdated":%q,"costs":[{"costType":"marketSpread","currency":"USD","value":1},{"costType":"overnightFee","currency":"USD","value":0}]}`, costTime)
		default:
			t.Errorf("unexpected provider path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func TestDemoPreflightRequiresAccountAndMarketChecks(t *testing.T) {
	for _, tc := range []struct {
		name, cash, bid, ask string
		pending              bool
		wantReady            bool
		failedCheck          string
	}{
		{name: "ready", cash: "50000", bid: "80000", ask: "80010", wantReady: true},
		{name: "insufficient cash", cash: "400", bid: "80000", ask: "80010", failedCheck: "cash_budget"},
		{name: "pending broker order", cash: "50000", bid: "80000", ask: "80010", pending: true, failedCheck: "no_existing_orders"},
		{name: "wide spread", cash: "50000", bid: "80000", ask: "82000", failedCheck: "spread_cap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := syntheticDemoServer(t, tc.cash, tc.bid, tc.ask, tc.pending)
			defer server.Close()
			client := &etoro.Client{BaseURL: server.URL, APIKey: "synthetic-app", UserKey: "synthetic-demo", HTTP: server.Client()}
			service := &BrokerService{bound: map[string]*etoro.Client{"demo": client}, demoExecutionEnabled: true, token: "synthetic-operator-token"}
			result := service.evaluateDemo(context.Background())
			if (result.Plan != nil) != tc.wantReady {
				t.Fatalf("plan ready=%t, reason=%s", result.Plan != nil, result.Reason)
			}
			if tc.wantReady {
				if result.Plan.OrderType != "limitIOC" || result.Plan.Leverage != 1 || !result.Plan.AmountUSD.Equal(demoAmount) || !result.Plan.LimitRate.GreaterThan(result.Plan.Ask) {
					t.Fatalf("unsafe order plan: %+v", result.Plan)
				}
				payload, err := json.Marshal(result.Plan)
				if err != nil || !strings.Contains(string(payload), `"amount_usd":"100"`) {
					t.Fatalf("money must cross API as string: %s %v", payload, err)
				}
			} else {
				found := false
				for _, check := range result.Checks {
					if check.Name == tc.failedCheck && !check.Passed {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing failed check %s: %+v", tc.failedCheck, result.Checks)
				}
			}
		})
	}
}

func TestBrokerStatusNeverTreatsAcceptanceAsFill(t *testing.T) {
	for _, tc := range []struct {
		status    int
		positions []int64
		want      string
	}{
		{1, nil, "accepted"}, {3, nil, "unknown"}, {3, []int64{42}, "filled"},
		{5, []int64{42}, "partially_filled"}, {10, []int64{42}, "partially_filled"}, {4, nil, "rejected"},
	} {
		got, _ := demoLookupState(etoro.OrderLookup{StatusID: tc.status, PositionIDs: tc.positions})
		if got != tc.want {
			t.Fatalf("status %d positions %v: got %s, want %s", tc.status, tc.positions, got, tc.want)
		}
	}
}
