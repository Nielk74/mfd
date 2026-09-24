package platform

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Nielk74/mfd/internal/lab"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

//go:embed schema.sql
var schema string

const subject = "mfd.jobs.replay.v1"

type Platform struct {
	DB    *pgxpool.Pool
	Cache *redis.Client
	NATS  *nats.Conn
	JS    nats.JetStreamContext
}
type Run struct {
	ID          string      `json:"id"`
	Status      string      `json:"status"`
	Mode        string      `json:"mode"`
	Actor       string      `json:"actor"`
	DatasetHash string      `json:"dataset_hash"`
	CreatedAt   time.Time   `json:"created_at"`
	FinishedAt  *time.Time  `json:"finished_at"`
	Result      *lab.Result `json:"result"`
	Error       *string     `json:"error"`
}

func Open(ctx context.Context, dbURL, redisAddr, natsURL string) (*Platform, error) {
	db, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, err
	}
	cache := redis.NewClient(&redis.Options{Addr: redisAddr, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	p := &Platform{DB: db, Cache: cache}
	fail := func(err error) (*Platform, error) { p.Close(); return nil, err }
	if err = db.Ping(ctx); err != nil {
		return fail(err)
	}
	if err = p.migrate(ctx); err != nil {
		return fail(err)
	}
	nc, err := nats.Connect(natsURL, nats.Name("mfd"), nats.Timeout(3*time.Second), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
	if err != nil {
		return fail(err)
	}
	p.NATS = nc
	js, err := nc.JetStream(nats.MaxWait(3 * time.Second))
	if err != nil {
		return fail(err)
	}
	p.JS = js
	if _, err = js.AddStream(&nats.StreamConfig{Name: "MFD_JOBS", Subjects: []string{subject}, Storage: nats.FileStorage, Retention: nats.WorkQueuePolicy, MaxBytes: 256 * 1024 * 1024, Discard: nats.DiscardNew}); err != nil {
		return fail(err)
	}
	return p, nil
}
func (p *Platform) Close() {
	if p.NATS != nil {
		p.NATS.Close()
	}
	if p.Cache != nil {
		_ = p.Cache.Close()
	}
	if p.DB != nil {
		p.DB.Close()
	}
}
func (p *Platform) migrate(ctx context.Context) error {
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(6431001)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_version (version integer PRIMARY KEY)"); err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_version WHERE version=1)").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err = tx.Exec(ctx, schema); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_version VALUES(1)"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (p *Platform) Enqueue(ctx context.Context, key string) (string, error) {
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	id := newID()
	tag, err := tx.Exec(ctx, "INSERT INTO runs(id,idempotency_key,status,dataset_hash) VALUES($1,$2,'queued',$3) ON CONFLICT(idempotency_key) DO NOTHING", id, key, lab.DatasetHash())
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		if err = tx.QueryRow(ctx, "SELECT id FROM runs WHERE idempotency_key=$1", key).Scan(&id); err != nil {
			return "", err
		}
	} else {
		if _, err = tx.Exec(ctx, "INSERT INTO outbox(run_id) VALUES($1)", id); err != nil {
			return "", err
		}
	}
	return id, tx.Commit(ctx)
}

const selectRun = "SELECT id,status,mode,actor,dataset_hash,created_at,finished_at,result,error FROM runs"

func scanRun(row pgx.Row) (Run, error) {
	var r Run
	var raw []byte
	err := row.Scan(&r.ID, &r.Status, &r.Mode, &r.Actor, &r.DatasetHash, &r.CreatedAt, &r.FinishedAt, &raw, &r.Error)
	if err == nil && raw != nil {
		err = json.Unmarshal(raw, &r.Result)
	}
	return r, err
}
func (p *Platform) Run(ctx context.Context, id string) (Run, error) {
	return scanRun(p.DB.QueryRow(ctx, selectRun+" WHERE id=$1", id))
}
func (p *Platform) Runs(ctx context.Context) ([]Run, error) {
	rows, err := p.DB.Query(ctx, "SELECT id,status,mode,actor,dataset_hash,created_at,finished_at,NULL::jsonb,error FROM runs ORDER BY created_at DESC LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (p *Platform) Ready(ctx context.Context) map[string]bool {
	return map[string]bool{"postgres": p.DB.Ping(ctx) == nil, "redis": p.Cache.Ping(ctx).Err() == nil, "nats": p.NATS.IsConnected() && p.NATS.FlushTimeout(time.Second) == nil}
}

// Start starts an outbox relay and a bounded pool sharing one durable consumer.
// All goroutines are joined by the returned wait function on context cancellation.
func (p *Platform) Start(ctx context.Context, workers int) (func(), error) {
	if workers < 1 || workers > 16 {
		return nil, fmt.Errorf("workers must be 1..16")
	}
	sub, err := p.JS.PullSubscribe(subject, "replay-workers", nats.BindStream("MFD_JOBS"), nats.ManualAck(), nats.AckExplicit(), nats.AckWait(30*time.Second), nats.MaxAckPending(workers))
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.dispatch(ctx); err != nil && ctx.Err() == nil {
					slog.Error("outbox dispatch failed", "error", err)
				}
			}
		}
	}()
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for ctx.Err() == nil {
				messages, err := sub.Fetch(1, nats.MaxWait(time.Second))
				if err != nil {
					if !errors.Is(err, nats.ErrTimeout) && ctx.Err() == nil {
						slog.Warn("queue fetch failed", "worker", worker, "error", err)
						select {
						case <-ctx.Done():
							return
						case <-time.After(time.Second):
						}
					}
					continue
				}
				for _, msg := range messages {
					jobCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
					err = p.process(jobCtx, string(msg.Data))
					cancel()
					if err != nil {
						slog.Error("replay deferred", "worker", worker, "error", err)
						_ = msg.NakWithDelay(5 * time.Second)
					} else {
						if err = msg.AckSync(); err != nil {
							slog.Warn("ack failed; safe to redeliver", "error", err)
						}
					}
				}
			}
		}(i)
	}
	return func() { wg.Wait() }, nil
}

func (p *Platform) dispatch(ctx context.Context) error {
	// Keep each batch small. Duplicate publishes after a crash are safe because
	// process locks the run and ignores already committed results.
	rows, err := p.DB.Query(ctx, "SELECT run_id::text FROM outbox WHERE published_at IS NULL ORDER BY created_at LIMIT 20")
	if err != nil {
		return err
	}
	var ids []string
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
		publishCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err = p.JS.Publish(subject, []byte(id), nats.MsgId(id), nats.Context(publishCtx))
		cancel()
		if err != nil {
			return err
		}
		if _, err = p.DB.Exec(ctx, "UPDATE outbox SET published_at=now() WHERE run_id=$1 AND published_at IS NULL", id); err != nil {
			return err
		}
	}
	return nil
}
func (p *Platform) process(ctx context.Context, id string) error {
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status, hash string
	if err = tx.QueryRow(ctx, "SELECT status,dataset_hash FROM runs WHERE id=$1 FOR UPDATE", id).Scan(&status, &hash); err != nil {
		return err
	}
	if status != "queued" {
		return nil
	}
	result, replayErr := lab.Replay()
	if hash != lab.DatasetHash() {
		replayErr = fmt.Errorf("queued dataset version differs from this worker")
	}
	if replayErr != nil {
		if _, err = tx.Exec(ctx, "UPDATE runs SET status='failed',finished_at=now(),error=$2 WHERE id=$1", id, replayErr.Error()); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "UPDATE runs SET status='completed',finished_at=now(),result=$2 WHERE id=$1", id, raw); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	// Cache is disposable and run-scoped; it is never the source of accounting.
	cacheCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err = p.Cache.Set(cacheCtx, "mfd:fixture:result:"+id, raw, time.Hour).Err(); err != nil {
		slog.Warn("result cache unavailable; durable result retained")
	}
	slog.Info("replay completed", "run_id", id, "decisions", len(result.Decisions))
	return nil
}

func (p *Platform) Metrics(ctx context.Context) (string, error) {
	var queued, completed, failed, pending int64
	var age float64
	err := p.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE status='queued'),count(*) FILTER(WHERE status='completed'),count(*) FILTER(WHERE status='failed'),COALESCE(EXTRACT(EPOCH FROM now()-min(created_at) FILTER(WHERE status='queued')),0) FROM runs`).Scan(&queued, &completed, &failed, &age)
	if err != nil {
		return "", err
	}
	if err = p.DB.QueryRow(ctx, "SELECT count(*) FROM outbox WHERE published_at IS NULL").Scan(&pending); err != nil {
		return "", err
	}
	return fmt.Sprintf("# HELP mfd_runs Durable replay runs by status.\n# TYPE mfd_runs gauge\nmfd_runs{status=\"queued\"} %d\nmfd_runs{status=\"completed\"} %d\nmfd_runs{status=\"failed\"} %d\n# HELP mfd_outbox_pending Unpublished durable jobs.\n# TYPE mfd_outbox_pending gauge\nmfd_outbox_pending %d\n# HELP mfd_oldest_queued_seconds Age of oldest pending run.\n# TYPE mfd_oldest_queued_seconds gauge\nmfd_oldest_queued_seconds %g\n", queued, completed, failed, pending, age), nil
}
