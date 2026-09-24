package lab

import (
	"encoding/json"
	"github.com/shopspring/decimal"
	"testing"
	"time"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func TestValueExactAndLiquidation(t *testing.T) {
	now := time.Now()
	s := Sleeve{ID: "fractional", Cash: d("0.10"), Positions: []Position{{Symbol: "X", Currency: "USD", Units: d("0.3"), Cost: d("0.03")}}}
	v, err := Value(s, []Quote{{Symbol: "X", Currency: "USD", Bid: d("0.10"), Ask: d("0.12"), At: now}}, now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Equity.Equal(d("0.133")) || !v.LiquidationEquity.Equal(d("0.13")) || !v.UnrealizedPnL.Equal(d("0.003")) {
		t.Fatalf("incorrect valuation: %+v", v)
	}
}
func TestInvalidMarksDoNotProducePartialNAV(t *testing.T) {
	now := time.Now()
	s := Sleeve{Cash: d("100"), Positions: []Position{{Symbol: "X", Currency: "USD", Units: d("1"), Cost: d("10")}}}
	good := Quote{Symbol: "X", Currency: "USD", Bid: d("10"), Ask: d("11"), At: now}
	cases := map[string][]Quote{"missing": {}, "duplicate": {good, good}}
	for _, name := range []string{"crossed", "stale", "future", "foreign", "zero"} {
		q := good
		switch name {
		case "crossed":
			q.Ask = d("9")
		case "stale":
			q.At = now.Add(-2 * time.Second)
		case "future":
			q.At = now.Add(time.Second)
		case "foreign":
			q.Currency = "EUR"
		case "zero":
			q.Bid = d("0")
		}
		cases[name] = []Quote{q}
	}
	for name, quotes := range cases {
		t.Run(name, func(t *testing.T) {
			v, err := Value(s, quotes, now, time.Second)
			if err == nil || !v.Equity.IsZero() {
				t.Fatalf("accepted invalid mark: %+v %v", v, err)
			}
		})
	}
}
func TestReplayReproducibilityAndConservation(t *testing.T) {
	a, err := Replay()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Replay()
	if err != nil {
		t.Fatal(err)
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	if string(aj) != string(bj) {
		t.Fatal("replay differs")
	}
	if len(a.Snapshots) != 6 || len(a.Decisions) != 6 {
		t.Fatal("incomplete replay")
	}
	totals := []string{"10000", "10000", "10005"}
	for i, want := range totals {
		if !a.Snapshots[i*2].Equity.Add(a.Snapshots[i*2+1].Equity).Equal(d(want)) {
			t.Fatalf("wrong total at observation %d", i)
		}
	}
	if a.Decisions[5].Action != "review" || a.Decisions[0].Action != "hold" {
		t.Fatal("wrong decision")
	}
}
