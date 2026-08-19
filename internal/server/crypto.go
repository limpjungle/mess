package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const keySize = 32

type Cryptor struct {
	gcm cipher.AEAD
}

func NewCryptor(masterKey string) (*Cryptor, error) {
	key, err := decodeKey(masterKey)
	if err != nil {
		return nil, fmt.Errorf("load master key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cryptor{gcm: gcm}, nil
}

func decodeKey(masterKey string) ([]byte, error) {
	if masterKey != "" {
		b, err := base64.StdEncoding.DecodeString(masterKey)
		if err != nil {
			return nil, errors.New("MASTER_KEY must be base64")
		}
		if len(b) != keySize {
			return nil, fmt.Errorf("MASTER_KEY must decode to %d bytes, got %d", keySize, len(b))
		}
		return b, nil
	}

	path := filepath.Join("certs", "master.key")
	if b, err := os.ReadFile(path); err == nil {
		if len(b) == keySize {
			return b, nil
		}
	}

	b := make([]byte, keySize)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

func (c *Cryptor) Encrypt(plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, c.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return c.gcm.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func (c *Cryptor) Decrypt(ciphertext, nonce []byte) ([]byte, error) {
	return c.gcm.Open(nil, nonce, ciphertext, nil)
}
