// Package lab contains deterministic, broker-independent fixture valuation.
package lab

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

//go:embed fixture.json
var fixture []byte

type Position struct {
	Symbol   string          `json:"symbol"`
	Currency string          `json:"currency"`
	Units    decimal.Decimal `json:"units"`
	Cost     decimal.Decimal `json:"cost"`
}
type Quote struct {
	Symbol   string          `json:"symbol"`
	Currency string          `json:"currency"`
	Bid      decimal.Decimal `json:"bid"`
	Ask      decimal.Decimal `json:"ask"`
	At       time.Time       `json:"at"`
}
type Sleeve struct {
	ID        string          `json:"id"`
	Cash      decimal.Decimal `json:"cash"`
	Strategy  string          `json:"strategy"`
	Positions []Position      `json:"positions"`
}
type Snapshot struct {
	Sleeve            string          `json:"sleeve"`
	At                time.Time       `json:"at"`
	Currency          string          `json:"currency"`
	Cash              decimal.Decimal `json:"cash"`
	MarketValue       decimal.Decimal `json:"market_value"`
	Equity            decimal.Decimal `json:"equity"`
	LiquidationEquity decimal.Decimal `json:"liquidation_equity"`
	UnrealizedPnL     decimal.Decimal `json:"unrealized_pnl"`
	Positions         []Position      `json:"positions"`
	Quotes            []Quote         `json:"quotes"`
}
type Decision struct {
	ID            string    `json:"id"`
	Sleeve        string    `json:"sleeve"`
	At            time.Time `json:"at"`
	Actor         string    `json:"actor"`
	Strategy      string    `json:"strategy"`
	Action        string    `json:"action"`
	Reason        string    `json:"reason"`
	Checks        []string  `json:"checks"`
	SnapshotIndex int       `json:"snapshot_index"`
}
type Result struct {
	DatasetHash      string     `json:"dataset_hash"`
	ValuationVersion string     `json:"valuation_version"`
	Snapshots        []Snapshot `json:"snapshots"`
	Decisions        []Decision `json:"decisions"`
}

func DatasetHash() string { s := sha256.Sum256(fixture); return hex.EncodeToString(s[:]) }

// Value only supports unlevered long USD spot positions. A missing or stale mark
// invalidates the entire snapshot; a partial sum must never look like a full NAV.
func Value(s Sleeve, quotes []Quote, at time.Time, maxAge time.Duration) (Snapshot, error) {
	out := Snapshot{Sleeve: s.ID, At: at, Currency: "USD", Cash: s.Cash, Positions: s.Positions, Quotes: []Quote{}}
	if s.Cash.IsNegative() || maxAge <= 0 {
		return Snapshot{}, fmt.Errorf("invalid cash or freshness policy")
	}
	bySymbol := make(map[string]Quote)
	for _, q := range quotes {
		if _, exists := bySymbol[q.Symbol]; exists {
			return Snapshot{}, fmt.Errorf("duplicate quote: %s", q.Symbol)
		}
		bySymbol[q.Symbol] = q
	}
	cost, liquidation := decimal.Zero, decimal.Zero
	for _, p := range s.Positions {
		q, ok := bySymbol[p.Symbol]
		if !ok {
			return Snapshot{}, fmt.Errorf("missing mark: %s", p.Symbol)
		}
		if p.Currency != "USD" || q.Currency != "USD" {
			return Snapshot{}, fmt.Errorf("unsupported currency: %s", p.Symbol)
		}
		if !p.Units.IsPositive() || p.Cost.IsNegative() {
			return Snapshot{}, fmt.Errorf("invalid long position: %s", p.Symbol)
		}
		if !q.Bid.IsPositive() || q.Ask.LessThan(q.Bid) {
			return Snapshot{}, fmt.Errorf("invalid quote: %s", p.Symbol)
		}
		age := at.Sub(q.At)
		if age < 0 || age > maxAge {
			return Snapshot{}, fmt.Errorf("stale or future mark: %s", p.Symbol)
		}
		mid := q.Bid.Add(q.Ask).Div(decimal.NewFromInt(2))
		out.MarketValue = out.MarketValue.Add(p.Units.Mul(mid))
		liquidation = liquidation.Add(p.Units.Mul(q.Bid))
		cost = cost.Add(p.Cost)
		out.Quotes = append(out.Quotes, q)
	}
	out.Equity = s.Cash.Add(out.MarketValue)
	out.LiquidationEquity = s.Cash.Add(liquidation)
	out.UnrealizedPnL = out.MarketValue.Sub(cost)
	return out, nil
}

func Replay() (Result, error) {
	var data struct {
		Sleeves      []Sleeve `json:"sleeves"`
		Observations []struct {
			At     time.Time `json:"at"`
			Quotes []Quote   `json:"quotes"`
		} `json:"observations"`
	}
	if err := json.Unmarshal(fixture, &data); err != nil {
		return Result{}, err
	}
	result := Result{DatasetHash: DatasetHash(), ValuationVersion: "usd-long-spot-mid-v1"}
	for i, obs := range data.Observations {
		for _, s := range data.Sleeves {
			snap, err := Value(s, obs.Quotes, obs.At, 5*time.Minute)
			if err != nil {
				return Result{}, err
			}
			decision := Decision{ID: fmt.Sprintf("%s-%d", s.ID, i), Sleeve: s.ID, At: obs.At, Actor: "fixture-worker", Strategy: s.Strategy, Action: "hold", Reason: "Fixed holdings baseline. No order proposed.", Checks: []string{"USD unlevered long positions", "Valid bid/ask within replay freshness limit", "Execution unavailable in fixture mode"}, SnapshotIndex: len(result.Snapshots)}
			if s.Strategy == "review-move-v1" {
				basis := snap.MarketValue.Sub(snap.UnrealizedPnL)
				if basis.IsPositive() && snap.UnrealizedPnL.Abs().GreaterThanOrEqual(basis.Mul(decimal.RequireFromString("0.01"))) {
					decision.Action = "review"
					decision.Reason = "Unrealized move reached 1% of cost basis. Review recorded; holdings unchanged."
				} else {
					decision.Reason = "Unrealized move below 1% of cost basis. Holdings unchanged."
				}
			}
			result.Snapshots = append(result.Snapshots, snap)
			result.Decisions = append(result.Decisions, decision)
		}
	}
	return result, nil
}
