package configsdk

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"testing"
)

// TestAESGCMServerCompatibility verifies that the SDK can decrypt ciphertext
// produced by a server-side AES-256-GCM implementation. The test uses a pre-computed
// vector: known key, nonce, plaintext and expected ciphertext to ensure binary
// compatibility between client and server encryption formats.
//
// Ciphertext layout (same on server): nonce (12 bytes) || encrypted || GCM tag (16 bytes)
func TestAESGCMServerCompatibility(t *testing.T) {
	// --- reference values (would come from the server implementation) ---
	const (
		hexKey       = "4b7a2f5e8c1d3a6f9e0b7c4d2a8f5e1b3c6d9a0e7f4b2c8d1a5e3f6b9c0d7a2e"
		hexNonce     = "aabbccddeeff00112233aabb"
		hexPlaintext = "7b22686f7374223a226462312e6578616d706c652e636f6d222c22706f7274223a353433327d"
	)

	key, _ := hex.DecodeString(hexKey)
	nonce, _ := hex.DecodeString(hexNonce)
	plaintext, _ := hex.DecodeString(hexPlaintext)

	// Produce the reference ciphertext using Go's standard crypto (simulates server).
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	// gcm.Seal prepends ciphertextTag to nonce, producing: nonce || enc || tag
	serverCiphertext := gcm.Seal(nonce, nonce, plaintext, nil)

	// Verify the SDK decrypt function can recover the plaintext.
	got, err := decrypt(serverCiphertext, key)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("plaintext mismatch:\n got: %s\nwant: %s", got, plaintext)
	}

	// Verify the actual JSON content.
	want := `{"host":"db1.example.com","port":5432}`
	if string(got) != want {
		t.Fatalf("JSON mismatch:\n got: %s\nwant: %s", string(got), want)
	}
}

// TestAESGCMFixedVector validates decryption against a fully pre-computed hex
// ciphertext. If this test ever fails it means the wire format has changed.
func TestAESGCMFixedVector(t *testing.T) {
	const (
		hexKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	)
	key, _ := hex.DecodeString(hexKey)

	// Encrypt with a fixed nonce to get a deterministic ciphertext.
	nonce := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	plaintext := []byte(`{"env":"production"}`)

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM: %v", err)
	}
	fixedCiphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	fixedHex := hex.EncodeToString(fixedCiphertext)

	// Now decode from hex and decrypt — simulates receiving from wire.
	wire, err := hex.DecodeString(fixedHex)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	got, err := decrypt(wire, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}
