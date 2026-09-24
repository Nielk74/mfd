package httpapi

import (
	"context"
	"github.com/Nielk74/mfd/internal/lab"
	"github.com/Nielk74/mfd/internal/platform"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type stub struct {
	calls        int
	syncDeadline time.Time
}

func (s *stub) Enqueue(context.Context, string, lab.Experiment) (string, error) {
	s.calls++
	return "00000000-0000-4000-8000-000000000000", nil
}
func (s *stub) Runs(context.Context) ([]platform.Run, error)      { return []platform.Run{}, nil }
func (s *stub) Run(context.Context, string) (platform.Run, error) { return platform.Run{}, nil }
func (s *stub) Operations(context.Context) (platform.Operations, error) {
	return platform.Operations{}, nil
}
func (s *stub) BrokerStatus(context.Context) (platform.BrokerStatus, error) {
	return platform.BrokerStatus{}, nil
}
func (s *stub) BrokerAuthorized(token string) bool                               { return token == "test-operator-token" }
func (s *stub) BrokerHistory(context.Context) ([]platform.BrokerSnapshot, error) { return nil, nil }
func (s *stub) BrokerSync(ctx context.Context) (platform.BrokerStatus, error) {
	s.syncDeadline, _ = ctx.Deadline()
	return platform.BrokerStatus{}, nil
}
func (s *stub) Ready(context.Context) map[string]bool   { return map[string]bool{"postgres": true} }
func (s *stub) Metrics(context.Context) (string, error) { return "", nil }
func TestReplayBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, key, body, origin string
		want                    int
	}{
		{"valid", "test-key-123", "{}", "", 202},
		{"custom experiment", "test-key-123", `{"name":"Shock","final_shock_bps":{"MSFT":1000},"review_threshold_bps":200}`, "", 202},
		{"missing idempotency", "", "{}", "", 400},
		{"unexpected option", "test-key-123", `{"mode":"live"}`, "", 400},
		{"invalid symbol", "test-key-123", `{"final_shock_bps":{"BTC":100}}`, "", 400},
		{"invalid shock", "test-key-123", `{"final_shock_bps":{"MSFT":10000}}`, "", 400},
		{"multiple objects", "test-key-123", "{} {}", "", 400},
		{"null body", "test-key-123", "null", "", 400},
		{"cross origin", "test-key-123", "{}", "https://external.example", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &stub{}
			r := httptest.NewRequest("POST", "http://localhost/api/v1/replays", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", tc.key)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			New(s).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tc.want != http.StatusAccepted && s.calls != 0 {
				t.Fatal("invalid request enqueued")
			}
		})
	}
}
func TestBrokerAccountRequiresOperatorToken(t *testing.T) {
	for _, path := range []string{"/api/v1/etoro/snapshots", "/api/v1/etoro/sync"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/sync") {
			method = http.MethodPost
		}
		w := httptest.NewRecorder()
		New(&stub{}).ServeHTTP(w, httptest.NewRequest(method, "http://localhost"+path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s returned %d", path, w.Code)
		}
	}
}
func TestBrokerSyncAllowsProviderDeadline(t *testing.T) {
	s := &stub{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "http://localhost/api/v1/etoro/sync", nil)
	r.Header.Set("X-MFD-Operator-Token", "test-operator-token")
	start := time.Now()
	New(s).ServeHTTP(w, r)
	if w.Code != http.StatusOK || s.syncDeadline.Sub(start) < 25*time.Second {
		t.Fatalf("provider sync deadline too short: status=%d window=%s", w.Code, s.syncDeadline.Sub(start))
	}
}
