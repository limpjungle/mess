package server

import (
	"bytes"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func testCryptor(t *testing.T) *Cryptor {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	c, err := NewCryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEncryptDecrypt(t *testing.T) {
	c := testCryptor(t)
	plain := []byte("hello, мир!")
	ct, nonce, err := c.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ct, plain) {
		t.Fatal("ciphertext equals plaintext")
	}
	got, err := c.Decrypt(ct, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q want %q", got, plain)
	}
}

func TestEncryptUniqueNonce(t *testing.T) {
	c := testCryptor(t)
	_, n1, _ := c.Encrypt([]byte("same"))
	_, n2, _ := c.Encrypt([]byte("same"))
	if bytes.Equal(n1, n2) {
		t.Fatal("nonces must be unique")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	c := testCryptor(t)
	ct, nonce, _ := c.Encrypt([]byte("secret"))
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	other, _ := NewCryptor(key)
	if _, err := other.Decrypt(ct, nonce); err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestNewCryptorGeneratesFile(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	c, err := NewCryptor("")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := NewCryptor("")
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, _ := c.Encrypt([]byte("x"))
	got, err := c2.Decrypt(ct, nonce)
	if err != nil || string(got) != "x" {
		t.Fatalf("reloaded key mismatch: %s %v", got, err)
	}
}

func TestInvalidMasterKey(t *testing.T) {
	if _, err := NewCryptor(strings.Repeat("a", 10)); err == nil {
		t.Fatal("expected error for non-base64 key")
	}
}
