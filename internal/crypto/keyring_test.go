package crypto

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func generateTestKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestKeyring_Roundtrip(t *testing.T) {
	key1 := generateTestKey(t)
	kr, err := NewKeyring(1, map[int][]byte{1: key1})
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}

	plaintext := []byte("secret tenant seed SCZ...")
	ciphertext, err := kr.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("kr.Encrypt: %v", err)
	}

	if !strings.HasPrefix(string(ciphertext), "v1::") {
		t.Fatalf("expected v1:: envelope prefix, got %q", string(ciphertext))
	}

	decrypted, err := kr.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("kr.Decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}

func TestKeyring_RotationProcedure(t *testing.T) {
	// Step 1: Initial state (Version 1 active)
	keyV1 := generateTestKey(t)
	krV1, err := NewKeyring(1, map[int][]byte{1: keyV1})
	if err != nil {
		t.Fatalf("NewKeyring v1: %v", err)
	}

	secret1 := []byte("wallet-secret-1-created-under-v1")
	cipherV1, err := krV1.Encrypt(secret1)
	if err != nil {
		t.Fatalf("Encrypt v1: %v", err)
	}
	if !strings.HasPrefix(string(cipherV1), "v1::") {
		t.Fatalf("expected v1 prefix, got %s", cipherV1)
	}

	// Step 2: Rotate to Version 2 (dual-read: v2 active, v1 retained for existing secrets)
	keyV2 := generateTestKey(t)
	krV2, err := NewKeyring(2, map[int][]byte{
		1: keyV1,
		2: keyV2,
	})
	if err != nil {
		t.Fatalf("NewKeyring v2: %v", err)
	}

	// New secrets are encrypted under v2
	secret2 := []byte("wallet-secret-2-created-under-v2")
	cipherV2, err := krV2.Encrypt(secret2)
	if err != nil {
		t.Fatalf("Encrypt v2: %v", err)
	}
	if !strings.HasPrefix(string(cipherV2), "v2::") {
		t.Fatalf("expected v2 prefix, got %s", cipherV2)
	}

	// Existing v1 secrets remain decryptable with the rotated keyring
	decrypted1, err := krV2.Decrypt(cipherV1)
	if err != nil {
		t.Fatalf("failed to decrypt v1 secret with rotated keyring: %v", err)
	}
	if !bytes.Equal(decrypted1, secret1) {
		t.Fatalf("got %q, want %q", decrypted1, secret1)
	}

	// v2 secrets decrypt normally
	decrypted2, err := krV2.Decrypt(cipherV2)
	if err != nil {
		t.Fatalf("failed to decrypt v2 secret: %v", err)
	}
	if !bytes.Equal(decrypted2, secret2) {
		t.Fatalf("got %q, want %q", decrypted2, secret2)
	}

	// Step 3: Re-encrypt existing v1 secrets to v2
	reEncryptedV1, err := krV2.Encrypt(decrypted1)
	if err != nil {
		t.Fatalf("re-encrypt v1 to v2: %v", err)
	}
	if !strings.HasPrefix(string(reEncryptedV1), "v2::") {
		t.Fatalf("expected re-encrypted secret to have v2 prefix, got %s", reEncryptedV1)
	}

	// Step 4: After all secrets are migrated, retire v1
	krV2Retired, err := NewKeyring(2, map[int][]byte{2: keyV2})
	if err != nil {
		t.Fatalf("NewKeyring after retirement: %v", err)
	}

	// Re-encrypted secret decrypts on retired keyring
	reDecrypted, err := krV2Retired.Decrypt(reEncryptedV1)
	if err != nil {
		t.Fatalf("failed to decrypt re-encrypted secret on retired keyring: %v", err)
	}
	if !bytes.Equal(reDecrypted, secret1) {
		t.Fatalf("got %q, want %q", reDecrypted, secret1)
	}

	// Old un-migrated v1 secret fails on retired keyring
	if _, err := krV2Retired.Decrypt(cipherV1); err == nil {
		t.Fatal("expected decryption failure for retired key version")
	}
}

func TestKeyring_NewKeyringFromHexStrings(t *testing.T) {
	k1 := generateTestKey(t)
	k2 := generateTestKey(t)

	k1Hex := hex.EncodeToString(k1)
	k2Hex := hex.EncodeToString(k2)

	// Single key
	krSingle, err := NewKeyringFromHexStrings(1, k1Hex, "")
	if err != nil {
		t.Fatalf("single key: %v", err)
	}
	if krSingle.ActiveVersion() != 1 {
		t.Fatalf("expected active version 1, got %d", krSingle.ActiveVersion())
	}

	// Rotated keys specification
	krRotated, err := NewKeyringFromHexStrings(2, k2Hex, "1:"+k1Hex)
	if err != nil {
		t.Fatalf("rotated keys: %v", err)
	}
	if krRotated.ActiveVersion() != 2 {
		t.Fatalf("expected active version 2, got %d", krRotated.ActiveVersion())
	}
	if len(krRotated.Versions()) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(krRotated.Versions()))
	}
}

func TestKeyring_KeyManagementService(t *testing.T) {
	key := generateTestKey(t)
	kr, _ := NewKeyring(1, map[int][]byte{1: key})
	kms := NewKeyringKMS(kr)

	ctx := context.Background()
	plaintext := []byte("kms-encrypted-payload")

	ciphertext, err := kms.Encrypt(ctx, plaintext)
	if err != nil {
		t.Fatalf("kms.Encrypt: %v", err)
	}

	decrypted, err := kms.Decrypt(ctx, ciphertext)
	if err != nil {
		t.Fatalf("kms.Decrypt: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}
