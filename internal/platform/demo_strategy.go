package platform

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Nielk74/mfd/internal/etoro"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

// This is a deliberately narrow Demo strategy: one $100 unlevered BTC buy,
// only when the observed account is empty and live provider checks pass. It is
// a connectivity and decision-trail experiment, not a performance claim.
const DemoProbeVersion = "btc-liquidity-probe-v1"

var demoAmount = decimal.NewFromInt(100)
var demoMinimumCash = decimal.NewFromInt(500)
var demoCostCap = decimal.NewFromInt(2)
var demoSpreadCapBps = decimal.NewFromInt(50)
var demoLimitMultiplier = decimal.RequireFromString("1.002")

var ErrDemoIntentNotReady = errors.New("demo decision is not ready or has expired")
var ErrDemoActiveIntent = errors.New("another demo order is still unresolved")

type DemoCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}
type DemoPlan struct {
	Symbol                  string            `json:"symbol"`
	AccountFingerprint      string            `json:"account_fingerprint"`
	InstrumentID            int64             `json:"instrument_id"`
	AmountUSD               decimal.Decimal   `json:"amount_usd"`
	OrderType               string            `json:"order_type"`
	SettlementType          string            `json:"settlement_type"`
	Leverage                int               `json:"leverage"`
	Bid                     decimal.Decimal   `json:"bid"`
	Ask                     decimal.Decimal   `json:"ask"`
	LimitRate               decimal.Decimal   `json:"limit_rate"`
	SpreadBps               decimal.Decimal   `json:"spread_bps"`
	QuoteAt                 time.Time         `json:"quote_at"`
	QuoteTimeAssumedUTC     bool              `json:"quote_time_assumed_utc"`
	EstimatedUpfrontCostUSD decimal.Decimal   `json:"estimated_upfront_cost_usd"`
	SourceSHA256            map[string]string `json:"source_sha256"`
}
type DemoEvent struct {
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"`
	Summary    string    `json:"summary"`
	HTTPStatus int       `json:"http_status"`
}
type DemoStrategyRun struct {
	ID                string      `json:"id"`
	IdempotencyKey    string      `json:"idempotency_key"`
	StrategyVersion   string      `json:"strategy_version"`
	Actor             string      `json:"actor"`
	Status            string      `json:"status"`
	Reason            string      `json:"reason"`
	CreatedAt         time.Time   `json:"created_at"`
	UpdatedAt         time.Time   `json:"updated_at"`
	ExpiresAt         *time.Time  `json:"expires_at"`
	Plan              *DemoPlan   `json:"plan"`
	Checks            []DemoCheck `json:"checks"`
	BrokerOrderID     *int64      `json:"broker_order_id"`
	BrokerStatusID    *int        `json:"broker_status_id"`
	BrokerPositionIDs []int64     `json:"broker_position_ids"`
	Events            []DemoEvent `json:"events"`
}
type demoArtifact struct {
	Source string
	Raw    []byte
}
type demoEvaluation struct {
	Client    *etoro.Client
	Plan      *DemoPlan
	Checks    []DemoCheck
	Artifacts []demoArtifact
	Reason    string
}

func (v *demoEvaluation) check(name string, passed bool, detail string) bool {
	v.Checks = append(v.Checks, DemoCheck{Name: name, Passed: passed, Detail: detail})
	if !passed && v.Reason == "" {
		v.Reason = detail
	}
	return passed
}
func (v *demoEvaluation) capture(source string, raw []byte) {
	if len(raw) > 0 {
		v.Artifacts = append(v.Artifacts, demoArtifact{Source: source, Raw: raw})
	}
}
func recentProviderTime(at, now time.Time, maxAge time.Duration) bool {
	age := now.Sub(at)
	return age >= -10*time.Second && age <= maxAge
}

func (b *BrokerService) demoClient() (*etoro.Client, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bound["demo"], b.demoExecutionEnabled
}

// evaluate checks current provider state. It does not submit an order.
func (b *BrokerService) evaluateDemo(ctx context.Context) demoEvaluation {
	v := demoEvaluation{Checks: []DemoCheck{}, Artifacts: []demoArtifact{}}
	client, enabled := b.demoClient()
	if !v.check("demo_scope", client != nil, "A schema-valid Demo key must be connected before this strategy can run.") {
		return v
	}
	v.Client = client
	if !v.check("demo_execution_switch", enabled, "The private Demo execution switch is off.") {
		return v
	}
	rawAccount, account, err := client.FetchAggregate(ctx, "demo")
	v.capture(etoro.DemoAggregatePath, rawAccount)
	if !v.check("account_read", err == nil, "A fresh official Demo account read is required.") {
		return v
	}
	now := time.Now().UTC()
	if !v.check("account_freshness", recentProviderTime(account.ProviderAt, now, 2*time.Minute), "The Demo account snapshot is stale or has an invalid provider time.") {
		return v
	}
	if !v.check("account_currency", account.Currency == "USD", "The strategy requires a USD account.") {
		return v
	}
	var identity struct {
		CID json.Number `json:"cid"`
	}
	identityErr := json.Unmarshal(rawAccount, &identity)
	cid, parseErr := strconv.ParseInt(string(identity.CID), 10, 64)
	if !v.check("account_identity", identityErr == nil && parseErr == nil && cid > 0, "A stable Demo provider account ID is required to bind the reviewed plan.") {
		return v
	}
	mac := hmac.New(sha256.New, []byte(b.token))
	_, _ = mac.Write([]byte("etoro:demo:" + strconv.FormatInt(cid, 10)))
	accountFingerprint := hex.EncodeToString(mac.Sum(nil))
	if !v.check("cash_budget", account.AvailableCash.GreaterThanOrEqual(demoMinimumCash) && account.TotalValue.GreaterThanOrEqual(demoAmount.Mul(decimal.NewFromInt(100))), "Available cash must cover $500 and the $100 order must stay within 1% of provider-reported account value.") {
		return v
	}
	if !v.check("aggregate_empty", len(account.Instruments) == 0 && account.MirrorCount == 0, "This one-time probe requires an account without existing holdings or copy portfolios.") {
		return v
	}
	rawBook, book, err := client.FetchDemoBook(ctx)
	v.capture(etoro.DemoPnLPath, rawBook)
	if !v.check("pending_order_read", err == nil, "The official Demo order and position read is required.") {
		return v
	}
	if !v.check("no_existing_orders", book.OpenPositionCount == 0 && book.PendingOrderCount == 0, "Existing positions or pending orders block this one-time probe.") {
		return v
	}
	rawInstrument, instrument, err := client.FindInstrument(ctx, "BTC")
	v.capture("/api/v1/market-data/search", rawInstrument)
	if !v.check("instrument_lookup", err == nil && instrument.Symbol == "BTC" && instrument.ID > 0, "An exact BTC instrument mapping is required.") {
		return v
	}
	if !v.check("instrument_tradable", instrument.Tradable && instrument.Buyable, "BTC must be currently tradable and enabled for buying.") {
		return v
	}
	rawQuote, quote, err := client.FetchRate(ctx, instrument.ID)
	v.capture("/api/v2/market-data/rates", rawQuote)
	if !v.check("quote_read", err == nil, "A valid eToro bid and ask quote is required.") {
		return v
	}
	now = time.Now().UTC()
	if !v.check("realtime_quote", quote.QuoteType == "realtime" && recentProviderTime(quote.At, now, 30*time.Second), "The BTC quote must be realtime and no more than 30 seconds old.") {
		return v
	}
	spread := quote.Ask.Sub(quote.Bid).Div(quote.Bid).Mul(decimal.NewFromInt(10000))
	if !v.check("spread_cap", spread.LessThanOrEqual(demoSpreadCapBps), "The bid/ask spread must be no wider than 50 basis points.") {
		return v
	}
	rawEligibility, eligibility, err := client.FetchDemoEligibility(ctx, instrument.ID)
	v.capture(etoro.DemoEligibilityPath, rawEligibility)
	if !v.check("eligibility_read", err == nil, "The account-specific Demo trading eligibility check is required.") {
		return v
	}
	if !v.check("unlevered_real", eligibility.CanOpen && eligibility.Symbol == "BTC" && eligibility.RealLongUnlevered && eligibility.MinExposure.LessThanOrEqual(demoAmount) && eligibility.MinPositionAmount.LessThanOrEqual(demoAmount), "The account must allow a $100 unlevered real BTC buy.") {
		return v
	}
	rawCosts, costs, err := client.FetchDemoCosts(ctx, instrument.ID, demoAmount)
	v.capture(etoro.DemoCostsPath, rawCosts)
	if !v.check("cost_read", err == nil, "An official Demo what-if cost estimate is required.") {
		return v
	}
	now = time.Now().UTC()
	if !v.check("cost_freshness", recentProviderTime(costs.UpdatedAt, now, 2*time.Minute), "The Demo cost estimate must be no more than two minutes old.") {
		return v
	}
	immediate := decimal.Zero
	knownCosts := true
	noOngoingCosts := true
	for _, cost := range costs.Items {
		switch cost.Type {
		case "markup", "marketSpread", "transactionFee", "sdrt":
			immediate = immediate.Add(cost.Value)
		case "overnightFee", "overWeekendFee":
			if !cost.Value.IsZero() {
				noOngoingCosts = false
			}
		default:
			knownCosts = false
		}
	}
	if !v.check("cost_types", knownCosts && noOngoingCosts, "Unknown or nonzero ongoing costs block this simple unlevered strategy.") {
		return v
	}
	if !v.check("cost_cap", immediate.LessThanOrEqual(demoCostCap), "Estimated upfront costs must not exceed $2 (2% of the order).") {
		return v
	}
	limit := quote.Ask.Mul(demoLimitMultiplier).RoundUp(2)
	digests := map[string]string{}
	for _, artifact := range v.Artifacts {
		digests[artifact.Source] = etoro.Digest(artifact.Raw)
	}
	v.Plan = &DemoPlan{Symbol: "BTC", AccountFingerprint: accountFingerprint, InstrumentID: instrument.ID, AmountUSD: demoAmount, OrderType: "limitIOC", SettlementType: "real", Leverage: 1, Bid: quote.Bid, Ask: quote.Ask, LimitRate: limit, SpreadBps: spread.Round(4), QuoteAt: quote.At, QuoteTimeAssumedUTC: quote.TimeAssumedUTC, EstimatedUpfrontCostUSD: immediate, SourceSHA256: digests}
	v.Reason = "All Demo account, market, eligibility, and cost checks passed. The price-limited order is ready for operator submission."
	return v
}

func scanDemoRun(row pgx.Row) (DemoStrategyRun, error) {
	var out DemoStrategyRun
	var planRaw, checksRaw []byte
	err := row.Scan(&out.ID, &out.IdempotencyKey, &out.StrategyVersion, &out.Actor, &out.Status, &out.Reason, &out.CreatedAt, &out.UpdatedAt, &out.ExpiresAt, &planRaw, &checksRaw, &out.BrokerOrderID, &out.BrokerStatusID, &out.BrokerPositionIDs)
	if err != nil {
		return out, err
	}
	if planRaw != nil {
		if err = json.Unmarshal(planRaw, &out.Plan); err != nil {
			return out, err
		}
	}
	if err = json.Unmarshal(checksRaw, &out.Checks); err != nil {
		return out, err
	}
	out.Events = []DemoEvent{}
	return out, nil
}

const demoRunColumns = `SELECT id::text,idempotency_key,strategy_version,actor,status,reason,created_at,updated_at,expires_at,plan,checks,broker_order_id,broker_status_id,broker_position_ids FROM demo_strategy_runs`

func (b *BrokerService) DemoRun(ctx context.Context, id string) (DemoStrategyRun, error) {
	out, err := scanDemoRun(b.DB.QueryRow(ctx, demoRunColumns+` WHERE id=$1`, id))
	if err != nil {
		return out, err
	}
	rows, err := b.DB.Query(ctx, `SELECT at,kind,summary,http_status FROM demo_strategy_events WHERE run_id=$1 ORDER BY at,id`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var event DemoEvent
		if err = rows.Scan(&event.At, &event.Kind, &event.Summary, &event.HTTPStatus); err != nil {
			return out, err
		}
		out.Events = append(out.Events, event)
	}
	return out, rows.Err()
}
func (b *BrokerService) DemoRuns(ctx context.Context) ([]DemoStrategyRun, error) {
	rows, err := b.DB.Query(ctx, demoRunColumns+` ORDER BY created_at DESC LIMIT 30`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DemoStrategyRun{}
	for rows.Next() {
		run, err := scanDemoRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		row, err := b.DemoRun(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Events = row.Events
	}
	return out, nil
}

func (b *BrokerService) saveArtifacts(ctx context.Context, tx pgx.Tx, runID, phase string, items []demoArtifact) error {
	for _, artifact := range items {
		if len(artifact.Raw) == 0 {
			continue
		}
		nonce, ciphertext, err := b.encrypt(artifact.Raw)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO demo_strategy_artifacts(id,run_id,phase,source,response_sha256,nonce,ciphertext) VALUES($1,$2,$3,$4,$5,$6,$7)`, newID(), runID, phase, artifact.Source, etoro.Digest(artifact.Raw), nonce, ciphertext); err != nil {
			return err
		}
	}
	return nil
}
func addDemoEvent(ctx context.Context, tx pgx.Tx, runID, kind, summary string, httpStatus int) error {
	_, err := tx.Exec(ctx, `INSERT INTO demo_strategy_events(id,run_id,kind,summary,http_status) VALUES($1,$2,$3,$4,$5)`, newID(), runID, kind, summary, httpStatus)
	return err
}

func (b *BrokerService) DemoPreview(ctx context.Context, key string) (DemoStrategyRun, error) {
	existing, err := scanDemoRun(b.DB.QueryRow(ctx, demoRunColumns+` WHERE idempotency_key=$1`, key))
	if err == nil {
		return b.DemoRun(ctx, existing.ID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DemoStrategyRun{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	evaluation := b.evaluateDemo(requestCtx)
	cancel()
	id := newID()
	status := "hold"
	var expiry *time.Time
	if evaluation.Plan != nil {
		status = "ready"
		at := time.Now().UTC().Add(60 * time.Second)
		expiry = &at
	}
	checks, err := json.Marshal(evaluation.Checks)
	if err != nil {
		return DemoStrategyRun{}, err
	}
	var plan []byte
	if evaluation.Plan != nil {
		plan, err = json.Marshal(evaluation.Plan)
		if err != nil {
			return DemoStrategyRun{}, err
		}
	}
	tx, err := b.DB.Begin(ctx)
	if err != nil {
		return DemoStrategyRun{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `INSERT INTO demo_strategy_runs(id,idempotency_key,strategy_version,actor,status,reason,expires_at,plan,checks) VALUES($1,$2,$3,'operator-token',$4,$5,$6,$7,$8) ON CONFLICT(idempotency_key) DO NOTHING`, id, key, DemoProbeVersion, status, evaluation.Reason, expiry, plan, checks)
	if err != nil {
		return DemoStrategyRun{}, err
	}
	if tag.RowsAffected() == 0 {
		if err = tx.Commit(ctx); err != nil {
			return DemoStrategyRun{}, err
		}
		stored, err := scanDemoRun(b.DB.QueryRow(ctx, demoRunColumns+` WHERE idempotency_key=$1`, key))
		if err != nil {
			return DemoStrategyRun{}, err
		}
		return b.DemoRun(ctx, stored.ID)
	}
	if err = b.saveArtifacts(ctx, tx, id, "preview", evaluation.Artifacts); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = addDemoEvent(ctx, tx, id, "decision", strings.ToUpper(status)+": "+evaluation.Reason, 0); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DemoStrategyRun{}, err
	}
	return b.DemoRun(ctx, id)
}

func (b *BrokerService) holdDemo(ctx context.Context, runID, reason string, evaluation demoEvaluation) (DemoStrategyRun, error) {
	tx, err := b.DB.Begin(ctx)
	if err != nil {
		return DemoStrategyRun{}, err
	}
	defer tx.Rollback(ctx)
	checks, err := json.Marshal(evaluation.Checks)
	if err != nil {
		return DemoStrategyRun{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE demo_strategy_runs SET status='hold',reason=$2,checks=$3,updated_at=now() WHERE id=$1 AND status='ready'`, runID, reason, checks); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = b.saveArtifacts(ctx, tx, runID, "recheck", evaluation.Artifacts); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = addDemoEvent(ctx, tx, runID, "hold", reason, 0); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DemoStrategyRun{}, err
	}
	return b.DemoRun(ctx, runID)
}

func (b *BrokerService) DemoExecute(ctx context.Context, id string) (DemoStrategyRun, error) {
	run, err := b.DemoRun(ctx, id)
	if err != nil {
		return run, err
	}
	if run.Status != "ready" || run.ExpiresAt == nil || time.Now().After(*run.ExpiresAt) || run.Plan == nil {
		return run, ErrDemoIntentNotReady
	}
	requestCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	evaluation := b.evaluateDemo(requestCtx)
	cancel()
	if evaluation.Plan == nil {
		return b.holdDemo(ctx, id, evaluation.Reason, evaluation)
	}
	if evaluation.Plan.AccountFingerprint != run.Plan.AccountFingerprint || evaluation.Plan.InstrumentID != run.Plan.InstrumentID || evaluation.Plan.Ask.GreaterThan(run.Plan.LimitRate) {
		return b.holdDemo(ctx, id, "The provider account, instrument mapping or quote changed beyond the reviewed plan. Preview a new decision.", evaluation)
	}
	// Persist the intent and its source evidence before calling the broker. The
	// unique active index serializes unresolved submissions across requests.
	tx, err := b.DB.Begin(ctx)
	if err != nil {
		return DemoStrategyRun{}, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE demo_strategy_runs SET status='submitting',reason='A single Demo limitIOC order is being submitted.',request_id=$1,updated_at=now() WHERE id=$1 AND status='ready' AND expires_at>now()`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return run, ErrDemoActiveIntent
		}
		return DemoStrategyRun{}, err
	}
	if tag.RowsAffected() != 1 {
		return run, ErrDemoIntentNotReady
	}
	if err = b.saveArtifacts(ctx, tx, id, "recheck", evaluation.Artifacts); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = addDemoEvent(ctx, tx, id, "submit_intent", "The operator submitted the reviewed $100 Demo BTC limitIOC plan. Broker request ID equals the decision ID.", 0); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DemoStrategyRun{}, err
	}
	// Do not abandon persistence if the HTTP caller disconnects after eToro has
	// accepted the request. An ambiguous outcome remains frozen for lookup.
	submitCtx, submitCancel := context.WithTimeout(context.Background(), 25*time.Second)
	raw, httpStatus, receipt, submitErr := evaluation.Client.SubmitDemoLimitIOC(submitCtx, id, run.Plan.InstrumentID, run.Plan.AmountUSD, run.Plan.LimitRate)
	submitCancel()
	persistCtx, persistCancel := context.WithTimeout(context.Background(), 10*time.Second)
	state, reason, event := "unknown", "Broker submission outcome is uncertain; reconcile by request ID before any further order.", "submission_unknown"
	if submitErr == nil {
		state, reason, event = "accepted", "eToro accepted the order for processing; a fill is not yet confirmed.", "submission_accepted"
	} else if httpStatus >= 400 && httpStatus < 500 && httpStatus != 429 {
		state, reason, event = "rejected", "eToro rejected the Demo order request before acceptance.", "submission_rejected"
	}
	err = b.recordDemoSubmit(persistCtx, id, state, reason, event, httpStatus, receipt.OrderID, raw)
	persistCancel()
	if err != nil {
		return DemoStrategyRun{}, err
	}
	if state == "accepted" {
		reconcileCtx, reconcileCancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer reconcileCancel()
		return b.DemoReconcile(reconcileCtx, id)
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	return b.DemoRun(readCtx, id)
}

func (b *BrokerService) recordDemoSubmit(ctx context.Context, id, state, reason, event string, httpStatus int, orderID int64, raw []byte) error {
	tx, err := b.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var brokerOrderID any
	if orderID > 0 {
		brokerOrderID = orderID
	}
	if _, err = tx.Exec(ctx, `UPDATE demo_strategy_runs SET status=$2,reason=$3,broker_order_id=$4,updated_at=now() WHERE id=$1 AND status='submitting'`, id, state, reason, brokerOrderID); err != nil {
		return err
	}
	if err = b.saveArtifacts(ctx, tx, id, "submit", []demoArtifact{{Source: etoro.DemoOrderPath, Raw: raw}}); err != nil {
		return err
	}
	if err = addDemoEvent(ctx, tx, id, event, reason, httpStatus); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func demoLookupState(lookup etoro.OrderLookup) (string, string) {
	switch lookup.StatusID {
	case 3:
		if len(lookup.PositionIDs) == 0 {
			return "unknown", "Broker reported Filled without a position ID; manual reconciliation required."
		}
		return "filled", "Broker lookup confirmed a filled Demo order and returned its position ID."
	case 5, 10:
		return "partially_filled", "Broker lookup reported a partial fill; further reconciliation is required."
	case 4:
		if len(lookup.PositionIDs) > 0 {
			return "unknown", "Broker reported rejection with position executions; manual reconciliation required."
		}
		reason := "Broker lookup confirmed the Demo order was rejected."
		if lookup.ErrorCode != "" && lookup.ErrorCode != "0" {
			reason += " eToro error code: " + lookup.ErrorCode + "."
		}
		return "rejected", reason
	case 1, 2, 11, 12:
		return "accepted", "Broker lookup shows the Demo order is still in flight."
	default:
		return "unknown", "Broker returned an unrecognized order status; manual reconciliation required."
	}
}

func lookupDemoOrderWithFallback(ctx context.Context, client *etoro.Client, referenceID string, brokerOrderID *int64) ([]byte, int, etoro.OrderLookup, bool, error) {
	raw, httpStatus, lookup, err := client.LookupDemoOrder(ctx, referenceID)
	var apiErr *etoro.APIError
	if err != nil && brokerOrderID != nil && errors.As(err, &apiErr) && apiErr.Status == 404 {
		raw, httpStatus, lookup, err = client.LookupDemoOrderByID(ctx, *brokerOrderID)
		return raw, httpStatus, lookup, true, err
	}
	return raw, httpStatus, lookup, false, err
}

func (b *BrokerService) DemoReconcile(ctx context.Context, id string) (DemoStrategyRun, error) {
	run, err := b.DemoRun(ctx, id)
	if err != nil {
		return run, err
	}
	if run.Status != "submitting" && run.Status != "accepted" && run.Status != "unknown" && run.Status != "partially_filled" {
		return run, nil
	}
	// A POST may still be on the wire. Only the submitter records its immediate
	// outcome; a stale submitting intent is reconciled after its timeout window.
	if run.Status == "submitting" && time.Since(run.UpdatedAt) < 30*time.Second {
		return run, nil
	}
	client, _ := b.demoClient()
	if client == nil {
		return run, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	raw, httpStatus, lookup, lookupByOrderID, lookupErr := lookupDemoOrderWithFallback(lookupCtx, client, id, run.BrokerOrderID)
	cancel()
	if lookupErr != nil {
		if run.Status == "submitting" {
			return b.markDemoUnknown(ctx, id, httpStatus, raw)
		}
		return run, nil
	}
	state, reason := demoLookupState(lookup)
	if run.BrokerOrderID != nil && *run.BrokerOrderID != lookup.OrderID {
		state, reason = "unknown", "Broker lookup order ID does not match the submitted order; manual reconciliation required."
	}
	if run.Status == state && run.BrokerStatusID != nil && *run.BrokerStatusID == lookup.StatusID && run.BrokerOrderID != nil && *run.BrokerOrderID == lookup.OrderID {
		return run, nil
	}
	tx, err := b.DB.Begin(ctx)
	if err != nil {
		return DemoStrategyRun{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE demo_strategy_runs SET status=$2,reason=$3,broker_order_id=$4,broker_status_id=$5,broker_position_ids=$6,updated_at=now() WHERE id=$1 AND status IN ('submitting','accepted','unknown','partially_filled')`, id, state, reason, lookup.OrderID, lookup.StatusID, lookup.PositionIDs); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = b.saveArtifacts(ctx, tx, id, "lookup", []demoArtifact{{Source: etoro.DemoOrderLookupPath, Raw: raw}}); err != nil {
		return DemoStrategyRun{}, err
	}
	eventReason := reason
	if lookupByOrderID {
		eventReason = "Reference lookup returned 404; order-ID lookup resolved the broker state. " + reason
	}
	if err = addDemoEvent(ctx, tx, id, "broker_lookup", eventReason, httpStatus); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DemoStrategyRun{}, err
	}
	if state == "filled" || state == "partially_filled" {
		b.syncDemoAfterOrder(client)
	}
	return b.DemoRun(ctx, id)
}

func (b *BrokerService) markDemoUnknown(ctx context.Context, id string, httpStatus int, raw []byte) (DemoStrategyRun, error) {
	tx, err := b.DB.Begin(ctx)
	if err != nil {
		return DemoStrategyRun{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE demo_strategy_runs SET status='unknown',reason='Broker lookup did not resolve the submission; no retry is allowed.',updated_at=now() WHERE id=$1 AND status='submitting'`, id); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = b.saveArtifacts(ctx, tx, id, "lookup", []demoArtifact{{Source: etoro.DemoOrderLookupPath, Raw: raw}}); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = addDemoEvent(ctx, tx, id, "broker_lookup_unknown", "Broker lookup did not resolve the submission; no retry is allowed.", httpStatus); err != nil {
		return DemoStrategyRun{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DemoStrategyRun{}, err
	}
	return b.DemoRun(ctx, id)
}

func (b *BrokerService) syncDemoAfterOrder(client *etoro.Client) {
	requestCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	raw, snapshot, err := client.FetchAggregate(requestCtx, "demo")
	cancel()
	if err != nil {
		return
	}
	storageCtx, storageCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer storageCancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = b.recordSuccess(storageCtx, client, "demo", raw, snapshot)
}

func (b *BrokerService) ReconcilePendingDemo(ctx context.Context) error {
	rows, err := b.DB.Query(ctx, `SELECT id::text FROM demo_strategy_runs WHERE status IN ('submitting','accepted','unknown','partially_filled') ORDER BY created_at LIMIT 10`)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = b.DemoReconcile(ctx, id); err != nil {
			return fmt.Errorf("reconcile demo order %s: %w", id, err)
		}
	}
	return nil
}

func (p *Platform) DemoPreview(ctx context.Context, key string) (DemoStrategyRun, error) {
	if p.Broker == nil {
		return DemoStrategyRun{}, errors.New("eToro Demo is not configured")
	}
	return p.Broker.DemoPreview(ctx, key)
}
func (p *Platform) DemoExecute(ctx context.Context, id string) (DemoStrategyRun, error) {
	if p.Broker == nil {
		return DemoStrategyRun{}, errors.New("eToro Demo is not configured")
	}
	return p.Broker.DemoExecute(ctx, id)
}
func (p *Platform) DemoReconcile(ctx context.Context, id string) (DemoStrategyRun, error) {
	if p.Broker == nil {
		return DemoStrategyRun{}, errors.New("eToro Demo is not configured")
	}
	return p.Broker.DemoReconcile(ctx, id)
}
func (p *Platform) DemoRuns(ctx context.Context) ([]DemoStrategyRun, error) {
	if p.Broker == nil {
		return []DemoStrategyRun{}, nil
	}
	return p.Broker.DemoRuns(ctx)
}
func (p *Platform) DemoRun(ctx context.Context, id string) (DemoStrategyRun, error) {
	if p.Broker == nil {
		return DemoStrategyRun{}, pgx.ErrNoRows
	}
	return p.Broker.DemoRun(ctx, id)
}
