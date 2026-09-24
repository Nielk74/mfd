package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Nielk74/mfd/internal/lab"
	"github.com/jackc/pgx/v5"
)

type Job struct {
	RunID       string     `json:"run_id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

type Operations struct {
	GeneratedAt  time.Time       `json:"generated_at"`
	Dependencies map[string]bool `json:"dependencies"`
	Database     struct {
		Runs          map[string]int64 `json:"runs"`
		StorageBytes  int64            `json:"storage_bytes"`
		OutboxPending int64            `json:"outbox_pending"`
		Jobs          []Job            `json:"recent_jobs"`
	} `json:"database"`
	Queue struct {
		Connected   bool   `json:"connected"`
		Stream      string `json:"stream"`
		Messages    uint64 `json:"messages"`
		Bytes       uint64 `json:"bytes"`
		Consumers   int    `json:"consumers"`
		Pending     uint64 `json:"pending"`
		AckPending  int    `json:"ack_pending"`
		Redelivered int    `json:"redelivered"`
		Error       string `json:"error,omitempty"`
	} `json:"queue"`
}

func (p *Platform) Operations(ctx context.Context) (Operations, error) {
	o := Operations{GeneratedAt: time.Now().UTC(), Dependencies: p.Ready(ctx)}
	o.Database.Runs = map[string]int64{}
	o.Database.Jobs = []Job{}
	rows, err := p.DB.Query(ctx, "SELECT status,count(*) FROM runs GROUP BY status")
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var status string
		var count int64
		if err = rows.Scan(&status, &count); err != nil {
			rows.Close()
			return o, err
		}
		o.Database.Runs[status] = count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return o, err
	}
	if err = p.DB.QueryRow(ctx, `SELECT pg_total_relation_size('runs') + pg_total_relation_size('outbox'),count(*) FROM outbox WHERE published_at IS NULL`).Scan(&o.Database.StorageBytes, &o.Database.OutboxPending); err != nil {
		return o, err
	}
	rows, err = p.DB.Query(ctx, `SELECT o.run_id::text,r.experiment->>'name',r.status,o.created_at,o.published_at,r.finished_at FROM outbox o JOIN runs r ON r.id=o.run_id ORDER BY o.created_at DESC LIMIT 20`)
	if err != nil {
		return o, err
	}
	for rows.Next() {
		var j Job
		if err = rows.Scan(&j.RunID, &j.Name, &j.Status, &j.CreatedAt, &j.PublishedAt, &j.FinishedAt); err != nil {
			rows.Close()
			return o, err
		}
		o.Database.Jobs = append(o.Database.Jobs, j)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return o, err
	}
	o.Queue.Connected = p.NATS.IsConnected()
	o.Queue.Stream = "MFD_JOBS"
	if o.Queue.Connected {
		info, infoErr := p.JS.StreamInfo("MFD_JOBS")
		if infoErr != nil {
			o.Queue.Error = "stream unavailable"
		} else {
			o.Queue.Messages = info.State.Msgs
			o.Queue.Bytes = info.State.Bytes
			o.Queue.Consumers = info.State.Consumers
			consumer, consumerErr := p.JS.ConsumerInfo("MFD_JOBS", "replay-workers")
			if consumerErr != nil {
				o.Queue.Error = "consumer unavailable"
			} else {
				o.Queue.Pending = consumer.NumPending
				o.Queue.AckPending = consumer.NumAckPending
				o.Queue.Redelivered = consumer.NumRedelivered
			}
		}
	} else {
		o.Queue.Error = "NATS disconnected"
	}
	return o, nil
}

func (p *Platform) productMetrics(ctx context.Context) (string, error) {
	var raw []byte
	var duration float64
	err := p.DB.QueryRow(ctx, `SELECT result,EXTRACT(EPOCH FROM finished_at-created_at) FROM runs WHERE status='completed' ORDER BY finished_at DESC LIMIT 1`).Scan(&raw, &duration)
	if err != nil {
		// No fixture run yet is an empty metric series, not a fake zero NAV.
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	var result lab.Result
	if err = json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	latest := map[string]lab.Snapshot{}
	for _, snap := range result.Snapshots {
		latest[snap.Sleeve] = snap
	}
	var b strings.Builder
	b.WriteString("# HELP mfd_fixture_latest_equity_usd Latest completed artificial experiment equity in USD.\n# TYPE mfd_fixture_latest_equity_usd gauge\n")
	totalEquity, totalPnL := 0.0, 0.0
	for _, sleeve := range []string{"core", "experiment"} {
		if s, ok := latest[sleeve]; ok {
			equity, _ := s.Equity.Float64()
			pnl, _ := s.UnrealizedPnL.Float64()
			totalEquity += equity
			totalPnL += pnl
			fmt.Fprintf(&b, "mfd_fixture_latest_equity_usd{scope=%q} %g\n", sleeve, equity)
		}
	}
	fmt.Fprintf(&b, "mfd_fixture_latest_equity_usd{scope=\"total\"} %g\n", totalEquity)
	b.WriteString("# HELP mfd_fixture_latest_unrealized_pnl_usd Latest completed artificial experiment mark to cost.\n# TYPE mfd_fixture_latest_unrealized_pnl_usd gauge\n")
	for _, sleeve := range []string{"core", "experiment"} {
		if s, ok := latest[sleeve]; ok {
			pnl, _ := s.UnrealizedPnL.Float64()
			fmt.Fprintf(&b, "mfd_fixture_latest_unrealized_pnl_usd{scope=%q} %g\n", sleeve, pnl)
		}
	}
	fmt.Fprintf(&b, "mfd_fixture_latest_unrealized_pnl_usd{scope=\"total\"} %g\n", totalPnL)
	counts := map[string]int{"hold": 0, "review": 0}
	for _, d := range result.Decisions {
		if _, ok := counts[d.Action]; ok {
			counts[d.Action]++
		}
	}
	b.WriteString("# HELP mfd_fixture_latest_decisions Latest completed artificial experiment decisions.\n# TYPE mfd_fixture_latest_decisions gauge\n")
	for _, action := range []string{"hold", "review"} {
		fmt.Fprintf(&b, "mfd_fixture_latest_decisions{action=%q} %d\n", action, counts[action])
	}
	fmt.Fprintf(&b, "# HELP mfd_latest_run_duration_seconds Time from enqueue to durable completion.\n# TYPE mfd_latest_run_duration_seconds gauge\nmfd_latest_run_duration_seconds %g\n", duration)
	return b.String(), nil
}
