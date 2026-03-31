package configsdk

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func encrypt(plaintext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func TestDecryptRoundTrip(t *testing.T) {
	key, _ := hex.DecodeString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	plaintext := []byte(`{"host":"localhost","port":5432}`)

	ciphertext, err := encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	got, err := decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if string(got) != string(plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key, _ := hex.DecodeString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	wrongKey, _ := hex.DecodeString("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")

	ciphertext, err := encrypt([]byte("secret"), key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	_, err = decrypt(ciphertext, wrongKey)
	if err != ErrDecryptionFailed {
		t.Fatalf("got %v, want ErrDecryptionFailed", err)
	}
}

func TestDecryptTooShort(t *testing.T) {
	key, _ := hex.DecodeString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	_, err := decrypt([]byte("short"), key)
	if err != ErrDecryptionFailed {
		t.Fatalf("got %v, want ErrDecryptionFailed", err)
	}
}

func TestDecryptCorruptedCiphertext(t *testing.T) {
	key, _ := hex.DecodeString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	ciphertext, err := encrypt([]byte(`{"key":"value"}`), key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	ciphertext[15] ^= 0xff

	_, err = decrypt(ciphertext, key)
	if err != ErrDecryptionFailed {
		t.Fatalf("got %v, want ErrDecryptionFailed", err)
	}
}

func TestDecryptInvalidKeySize(t *testing.T) {
	_, err := decrypt(
		[]byte("some data that is long enough for nonce and tag!!"),
		[]byte("bad"),
	)
	if err == nil {
		t.Fatal("expected error for invalid key size")
	}
}
