package httpapi

import (
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
	Enqueue(context.Context, string) (string, error)
	Runs(context.Context) ([]platform.Run, error)
	Run(context.Context, string) (platform.Run, error)
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
		var payload map[string]json.RawMessage
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&payload); err != nil || payload == nil || len(payload) != 0 {
			write(w, 400, map[string]string{"error": "expected_empty_object"})
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			write(w, 400, map[string]string{"error": "expected_one_object"})
			return
		}
		id, err := p.Enqueue(r.Context(), key)
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
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
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
