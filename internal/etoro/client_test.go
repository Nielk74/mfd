package etoro

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const aggregateFixture = `{"timestamp":"2026-05-26T15:24:25.267Z","accountCurrency":"USD","accountTotals":{"accountAvailableCash":4320.84,"accountTotalValue":5154.48,"accountCurrentPnl":-300.35},"instrumentAggregates":[{"instrumentId":100000,"assetCurrency":"USD","netUnits":0.008076,"netCurrentExposureAccountCurrency":624.57,"pnlAssetCurrency":-225.3,"liquidationValueAccountCurrency":624.56,"avgLeverage":1}],"mirrors":[{}]}`

func TestAggregateReadUsesOfficialHeadersAndExactNumbers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != AggregatePath {
			t.Errorf("wrong method/path: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "public-test" || r.Header.Get("x-user-key") != "user-test" || len(r.Header.Get("x-request-id")) != 36 {
			t.Error("missing authentication headers")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(aggregateFixture))
	}))
	defer server.Close()
	client := Client{BaseURL: server.URL, APIKey: "public-test", UserKey: "user-test"}
	raw, s, err := client.FetchAggregate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Currency != "USD" || s.TotalValue.String() != "5154.48" || s.CurrentPnL.String() != "-300.35" || s.MirrorCount != 1 {
		t.Fatalf("wrong totals: %+v", s)
	}
	if len(s.Instruments) != 1 || s.Instruments[0].NetUnits.String() != "0.008076" {
		t.Fatalf("wrong exact position: %+v", s.Instruments)
	}
	if Digest(raw) == "" {
		t.Fatal("missing source digest")
	}
}
func TestAggregateRejectsMissingFinancialFields(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(aggregateFixture, `"accountTotalValue":5154.48`, `"accountTotalValue":null`, 1),
		strings.Replace(aggregateFixture, `"netUnits":0.008076`, `"netUnits":null`, 1),
		strings.Replace(aggregateFixture, `"assetCurrency":"USD"`, `"assetCurrency":""`, 1),
		aggregateFixture + `{"unexpected":true}`,
	} {
		if _, err := ParseAggregate([]byte(raw)); err == nil {
			t.Fatal("incomplete provider account accepted")
		}
	}
}
func TestPermissionErrorNeverContainsProviderBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"errorMessage":"You do not have permission to access this endpoint PRIVATE"}`))
	}))
	defer server.Close()
	_, _, err := (&Client{BaseURL: server.URL, APIKey: "public-test", UserKey: "user-test"}).FetchAggregate(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != 403 || apiErr.Category != "permission_denied" {
		t.Fatalf("wrong classification: %v", err)
	}
	if err.Error() == "" || contains(err.Error(), "PRIVATE") {
		t.Fatal("provider body leaked")
	}
}
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
