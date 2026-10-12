package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"strings"
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
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
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
	if _, err := rand.Read(key1); err != nil {
		t.Fatalf("generate key1: %v", err)
	}
	if _, err := rand.Read(key2); err != nil {
		t.Fatalf("generate key2: %v", err)
	}

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

func TestEnvelopeVersion(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	plaintext := []byte("test")

	ciphertext, err := Encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Check that ciphertext has version prefix
	ciphertextStr := string(ciphertext)
	if !strings.HasPrefix(ciphertextStr, "v1::") {
		t.Fatalf("ciphertext should have v1:: prefix, got %q", ciphertextStr)
	}

	// Decrypt should work
	decrypted, err := Decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptLegacyFormat(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	plaintext := []byte("legacy format test")

	// Create legacy format ciphertext (nonce||ciphertext without version)
	block, _ := aes.NewCipher(key)
	gcm, _ := cipher.NewGCM(block)
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("generate nonce: %v", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)

	// Decrypt should handle legacy format
	decrypted, err := Decrypt(ciphertext, key)
	if err != nil {
		t.Fatalf("Decrypt legacy: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	plaintext := []byte("tamper test")

	ciphertext, _ := Encrypt(plaintext, key)

	// Tamper with ciphertext
	ciphertext[len(ciphertext)-1] ^= 0x01

	_, err := Decrypt(ciphertext, key)
	if err == nil {
		t.Fatal("expected error when decrypting tampered ciphertext")
	}
}

func TestParseEnvelope(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	plaintext := []byte("parse test")

	ciphertext, _ := Encrypt(plaintext, key)

	env, err := ParseEnvelope(ciphertext)
	if err != nil {
		t.Fatalf("ParseEnvelope: %v", err)
	}

	if env.Version != CurrentVersion {
		t.Fatalf("expected version %d, got %d", CurrentVersion, env.Version)
	}

	decrypted, err := env.Decrypt(key)
	if err != nil {
		t.Fatalf("Envelope.Decrypt: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}

func TestParseEnvelopeInvalidVersion(t *testing.T) {
	// Create envelope with invalid version
	invalidEnvelope := []byte("v99::deadbeef")
	_, err := ParseEnvelope(invalidEnvelope)
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
}

func TestParseEnvelopeInvalidFormat(t *testing.T) {
	_, err := ParseEnvelope([]byte("not-an-envelope"))
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}
