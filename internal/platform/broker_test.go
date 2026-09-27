package platform

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/Nielk74/mfd/internal/etoro"
)

func TestBrokerSnapshotsAreEncryptedAndAuthenticated(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	b, err := NewBrokerService(nil, map[string]*etoro.Client{"demo": {APIKey: "test-public", UserKey: "test-user"}}, key, "operator-token-with-at-least-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"private_account_value":123.45}`)
	nonce, ciphertext, err := b.encrypt(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, raw) || bytes.Equal(ciphertext, raw) {
		t.Fatal("account bytes stored as plaintext")
	}
	opened, err := b.decrypt(nonce, ciphertext)
	if err != nil || !bytes.Equal(opened, raw) {
		t.Fatalf("round trip: %v", err)
	}
	ciphertext[0] ^= 1
	if _, err = b.decrypt(nonce, ciphertext); err == nil {
		t.Fatal("tampered account data accepted")
	}
	if b.Authorized("bad") || !b.Authorized("operator-token-with-at-least-32-characters") {
		t.Fatal("operator check failed")
	}
}

func TestBrokerKeyIsRelabeledOnlyAfterConfirmedRead(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	client := &etoro.Client{APIKey: "test-public", UserKey: "test-user"}
	b, err := NewBrokerService(nil, map[string]*etoro.Client{"demo": client}, key, "operator-token-with-at-least-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	if !b.configured("demo") || b.configured("real") {
		t.Fatal("unverified slot status is wrong")
	}
	b.bound["real"] = client // equivalent to a successful Real aggregate read
	if b.configured("demo") || !b.configured("real") {
		t.Fatal("key was not moved to its confirmed environment")
	}
	if result, status := classifyBrokerRead(&etoro.SchemaError{}); result != "schema_error" || status != 200 {
		t.Fatal("schema error status lost")
	}
	if result, status := classifyBrokerRead(errBrokerCredentialConflict); result != "credential_conflict" || status != 200 {
		t.Fatal("duplicate account environment was not surfaced")
	}
}

func TestBrokerAcceptsTwoDistinctKeys(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	demo := &etoro.Client{APIKey: "test-public", UserKey: "test-demo"}
	real := &etoro.Client{APIKey: "test-public", UserKey: "test-real"}
	b, err := NewBrokerService(nil, map[string]*etoro.Client{"demo": demo, "real": real}, key, "operator-token-with-at-least-32-characters")
	if err != nil {
		t.Fatal(err)
	}
	if !b.configured("demo") || !b.configured("real") {
		t.Fatal("one credential slot was ignored")
	}
	b.bound["demo"], b.bound["real"] = demo, real
	if !b.configured("demo") || !b.configured("real") {
		t.Fatal("one confirmed environment was ignored")
	}
	_, err = NewBrokerService(nil, map[string]*etoro.Client{"demo": demo, "real": demo}, key, "operator-token-with-at-least-32-characters")
	if err == nil {
		t.Fatal("same user key accepted in both slots")
	}
}
