// Package lab contains deterministic, broker-independent fixture valuation.
package lab

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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
	Experiment       Experiment `json:"experiment"`
	Snapshots        []Snapshot `json:"snapshots"`
	Decisions        []Decision `json:"decisions"`
}

// Experiment changes only the final artificial observation. It cannot change
// holdings, submit an order, or be mistaken for an observed market feed.
type Experiment struct {
	Name               string         `json:"name"`
	FinalShockBPS      map[string]int `json:"final_shock_bps"`
	ReviewThresholdBPS int            `json:"review_threshold_bps"`
}

func NormalizeExperiment(e Experiment) (Experiment, error) {
	e.Name = strings.TrimSpace(e.Name)
	if e.Name == "" {
		e.Name = "Baseline"
	}
	if len(e.Name) > 60 || strings.ContainsAny(e.Name, "\r\n\t") {
		return Experiment{}, fmt.Errorf("name must be at most 60 characters on one line")
	}
	if e.ReviewThresholdBPS == 0 {
		e.ReviewThresholdBPS = 100
	}
	if e.ReviewThresholdBPS < 1 || e.ReviewThresholdBPS > 5000 {
		return Experiment{}, fmt.Errorf("review_threshold_bps must be 1..5000")
	}
	if e.FinalShockBPS == nil {
		e.FinalShockBPS = map[string]int{}
	}
	for symbol, bps := range e.FinalShockBPS {
		if symbol != "AAPL" && symbol != "MSFT" {
			return Experiment{}, fmt.Errorf("final_shock_bps supports AAPL and MSFT only")
		}
		if bps < -5000 || bps > 5000 {
			return Experiment{}, fmt.Errorf("final_shock_bps must be -5000..5000")
		}
	}
	return e, nil
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

func Replay() (Result, error) { return ReplayExperiment(Experiment{}) }

func ReplayExperiment(input Experiment) (Result, error) {
	experiment, err := NormalizeExperiment(input)
	if err != nil {
		return Result{}, err
	}
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
	result := Result{DatasetHash: DatasetHash(), ValuationVersion: "usd-long-spot-mid-v1", Experiment: experiment}
	for i, obs := range data.Observations {
		if i == len(data.Observations)-1 {
			for j := range obs.Quotes {
				bps := experiment.FinalShockBPS[obs.Quotes[j].Symbol]
				factor := decimal.NewFromInt(int64(10000 + bps)).Div(decimal.NewFromInt(10000))
				obs.Quotes[j].Bid = obs.Quotes[j].Bid.Mul(factor)
				obs.Quotes[j].Ask = obs.Quotes[j].Ask.Mul(factor)
			}
		}
		for _, s := range data.Sleeves {
			snap, err := Value(s, obs.Quotes, obs.At, 5*time.Minute)
			if err != nil {
				return Result{}, err
			}
			decision := Decision{ID: fmt.Sprintf("%s-%d", s.ID, i), Sleeve: s.ID, At: obs.At, Actor: "fixture-worker", Strategy: s.Strategy, Action: "hold", Reason: "Fixed holdings baseline. No order proposed.", Checks: []string{"USD unlevered long positions", "Valid bid/ask within replay freshness limit", "Execution unavailable in fixture mode"}, SnapshotIndex: len(result.Snapshots)}
			if s.Strategy == "review-move-v1" {
				basis := snap.MarketValue.Sub(snap.UnrealizedPnL)
				threshold := decimal.NewFromInt(int64(experiment.ReviewThresholdBPS)).Div(decimal.NewFromInt(10000))
				if basis.IsPositive() && snap.UnrealizedPnL.Abs().GreaterThanOrEqual(basis.Mul(threshold)) {
					decision.Action = "review"
					decision.Reason = fmt.Sprintf("Unrealized move reached %s%% of cost basis. Review recorded; holdings unchanged.", decimal.NewFromInt(int64(experiment.ReviewThresholdBPS)).Div(decimal.NewFromInt(100)).String())
				} else {
					decision.Reason = fmt.Sprintf("Unrealized move below %s%% of cost basis. Holdings unchanged.", decimal.NewFromInt(int64(experiment.ReviewThresholdBPS)).Div(decimal.NewFromInt(100)).String())
				}
			}
			result.Snapshots = append(result.Snapshots, snap)
			result.Decisions = append(result.Decisions, decision)
		}
	}
	return result, nil
}
