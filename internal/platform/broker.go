package platform

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Nielk74/mfd/internal/etoro"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var brokerEnvironments = []string{"demo", "real"}
var errBrokerCredentialConflict = errors.New("multiple eToro keys resolved to one environment")

func validBrokerEnvironment(environment string) bool {
	return environment == "demo" || environment == "real"
}
func otherBrokerEnvironment(environment string) string {
	if environment == "real" {
		return "demo"
	}
	return "real"
}

type BrokerStatus struct {
	Configured       bool       `json:"configured"`
	Provider         string     `json:"provider"`
	Environment      string     `json:"environment"`
	LastAttempt      *time.Time `json:"last_attempt"`
	LastResult       string     `json:"last_result"`
	HTTPStatus       int        `json:"http_status"`
	LastSuccess      *time.Time `json:"last_success"`
	SnapshotCount    int64      `json:"snapshot_count"`
	ExecutionEnabled bool       `json:"execution_enabled"`
}
type BrokerOverview struct {
	Provider         string                  `json:"provider"`
	ExecutionEnabled bool                    `json:"execution_enabled"`
	Environments     map[string]BrokerStatus `json:"environments"`
}
type BrokerSnapshot struct {
	ID           string         `json:"id"`
	Environment  string         `json:"environment"`
	FetchedAt    time.Time      `json:"fetched_at"`
	SourceSHA256 string         `json:"source_sha256"`
	Portfolio    etoro.Snapshot `json:"portfolio"`
}

// A slot is an operator hint. Only a successful aggregate read binds a key to Demo or Real.
type BrokerService struct {
	DB          *pgxpool.Pool
	clients     map[string]*etoro.Client
	bound       map[string]*etoro.Client
	conflicted  map[*etoro.Client]bool
	aead        cipher.AEAD
	token       string
	mu          sync.Mutex
	lastAttempt map[*etoro.Client]time.Time
}

func NewBrokerService(db *pgxpool.Pool, clients map[string]*etoro.Client, encodedKey, operatorToken string) (*BrokerService, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("MFD_ACCOUNT_ENCRYPTION_KEY must be base64 for 32 bytes")
	}
	if len(operatorToken) < 32 {
		return nil, errors.New("MFD_OPERATOR_TOKEN must contain at least 32 characters")
	}
	for slot, client := range clients {
		if !validBrokerEnvironment(slot) || client == nil || client.APIKey == "" || client.UserKey == "" {
			return nil, errors.New("invalid eToro credential slot")
		}
	}
	if clients["demo"] != nil && clients["real"] != nil && clients["demo"].UserKey == clients["real"].UserKey {
		return nil, errors.New("eToro Demo and Real user keys must be distinct")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &BrokerService{DB: db, clients: clients, bound: map[string]*etoro.Client{}, conflicted: map[*etoro.Client]bool{}, aead: aead, token: operatorToken, lastAttempt: map[*etoro.Client]time.Time{}}, nil
}
func (b *BrokerService) Authorized(token string) bool {
	return token != "" && len(token) == len(b.token) && subtle.ConstantTimeCompare([]byte(token), []byte(b.token)) == 1
}
func (b *BrokerService) encrypt(raw []byte) ([]byte, []byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, b.aead.Seal(nil, nonce, raw, nil), nil
}
func (b *BrokerService) decrypt(nonce, ciphertext []byte) ([]byte, error) {
	return b.aead.Open(nil, nonce, ciphertext, nil)
}

// caller holds b.mu.
func (b *BrokerService) configured(environment string) bool {
	if b.bound[environment] != nil {
		return true
	}
	client := b.clients[environment]
	return client != nil && !b.conflicted[client] && b.bound[otherBrokerEnvironment(environment)] != client
}
func (b *BrokerService) status(ctx context.Context, environment string) (BrokerStatus, error) {
	out := BrokerStatus{Configured: b.configured(environment), Provider: "etoro", Environment: environment, LastResult: "not_configured", ExecutionEnabled: false}
	if out.Configured {
		out.LastResult = "not_checked"
	}
	err := b.DB.QueryRow(ctx, `SELECT attempted_at,result,http_status FROM etoro_sync_events WHERE environment=$1 ORDER BY attempted_at DESC LIMIT 1`, environment).Scan(&out.LastAttempt, &out.LastResult, &out.HTTPStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if err = b.DB.QueryRow(ctx, `SELECT count(*),max(fetched_at) FROM etoro_snapshots WHERE environment=$1`, environment).Scan(&out.SnapshotCount, &out.LastSuccess); err != nil {
		return out, err
	}
	if !out.Configured {
		out.LastResult = "not_configured"
		out.HTTPStatus = 0
	}
	return out, nil
}
func (b *BrokerService) Status(ctx context.Context) (BrokerOverview, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := BrokerOverview{Provider: "etoro", ExecutionEnabled: false, Environments: map[string]BrokerStatus{}}
	for _, environment := range brokerEnvironments {
		status, err := b.status(ctx, environment)
		if err != nil {
			return BrokerOverview{}, err
		}
		out.Environments[environment] = status
	}
	return out, nil
}
func classifyBrokerRead(err error) (string, int) {
	if err == nil {
		return "ok", 200
	}
	if errors.Is(err, errBrokerCredentialConflict) {
		return "credential_conflict", 200
	}
	var apiErr *etoro.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Category, apiErr.Status
	}
	var schemaErr *etoro.SchemaError
	if errors.As(err, &schemaErr) {
		return "schema_error", 200
	}
	return "request_failed", 0
}

// A successful raw response is encrypted in the same transaction as its status event.
func (b *BrokerService) persist(ctx context.Context, environment string, raw []byte, snapshot etoro.Snapshot, readErr error) error {
	result, httpStatus := classifyBrokerRead(readErr)
	if readErr != nil {
		slog.Warn("eToro read unavailable", "environment", environment, "category", result, "http_status", httpStatus)
	}
	tx, err := b.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var snapshotID any
	if readErr == nil {
		nonce, ciphertext, err := b.encrypt(raw)
		if err != nil {
			return err
		}
		id := newID()
		if _, err = tx.Exec(ctx, `INSERT INTO etoro_snapshots(id,environment,provider_at,response_sha256,nonce,ciphertext) VALUES($1,$2,$3,$4,$5,$6)`, id, environment, snapshot.ProviderAt, etoro.Digest(raw), nonce, ciphertext); err != nil {
			return err
		}
		snapshotID = id
	}
	if _, err = tx.Exec(ctx, `INSERT INTO etoro_sync_events(id,environment,result,http_status,snapshot_id) VALUES($1,$2,$3,$4,$5)`, newID(), environment, result, httpStatus, snapshotID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// caller holds b.mu. A permission denial triggers one read against the other
// documented environment. A malformed 200 response does not prove key scope.
func (b *BrokerService) recordSuccess(ctx context.Context, client *etoro.Client, environment string, raw []byte, snapshot etoro.Snapshot) error {
	if existing := b.bound[environment]; existing != nil && existing != client {
		b.conflicted[client] = true
		return b.persist(ctx, environment, nil, etoro.Snapshot{}, errBrokerCredentialConflict)
	}
	if err := b.persist(ctx, environment, raw, snapshot, nil); err != nil {
		return err
	}
	b.bound[environment] = client
	return nil
}
func (b *BrokerService) syncClient(ctx context.Context, client *etoro.Client, preferred string, detect bool) error {
	if b.conflicted[client] {
		return nil
	}
	if time.Since(b.lastAttempt[client]) < 30*time.Second {
		return nil
	}
	b.lastAttempt[client] = time.Now()
	requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	raw, snapshot, readErr := client.FetchAggregate(requestCtx, preferred)
	cancel()
	if readErr == nil {
		return b.recordSuccess(ctx, client, preferred, raw, snapshot)
	}
	var apiErr *etoro.APIError
	if detect && errors.As(readErr, &apiErr) && (apiErr.Status == 403 || apiErr.Status == 404) {
		if err := b.persist(ctx, preferred, nil, etoro.Snapshot{}, readErr); err != nil {
			return err
		}
		alternate := otherBrokerEnvironment(preferred)
		requestCtx, cancel = context.WithTimeout(ctx, 25*time.Second)
		raw, snapshot, alternateErr := client.FetchAggregate(requestCtx, alternate)
		cancel()
		if alternateErr == nil {
			return b.recordSuccess(ctx, client, alternate, raw, snapshot)
		}
		return b.persist(ctx, alternate, nil, etoro.Snapshot{}, alternateErr)
	}
	return b.persist(ctx, preferred, nil, etoro.Snapshot{}, readErr)
}
func (b *BrokerService) Sync(ctx context.Context, environment string) (BrokerStatus, error) {
	if !validBrokerEnvironment(environment) {
		return BrokerStatus{}, errors.New("invalid eToro environment")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if client := b.bound[environment]; client != nil {
		if err := b.syncClient(ctx, client, environment, false); err != nil {
			return BrokerStatus{}, err
		}
	} else if client := b.clients[environment]; client != nil && !b.conflicted[client] && b.bound[otherBrokerEnvironment(environment)] != client {
		if err := b.syncClient(ctx, client, environment, true); err != nil {
			return BrokerStatus{}, err
		}
	} else if client := b.clients[otherBrokerEnvironment(environment)]; client != nil && !b.conflicted[client] && b.bound[otherBrokerEnvironment(environment)] != client {
		if err := b.syncClient(ctx, client, otherBrokerEnvironment(environment), true); err != nil {
			return BrokerStatus{}, err
		}
	}
	return b.status(ctx, environment)
}
func (b *BrokerService) History(ctx context.Context, environment string) ([]BrokerSnapshot, error) {
	if !validBrokerEnvironment(environment) {
		return nil, errors.New("invalid eToro environment")
	}
	rows, err := b.DB.Query(ctx, `SELECT id::text,fetched_at,response_sha256,nonce,ciphertext FROM etoro_snapshots WHERE environment=$1 ORDER BY fetched_at DESC LIMIT 30`, environment)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BrokerSnapshot{}
	for rows.Next() {
		var item BrokerSnapshot
		var nonce, ciphertext []byte
		if err = rows.Scan(&item.ID, &item.FetchedAt, &item.SourceSHA256, &nonce, &ciphertext); err != nil {
			return nil, err
		}
		raw, err := b.decrypt(nonce, ciphertext)
		if err != nil {
			return nil, fmt.Errorf("encrypted portfolio snapshot invalid: %w", err)
		}
		if etoro.Digest(raw) != item.SourceSHA256 {
			return nil, errors.New("portfolio snapshot digest mismatch")
		}
		item.Portfolio, err = etoro.ParseAggregate(raw)
		if err != nil {
			return nil, errors.New("stored portfolio schema invalid")
		}
		item.Environment = environment
		out = append(out, item)
	}
	return out, rows.Err()
}
func (b *BrokerService) Run(ctx context.Context) {
	b.syncConfigured(ctx)
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.syncConfigured(ctx)
		}
	}
}
func (b *BrokerService) syncConfigured(ctx context.Context) {
	for _, slot := range brokerEnvironments {
		b.mu.Lock()
		client := b.clients[slot]
		if client != nil && !b.conflicted[client] {
			preferred := slot
			if b.bound[otherBrokerEnvironment(slot)] == client {
				preferred = otherBrokerEnvironment(slot)
			}
			detect := b.bound[preferred] != client
			if err := b.syncClient(ctx, client, preferred, detect); err != nil {
				slog.Warn("eToro sync storage unavailable", "environment", preferred, "error", err)
			}
		}
		b.mu.Unlock()
	}
}
func (p *Platform) BrokerStatus(ctx context.Context) (BrokerOverview, error) {
	if p.Broker == nil {
		environments := map[string]BrokerStatus{}
		for _, name := range brokerEnvironments {
			environments[name] = BrokerStatus{Provider: "etoro", Environment: name, LastResult: "not_configured", ExecutionEnabled: false}
		}
		return BrokerOverview{Provider: "etoro", ExecutionEnabled: false, Environments: environments}, nil
	}
	return p.Broker.Status(ctx)
}
func (p *Platform) BrokerAuthorized(token string) bool {
	return p.Broker != nil && p.Broker.Authorized(token)
}
func (p *Platform) BrokerHistory(ctx context.Context, environment string) ([]BrokerSnapshot, error) {
	if p.Broker == nil {
		return nil, errors.New("eToro is not configured")
	}
	return p.Broker.History(ctx, environment)
}
func (p *Platform) BrokerSync(ctx context.Context, environment string) (BrokerStatus, error) {
	if p.Broker == nil {
		return BrokerStatus{}, errors.New("eToro is not configured")
	}
	return p.Broker.Sync(ctx, environment)
}
