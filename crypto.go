package configsdk

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
)

// decrypt decrypts ciphertext encrypted with AES-256-GCM.
// The ciphertext format is: nonce (12 bytes) || encrypted_data || GCM tag (16 bytes).
// This is binary-compatible with the server-side gcm.Seal(nonce, nonce, plaintext, nil).
func decrypt(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("configsdk: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("configsdk: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize+gcm.Overhead() {
		return nil, ErrDecryptionFailed
	}

	nonce := ciphertext[:nonceSize]
	encrypted := ciphertext[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}
