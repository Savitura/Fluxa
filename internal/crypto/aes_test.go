package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("SCZV123SECRETSTELLARKEY")

	ciphertext, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext should not equal plaintext")
	}

	decrypted, err := Decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptProducesUniqueCiphertexts(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	plaintext := []byte("same plaintext")

	c1, _ := Encrypt(plaintext, key)
	c2, _ := Encrypt(plaintext, key)

	if bytes.Equal(c1, c2) {
		t.Fatal("two encryptions of the same plaintext should produce different ciphertexts (random nonce)")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)

	ciphertext, _ := Encrypt([]byte("secret"), key1)
	_, err := Decrypt(ciphertext, key2)
	if err == nil {
		t.Fatal("expected error when decrypting with wrong key")
	}
}

func TestInvalidKeyLength(t *testing.T) {
	shortKey := make([]byte, 16)
	_, err := Encrypt([]byte("data"), shortKey)
	if err == nil {
		t.Fatal("expected error for non-32-byte key")
	}
}
