package etoro

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
)

func TestDemoMarketOrderUsesOnlyDemoEndpointAndExactNumericBoundary(t *testing.T) {
	requestID := "11111111-2222-4333-8444-555555555555"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != DemoOrderPath || r.Header.Get("x-request-id") != requestID {
			t.Errorf("unexpected order request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if string(body["amount"]) != "100" || string(body["settlementType"]) != `"real"` || string(body["orderType"]) != `"mkt"` || string(body["leverage"]) != "1" || body["limitRate"] != nil || body["triggerRate"] != nil {
			t.Errorf("unsafe numeric/order boundary: %v", body)
		}
		_, _ = w.Write([]byte(`{"orderId":42,"referenceId":"11111111-2222-4333-8444-555555555555"}`))
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, APIKey: "synthetic-app", UserKey: "synthetic-demo", HTTP: server.Client()}
	_, status, receipt, err := client.SubmitDemoMarket(context.Background(), requestID, 100000, decimal.NewFromInt(100))
	if err != nil || status != 200 || receipt.OrderID != 42 {
		t.Fatalf("receipt %+v, status %d, err %v", receipt, status, err)
	}
}

func TestDemoBookRejectsMissingPendingOrderArrays(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"clientPortfolio":{"positions":[],"mirrors":[]}}`))
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, APIKey: "synthetic-app", UserKey: "synthetic-demo", HTTP: server.Client()}
	if _, _, err := client.FetchDemoBook(context.Background()); err == nil {
		t.Fatal("missing order arrays were silently treated as empty")
	}
}
