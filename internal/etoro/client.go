// Package etoro implements a read-only adapter to the documented demo API.
// It never logs credentials or provider account payloads.
package etoro

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const OfficialBase = "https://public-api.etoro.com"
const AggregatePath = "/api/v1/trading/info/demo/aggregate-portfolio"
const maxBody = 2 << 20

type Client struct {
	BaseURL string
	APIKey  string
	UserKey string
	HTTP    *http.Client
}

type APIError struct {
	Status   int
	Category string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("eToro demo read: HTTP %d (%s)", e.Status, e.Category)
}

type Instrument struct {
	InstrumentID    int64           `json:"instrument_id"`
	AssetCurrency   string          `json:"asset_currency"`
	NetUnits        decimal.Decimal `json:"net_units"`
	ExposureUSD     decimal.Decimal `json:"exposure_usd"`
	PnL             decimal.Decimal `json:"pnl_asset_currency"`
	LiquidationUSD  decimal.Decimal `json:"liquidation_usd"`
	AverageLeverage decimal.Decimal `json:"average_leverage"`
}
type Snapshot struct {
	ProviderAt    time.Time       `json:"provider_at"`
	Currency      string          `json:"currency"`
	AvailableCash decimal.Decimal `json:"available_cash"`
	TotalValue    decimal.Decimal `json:"total_value"`
	CurrentPnL    decimal.Decimal `json:"current_pnl"`
	Instruments   []Instrument    `json:"instruments"`
	MirrorCount   int             `json:"mirror_count"`
}

func (c *Client) FetchAggregate(ctx context.Context) ([]byte, Snapshot, error) {
	if c.APIKey == "" || c.UserKey == "" {
		return nil, Snapshot{}, errors.New("eToro credentials not configured")
	}
	base := c.BaseURL
	if base == "" {
		base = OfficialBase
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+AggregatePath, nil)
	if err != nil {
		return nil, Snapshot{}, err
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return nil, Snapshot{}, err
	}
	id[6] = (id[6] & 15) | 64
	id[8] = (id[8] & 63) | 128
	req.Header.Set("x-request-id", fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:]))
	req.Header.Set("x-api-key", c.APIKey)
	req.Header.Set("x-user-key", c.UserKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "mfd-demo-importer/0.2")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, err := httpClient.Do(req)
	if err != nil {
		return nil, Snapshot{}, fmt.Errorf("eToro request failed: %w", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBody+1))
	if err != nil {
		return nil, Snapshot{}, fmt.Errorf("eToro response read failed: %w", err)
	}
	if len(raw) > maxBody {
		return nil, Snapshot{}, errors.New("eToro response exceeds 2 MiB")
	}
	if response.StatusCode != 200 {
		category := "request_rejected"
		lower := strings.ToLower(string(raw))
		switch {
		case response.StatusCode == 403 && strings.Contains(lower, "permission") && strings.Contains(lower, "access"):
			category = "permission_denied"
		case response.StatusCode == 401:
			category = "unauthorized"
		case response.StatusCode == 429:
			category = "rate_limited"
		case response.StatusCode >= 500:
			category = "provider_unavailable"
		case response.StatusCode >= 300 && response.StatusCode < 400:
			category = "redirect_rejected"
		}
		return nil, Snapshot{}, &APIError{Status: response.StatusCode, Category: category}
	}
	snapshot, err := ParseAggregate(raw)
	if err != nil {
		return nil, Snapshot{}, fmt.Errorf("eToro aggregate schema: %w", err)
	}
	return raw, snapshot, nil
}

func ParseAggregate(raw []byte) (Snapshot, error) {
	var envelope struct {
		Timestamp            time.Time                  `json:"timestamp"`
		AccountCurrency      string                     `json:"accountCurrency"`
		AccountTotals        map[string]json.RawMessage `json:"accountTotals"`
		InstrumentAggregates []json.RawMessage          `json:"instrumentAggregates"`
		Mirrors              []json.RawMessage          `json:"mirrors"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return Snapshot{}, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return Snapshot{}, errors.New("unexpected trailing aggregate data")
	}
	if envelope.Timestamp.IsZero() || envelope.AccountCurrency != "USD" || envelope.AccountTotals == nil {
		return Snapshot{}, errors.New("missing timestamp, USD currency or account totals")
	}
	requiredDecimal := func(name string) (decimal.Decimal, error) {
		value, ok := envelope.AccountTotals[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return decimal.Zero, fmt.Errorf("missing accountTotals.%s", name)
		}
		var d decimal.Decimal
		if err := json.Unmarshal(value, &d); err != nil {
			return decimal.Zero, fmt.Errorf("invalid accountTotals.%s", name)
		}
		return d, nil
	}
	var s Snapshot
	s.ProviderAt = envelope.Timestamp.UTC()
	s.Currency = "USD"
	s.MirrorCount = len(envelope.Mirrors)
	var err error
	if s.AvailableCash, err = requiredDecimal("accountAvailableCash"); err != nil {
		return Snapshot{}, err
	}
	if s.TotalValue, err = requiredDecimal("accountTotalValue"); err != nil {
		return Snapshot{}, err
	}
	if s.CurrentPnL, err = requiredDecimal("accountCurrentPnl"); err != nil {
		return Snapshot{}, err
	}
	if s.TotalValue.IsNegative() {
		return Snapshot{}, errors.New("negative account total value")
	}
	s.Instruments = []Instrument{}
	for _, item := range envelope.InstrumentAggregates {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil {
			return Snapshot{}, errors.New("invalid instrument aggregate")
		}
		for _, name := range []string{"instrumentId", "assetCurrency", "netUnits", "netCurrentExposureAccountCurrency", "pnlAssetCurrency", "liquidationValueAccountCurrency", "avgLeverage"} {
			value, ok := fields[name]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return Snapshot{}, fmt.Errorf("missing instrument aggregate %s", name)
			}
		}
		var provider struct {
			InstrumentID    int64           `json:"instrumentId"`
			AssetCurrency   string          `json:"assetCurrency"`
			NetUnits        decimal.Decimal `json:"netUnits"`
			ExposureUSD     decimal.Decimal `json:"netCurrentExposureAccountCurrency"`
			PnL             decimal.Decimal `json:"pnlAssetCurrency"`
			LiquidationUSD  decimal.Decimal `json:"liquidationValueAccountCurrency"`
			AverageLeverage decimal.Decimal `json:"avgLeverage"`
		}
		if err := json.Unmarshal(item, &provider); err != nil || provider.InstrumentID <= 0 || provider.AssetCurrency == "" {
			return Snapshot{}, errors.New("invalid instrument aggregate")
		}
		s.Instruments = append(s.Instruments, Instrument{InstrumentID: provider.InstrumentID, AssetCurrency: provider.AssetCurrency, NetUnits: provider.NetUnits, ExposureUSD: provider.ExposureUSD, PnL: provider.PnL, LiquidationUSD: provider.LiquidationUSD, AverageLeverage: provider.AverageLeverage})
	}
	return s, nil
}
func Digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
