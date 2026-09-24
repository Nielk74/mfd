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
type BrokerSnapshot struct {
	ID           string         `json:"id"`
	FetchedAt    time.Time      `json:"fetched_at"`
	SourceSHA256 string         `json:"source_sha256"`
	Portfolio    etoro.Snapshot `json:"portfolio"`
}

type BrokerService struct {
	DB          *pgxpool.Pool
	Client      *etoro.Client
	aead        cipher.AEAD
	token       string
	mu          sync.Mutex
	lastAttempt time.Time
}

func NewBrokerService(db *pgxpool.Pool, client *etoro.Client, encodedKey, operatorToken string) (*BrokerService, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil || len(key) != 32 {
		return nil, errors.New("MFD_ACCOUNT_ENCRYPTION_KEY must be base64 for 32 bytes")
	}
	if len(operatorToken) < 32 {
		return nil, errors.New("MFD_OPERATOR_TOKEN must contain at least 32 characters")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &BrokerService{DB: db, Client: client, aead: aead, token: operatorToken}, nil
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
func (b *BrokerService) Status(ctx context.Context) (BrokerStatus, error) {
	out := BrokerStatus{Configured: true, Provider: "etoro", Environment: "demo", LastResult: "not_checked", ExecutionEnabled: false}
	err := b.DB.QueryRow(ctx, `SELECT attempted_at,result,http_status FROM etoro_sync_events ORDER BY attempted_at DESC LIMIT 1`).Scan(&out.LastAttempt, &out.LastResult, &out.HTTPStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if err = b.DB.QueryRow(ctx, `SELECT count(*),max(fetched_at) FROM etoro_snapshots`).Scan(&out.SnapshotCount, &out.LastSuccess); err != nil {
		return out, err
	}
	return out, nil
}
func (b *BrokerService) Sync(ctx context.Context) (BrokerStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Since(b.lastAttempt) < 30*time.Second {
		return b.Status(ctx)
	}
	b.lastAttempt = time.Now()
	requestCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	raw, snapshot, readErr := b.Client.FetchAggregate(requestCtx)
	result := "ok"
	httpStatus := 200
	if readErr != nil {
		result = "request_failed"
		httpStatus = 0
		var apiErr *etoro.APIError
		if errors.As(readErr, &apiErr) {
			result = apiErr.Category
			httpStatus = apiErr.Status
		}
		// Never include broker response bodies, keys or account data in logs.
		slog.Warn("eToro demo sync unavailable", "category", result, "http_status", httpStatus)
	}
	tx, err := b.DB.Begin(ctx)
	if err != nil {
		return BrokerStatus{}, err
	}
	defer tx.Rollback(ctx)
	var snapshotID any
	if readErr == nil {
		nonce, ciphertext, err := b.encrypt(raw)
		if err != nil {
			return BrokerStatus{}, err
		}
		id := newID()
		if _, err = tx.Exec(ctx, `INSERT INTO etoro_snapshots(id,provider_at,response_sha256,nonce,ciphertext) VALUES($1,$2,$3,$4,$5)`, id, snapshot.ProviderAt, etoro.Digest(raw), nonce, ciphertext); err != nil {
			return BrokerStatus{}, err
		}
		snapshotID = id
	}
	if _, err = tx.Exec(ctx, `INSERT INTO etoro_sync_events(id,result,http_status,snapshot_id) VALUES($1,$2,$3,$4)`, newID(), result, httpStatus, snapshotID); err != nil {
		return BrokerStatus{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return BrokerStatus{}, err
	}
	return b.Status(ctx)
}
func (b *BrokerService) History(ctx context.Context) ([]BrokerSnapshot, error) {
	rows, err := b.DB.Query(ctx, `SELECT id::text,fetched_at,response_sha256,nonce,ciphertext FROM etoro_snapshots ORDER BY fetched_at DESC LIMIT 30`)
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
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (b *BrokerService) Run(ctx context.Context) {
	_, _ = b.Sync(ctx)
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = b.Sync(ctx)
		}
	}
}
func (p *Platform) BrokerStatus(ctx context.Context) (BrokerStatus, error) {
	if p.Broker == nil {
		return BrokerStatus{Provider: "etoro", Environment: "demo", LastResult: "not_configured", ExecutionEnabled: false}, nil
	}
	return p.Broker.Status(ctx)
}
func (p *Platform) BrokerAuthorized(token string) bool {
	return p.Broker != nil && p.Broker.Authorized(token)
}
func (p *Platform) BrokerHistory(ctx context.Context) ([]BrokerSnapshot, error) {
	if p.Broker == nil {
		return nil, errors.New("eToro is not configured")
	}
	return p.Broker.History(ctx)
}
func (p *Platform) BrokerSync(ctx context.Context) (BrokerStatus, error) {
	if p.Broker == nil {
		return BrokerStatus{}, errors.New("eToro is not configured")
	}
	return p.Broker.Sync(ctx)
}
