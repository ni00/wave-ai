// Package secrets protects vault credentials with AES-256-GCM.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Box seals and opens secret bundles with AES-256-GCM.
type Box struct {
	key []byte
}

// NewBox builds a Box from a 32-byte key. When key is nil the key is loaded
// (or created) from keyFile so single-box deployments restart cleanly.
// Creation uses O_EXCL: when several processes cold-start together, exactly
// one wins the election and writes its key; the losers read the winner's,
// so a race can never leave two different "master" keys in play.
func NewBox(key []byte, keyFile string) (*Box, error) {
	if key == nil {
		if keyFile == "" {
			return nil, fmt.Errorf("secrets: no master key configured")
		}
		if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
			return nil, err
		}
		k, loadErr := loadOrCreateKey(keyFile)
		if loadErr != nil {
			return nil, loadErr
		}
		key = k
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets: key must be 32 bytes")
	}
	return &Box{key: key}, nil
}

func loadOrCreateKey(keyFile string) ([]byte, error) {
	// single-writer election
	f, err := os.OpenFile(keyFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			f.Close()
			return nil, err
		}
		_, werr := f.Write([]byte(hex.EncodeToString(key)))
		f.Close()
		if werr != nil {
			return nil, werr
		}
		return key, nil
	}
	// lost the election (file exists): read the winner's key; it may be
	// mid-write, so retry briefly on an empty/partial read
	for i := 0; i < 40; i++ {
		raw, err := os.ReadFile(keyFile)
		if err == nil && len(raw) >= 64 {
			if k, err := hex.DecodeString(string(raw[:64])); err == nil && len(k) == 32 {
				return k, nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("secrets: master key file %s unreadable", keyFile)
}

// Seal encrypts plaintext; returns ciphertext and nonce.
func (b *Box) Seal(plaintext []byte) (ciphertext, nonce []byte, err error) {
	gcm, err := b.gcm()
	if err != nil {
		return nil, nil, err
	}
	nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return gcm.Seal(nil, nonce, plaintext, nil), nonce, nil
}

// Open decrypts a sealed pair.
func (b *Box) Open(ciphertext, nonce []byte) ([]byte, error) {
	gcm, err := b.gcm()
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("secrets: bad nonce length")
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func (b *Box) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(b.key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
