package platform

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/Nielk74/mfd/internal/etoro"
)

func TestBrokerSnapshotsAreEncryptedAndAuthenticated(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	b, err := NewBrokerService(nil, &etoro.Client{}, key, "operator-token-with-at-least-32-characters")
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
