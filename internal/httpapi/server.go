package httpapi

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/Nielk74/mfd/internal/lab"
	"github.com/Nielk74/mfd/internal/platform"
	"github.com/jackc/pgx/v5"
)

//go:embed web/*
var web embed.FS

// Revision is set at build time so deployment checks identify the running binary.
var Revision = "development"

var keyPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,100}$`)
var idPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type Store interface {
	Enqueue(context.Context, string, lab.Experiment) (string, error)
	Runs(context.Context) ([]platform.Run, error)
	Run(context.Context, string) (platform.Run, error)
	Operations(context.Context) (platform.Operations, error)
	BrokerStatus(context.Context) (platform.BrokerStatus, error)
	BrokerAuthorized(string) bool
	BrokerHistory(context.Context) ([]platform.BrokerSnapshot, error)
	BrokerSync(context.Context) (platform.BrokerStatus, error)
	Ready(context.Context) map[string]bool
	Metrics(context.Context) (string, error)
}

func New(p Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		write(w, 200, map[string]string{"status": "ok", "mode": "fixture", "revision": Revision})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		deps := p.Ready(ctx)
		status := 200
		for _, ok := range deps {
			if !ok {
				status = 503
			}
		}
		write(w, status, deps)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		body, err := p.Metrics(ctx)
		if err != nil {
			failure(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = io.WriteString(w, body)
	})
	mux.HandleFunc("GET /api/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		runs, err := p.Runs(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]any{"runs": runs, "limit": 50})
	})
	mux.HandleFunc("GET /api/v1/operations", func(w http.ResponseWriter, r *http.Request) {
		ops, err := p.Operations(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, ops)
	})
	mux.HandleFunc("GET /api/v1/etoro/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := p.BrokerStatus(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, status)
	})
	operator := func(w http.ResponseWriter, r *http.Request) bool {
		if !p.BrokerAuthorized(r.Header.Get("X-MFD-Operator-Token")) {
			write(w, 401, map[string]string{"error": "operator_token_required"})
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/v1/etoro/snapshots", func(w http.ResponseWriter, r *http.Request) {
		if !operator(w, r) {
			return
		}
		items, err := p.BrokerHistory(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, map[string]any{"snapshots": items, "limit": 30})
	})
	mux.HandleFunc("POST /api/v1/etoro/sync", func(w http.ResponseWriter, r *http.Request) {
		if !operator(w, r) {
			return
		}
		status, err := p.BrokerSync(r.Context())
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, status)
	})
	mux.HandleFunc("GET /api/v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !idPattern.MatchString(id) {
			write(w, 400, map[string]string{"error": "invalid_run_id"})
			return
		}
		run, err := p.Run(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			write(w, 404, map[string]string{"error": "run_not_found"})
			return
		}
		if err != nil {
			failure(w, err)
			return
		}
		write(w, 200, run)
	})
	mux.HandleFunc("POST /api/v1/replays", func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if !keyPattern.MatchString(key) {
			write(w, 400, map[string]string{"error": "idempotency_key_required", "detail": "Use 8–100 letters, digits, hyphens or underscores."})
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			write(w, 415, map[string]string{"error": "application_json_required"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		decoder := json.NewDecoder(r.Body)
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || len(raw) == 0 || raw[0] != '{' {
			write(w, 400, map[string]string{"error": "invalid_experiment", "detail": "Use name, final_shock_bps and review_threshold_bps only."})
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			write(w, 400, map[string]string{"error": "expected_one_object"})
			return
		}
		var payload lab.Experiment
		strict := json.NewDecoder(bytes.NewReader(raw))
		strict.DisallowUnknownFields()
		if err := strict.Decode(&payload); err != nil {
			write(w, 400, map[string]string{"error": "invalid_experiment", "detail": "Use name, final_shock_bps and review_threshold_bps only."})
			return
		}
		if _, err := lab.NormalizeExperiment(payload); err != nil {
			write(w, 400, map[string]string{"error": "invalid_experiment", "detail": err.Error()})
			return
		}
		id, err := p.Enqueue(r.Context(), key, payload)
		if errors.Is(err, platform.ErrIdempotencyConflict) {
			write(w, 409, map[string]string{"error": "idempotency_key_reused", "detail": "Use a new key for a different experiment."})
			return
		}
		if err != nil {
			failure(w, err)
			return
		}
		w.Header().Set("Location", "/api/v1/runs/"+id)
		write(w, 202, map[string]string{"run_id": id, "status_url": "/api/v1/runs/" + id})
	})
	files, _ := fs.Sub(web, "web")
	mux.Handle("GET /", http.FileServer(http.FS(files)))
	// Go's cross-origin protection rejects browser cross-site unsafe requests;
	// command-line/agent callers use the same API. This is not authentication.
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		timeout := 10 * time.Second
		if r.URL.Path == "/api/v1/etoro/sync" {
			timeout = 35 * time.Second
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		protected.ServeHTTP(w, r.WithContext(ctx))
	})
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, err error) {
	slog.Error("request failed", "error", err)
	write(w, 503, map[string]string{"error": "storage_unavailable", "detail": "Retry using the same idempotency key."})
}
