package httpapi

import (
	"context"
	"github.com/Nielk74/mfd/internal/platform"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stub struct{ calls int }

func (s *stub) Enqueue(context.Context, string) (string, error) {
	s.calls++
	return "00000000-0000-4000-8000-000000000000", nil
}
func (s *stub) Runs(context.Context) ([]platform.Run, error)      { return []platform.Run{}, nil }
func (s *stub) Run(context.Context, string) (platform.Run, error) { return platform.Run{}, nil }
func (s *stub) Ready(context.Context) map[string]bool             { return map[string]bool{"postgres": true} }
func (s *stub) Metrics(context.Context) (string, error)           { return "", nil }
func TestReplayBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, key, body, origin string
		want                    int
	}{
		{"valid", "test-key-123", "{}", "", 202},
		{"missing idempotency", "", "{}", "", 400},
		{"unexpected option", "test-key-123", `{"mode":"live"}`, "", 400},
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
