package etoro

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// These operations use only documented public APIs. There is intentionally no
// Real execution path in this package.
const DemoPnLPath = "/api/v1/trading/info/demo/pnl"
const DemoEligibilityPath = "/api/v2/trading/info/demo/eligibility"
const DemoCostsPath = "/api/v2/trading/info/demo/costs"
const DemoOrderPath = "/api/v2/trading/execution/demo/orders"
const DemoOrderLookupPath = "/api/v2/trading/info/demo/orders:lookup"

type MarketInstrument struct {
	ID       int64
	Symbol   string
	Tradable bool
	Buyable  bool
}

type MarketRate struct {
	InstrumentID   int64
	Bid            decimal.Decimal
	Ask            decimal.Decimal
	At             time.Time
	TimeAssumedUTC bool
	QuoteType      string
}

type TradingEligibility struct {
	InstrumentID      int64
	Symbol            string
	CanOpen           bool
	MinExposure       decimal.Decimal
	MinPositionAmount decimal.Decimal
	RealLongUnlevered bool
}

type CostItem struct {
	Type     string
	Currency string
	Value    decimal.Decimal
}

type TradingCosts struct {
	InstrumentID int64
	UpdatedAt    time.Time
	Items        []CostItem
}

type DemoBook struct {
	OpenPositionCount int
	PendingOrderCount int
}

type OrderReceipt struct {
	OrderID     int64
	ReferenceID string
}

type OrderLookup struct {
	OrderID      int64
	StatusID     int
	StatusName   string
	ErrorCode    string
	ErrorMessage string
	PositionIDs  []int64
}

func newRequestID() (string, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", err
	}
	id[6] = (id[6] & 15) | 64
	id[8] = (id[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]), nil
}

// Keep the provider's numeric JSON requirement at this boundary. All mfd API
// monetary values remain decimal strings; json.Number avoids float arithmetic.
func (c *Client) request(ctx context.Context, method, path, requestID string, body any) ([]byte, int, error) {
	if c.APIKey == "" || c.UserKey == "" {
		return nil, 0, errors.New("eToro credentials not configured")
	}
	base := c.BaseURL
	if base == "" {
		base = OfficialBase
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, 0, err
	}
	if requestID == "" {
		requestID, err = newRequestID()
		if err != nil {
			return nil, 0, err
		}
	}
	req.Header.Set("x-request-id", requestID)
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("x-user-key", c.UserKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "mfd-demo-strategy/0.1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(raw) > maxBody {
		return nil, response.StatusCode, errors.New("eToro response exceeds 2 MiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		category := "request_rejected"
		lower := strings.ToLower(string(raw))
		switch {
		case response.StatusCode == 401:
			category = "unauthorized"
		case response.StatusCode == 403 && strings.Contains(lower, "permission"):
			category = "permission_denied"
		case response.StatusCode == 429:
			category = "rate_limited"
		case response.StatusCode >= 500:
			category = "provider_unavailable"
		}
		return raw, response.StatusCode, &APIError{Status: response.StatusCode, Category: category}
	}
	return raw, response.StatusCode, nil
}

func decode(raw []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(target); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("unexpected trailing provider data")
	}
	return nil
}

func (c *Client) FindInstrument(ctx context.Context, symbol string) ([]byte, MarketInstrument, error) {
	query := url.Values{"internalSymbolFull": {symbol}, "fields": {"instrumentId,internalSymbolFull,isCurrentlyTradable,isBuyEnabled"}, "pageSize": {"10"}}
	raw, _, err := c.request(ctx, http.MethodGet, "/api/v1/market-data/search?"+query.Encode(), "", nil)
	if err != nil {
		return raw, MarketInstrument{}, err
	}
	var data struct {
		Items []struct {
			ID       int64  `json:"instrumentId"`
			Symbol   string `json:"internalSymbolFull"`
			Tradable bool   `json:"isCurrentlyTradable"`
			Buyable  bool   `json:"isBuyEnabled"`
		} `json:"items"`
	}
	if err := decode(raw, &data); err != nil {
		return raw, MarketInstrument{}, &SchemaError{}
	}
	for _, item := range data.Items {
		if item.Symbol == symbol && item.ID > 0 {
			return raw, MarketInstrument{item.ID, item.Symbol, item.Tradable, item.Buyable}, nil
		}
	}
	return raw, MarketInstrument{}, errors.New("exact instrument match unavailable")
}

func (c *Client) FetchRate(ctx context.Context, instrumentID int64) ([]byte, MarketRate, error) {
	raw, _, err := c.request(ctx, http.MethodGet, "/api/v2/market-data/rates?instrumentIds="+strconv.FormatInt(instrumentID, 10), "", nil)
	if err != nil {
		return raw, MarketRate{}, err
	}
	var data struct {
		Results []struct {
			ID        int64           `json:"instrumentId"`
			Bid       decimal.Decimal `json:"bid"`
			Ask       decimal.Decimal `json:"ask"`
			Date      string          `json:"date"`
			QuoteType string          `json:"quoteType"`
		} `json:"results"`
	}
	if err := decode(raw, &data); err != nil || len(data.Results) != 1 || data.Results[0].ID != instrumentID {
		return raw, MarketRate{}, &SchemaError{}
	}
	item := data.Results[0]
	at, assumed, err := parseProviderTimestamp(item.Date)
	if err != nil || !item.Bid.IsPositive() || !item.Ask.GreaterThan(item.Bid) {
		return raw, MarketRate{}, &SchemaError{}
	}
	return raw, MarketRate{instrumentID, item.Bid, item.Ask, at, assumed, item.QuoteType}, nil
}

func (c *Client) FetchDemoBook(ctx context.Context) ([]byte, DemoBook, error) {
	raw, _, err := c.request(ctx, http.MethodGet, DemoPnLPath, "", nil)
	if err != nil {
		return raw, DemoBook{}, err
	}
	var envelope map[string]json.RawMessage
	if err := decode(raw, &envelope); err != nil || envelope["clientPortfolio"] == nil {
		return raw, DemoBook{}, &SchemaError{}
	}
	var portfolio map[string]json.RawMessage
	if err := json.Unmarshal(envelope["clientPortfolio"], &portfolio); err != nil || portfolio == nil {
		return raw, DemoBook{}, &SchemaError{}
	}
	counts := map[string]int{}
	for _, name := range []string{"positions", "mirrors", "orders", "stockOrders", "entryOrders", "exitOrders", "ordersForOpen", "ordersForClose", "ordersForCloseMultiple"} {
		value, ok := portfolio[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return raw, DemoBook{}, &SchemaError{}
		}
		var items []json.RawMessage
		if err := json.Unmarshal(value, &items); err != nil || items == nil {
			return raw, DemoBook{}, &SchemaError{}
		}
		counts[name] = len(items)
	}
	return raw, DemoBook{OpenPositionCount: counts["positions"] + counts["mirrors"], PendingOrderCount: counts["orders"] + counts["stockOrders"] + counts["entryOrders"] + counts["exitOrders"] + counts["ordersForOpen"] + counts["ordersForClose"] + counts["ordersForCloseMultiple"]}, nil
}

func (c *Client) FetchDemoEligibility(ctx context.Context, instrumentID int64) ([]byte, TradingEligibility, error) {
	raw, _, err := c.request(ctx, http.MethodPost, DemoEligibilityPath, "", map[string]any{"instrumentIds": []int64{instrumentID}, "currency": "USD"})
	if err != nil {
		return raw, TradingEligibility{}, err
	}
	var data struct {
		Currency string `json:"currency"`
		Items    []struct {
			ID          int64            `json:"instrumentId"`
			Symbol      string           `json:"symbol"`
			CanOpen     bool             `json:"allowOpenPosition"`
			MinExposure *decimal.Decimal `json:"minPositionExposure"`
			Configs     []struct {
				Settlement string           `json:"settlementType"`
				Direction  string           `json:"direction"`
				Leverages  []int            `json:"leverageValues"`
				Potential  bool             `json:"isPotential"`
				Minimum    *decimal.Decimal `json:"minPositionAmount"`
			} `json:"leverageConfigs"`
		} `json:"eligibilities"`
	}
	if err := decode(raw, &data); err != nil || !strings.EqualFold(data.Currency, "USD") || len(data.Items) != 1 || data.Items[0].ID != instrumentID {
		return raw, TradingEligibility{}, &SchemaError{}
	}
	item := data.Items[0]
	if item.MinExposure == nil || item.MinExposure.IsNegative() {
		return raw, TradingEligibility{}, &SchemaError{}
	}
	out := TradingEligibility{InstrumentID: item.ID, Symbol: item.Symbol, CanOpen: item.CanOpen, MinExposure: *item.MinExposure}
	for _, config := range item.Configs {
		if strings.EqualFold(config.Settlement, "real") && strings.EqualFold(config.Direction, "long") && !config.Potential {
			for _, leverage := range config.Leverages {
				if leverage == 1 {
					if config.Minimum == nil || config.Minimum.IsNegative() {
						return raw, TradingEligibility{}, &SchemaError{}
					}
					out.RealLongUnlevered = true
					out.MinPositionAmount = *config.Minimum
				}
			}
		}
	}
	return raw, out, nil
}

func demoOrderBody(instrumentID int64, amount, limitRate decimal.Decimal) map[string]any {
	return map[string]any{"action": "open", "transaction": "buy", "instrumentId": instrumentID, "settlementType": "real", "orderType": "limitIOC", "leverage": 1, "amount": json.Number(amount.String()), "orderCurrency": "usd", "limitRate": json.Number(limitRate.String())}
}

func (c *Client) FetchDemoCosts(ctx context.Context, instrumentID int64, amount decimal.Decimal) ([]byte, TradingCosts, error) {
	body := map[string]any{"action": "open", "transaction": "buy", "instrumentId": instrumentID, "settlementType": "real", "orderType": "limitIOC", "leverage": 1, "amount": json.Number(amount.String()), "orderCurrency": "usd"}
	raw, _, err := c.request(ctx, http.MethodPost, DemoCostsPath, "", body)
	if err != nil {
		return raw, TradingCosts{}, err
	}
	var data struct {
		ID      int64  `json:"instrumentId"`
		Updated string `json:"lastUpdated"`
		Costs   []struct {
			Type     string           `json:"costType"`
			Currency string           `json:"currency"`
			Value    *decimal.Decimal `json:"value"`
		} `json:"costs"`
	}
	if err := decode(raw, &data); err != nil || data.ID != instrumentID || len(data.Costs) == 0 {
		return raw, TradingCosts{}, &SchemaError{}
	}
	at, _, err := parseProviderTimestamp(data.Updated)
	if err != nil {
		return raw, TradingCosts{}, &SchemaError{}
	}
	out := TradingCosts{InstrumentID: data.ID, UpdatedAt: at, Items: make([]CostItem, 0, len(data.Costs))}
	for _, cost := range data.Costs {
		if cost.Type == "" || !strings.EqualFold(cost.Currency, "USD") || cost.Value == nil || cost.Value.IsNegative() {
			return raw, TradingCosts{}, &SchemaError{}
		}
		out.Items = append(out.Items, CostItem{cost.Type, cost.Currency, *cost.Value})
	}
	return raw, out, nil
}

func (c *Client) SubmitDemoLimitIOC(ctx context.Context, requestID string, instrumentID int64, amount, limitRate decimal.Decimal) ([]byte, int, OrderReceipt, error) {
	raw, status, err := c.request(ctx, http.MethodPost, DemoOrderPath, requestID, demoOrderBody(instrumentID, amount, limitRate))
	if err != nil {
		return raw, status, OrderReceipt{}, err
	}
	var receipt OrderReceipt
	var data struct {
		OrderID     int64  `json:"orderId"`
		ReferenceID string `json:"referenceId"`
	}
	if err := decode(raw, &data); err != nil || data.OrderID <= 0 || data.ReferenceID != requestID {
		return raw, status, OrderReceipt{}, &SchemaError{}
	}
	receipt.OrderID, receipt.ReferenceID = data.OrderID, data.ReferenceID
	return raw, status, receipt, nil
}

func (c *Client) LookupDemoOrder(ctx context.Context, referenceID string) ([]byte, int, OrderLookup, error) {
	raw, status, err := c.request(ctx, http.MethodGet, DemoOrderLookupPath+"?referenceId="+url.QueryEscape(referenceID), "", nil)
	if err != nil {
		return raw, status, OrderLookup{}, err
	}
	var data struct {
		OrderID int64 `json:"orderId"`
		Status  struct {
			ID           int    `json:"id"`
			Name         string `json:"name"`
			ErrorCode    string `json:"errorCode"`
			ErrorMessage string `json:"errorMessage"`
		} `json:"status"`
		Executions []struct {
			PositionID int64 `json:"positionId"`
		} `json:"positionExecutions"`
	}
	if err := decode(raw, &data); err != nil || data.OrderID <= 0 || data.Status.ID <= 0 {
		return raw, status, OrderLookup{}, &SchemaError{}
	}
	out := OrderLookup{OrderID: data.OrderID, StatusID: data.Status.ID, StatusName: data.Status.Name, ErrorCode: data.Status.ErrorCode, ErrorMessage: data.Status.ErrorMessage, PositionIDs: []int64{}}
	for _, execution := range data.Executions {
		if execution.PositionID > 0 {
			out.PositionIDs = append(out.PositionIDs, execution.PositionID)
		}
	}
	return raw, status, out, nil
}
