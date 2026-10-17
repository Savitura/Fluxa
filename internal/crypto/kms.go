package crypto

import (
	"context"
)

// KeyManagementService defines the KMS envelope encryption interface.
// Implementations can wrap cloud providers (AWS KMS, GCP KMS, HashiCorp Vault)
// or an internal versioned Keyring.
type KeyManagementService interface {
	// Encrypt encrypts data producing a versioned ciphertext envelope.
	Encrypt(ctx context.Context, plaintext []byte) ([]byte, error)
	// Decrypt decrypts a versioned ciphertext envelope using the version's key.
	Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error)
	// ActiveVersion returns the active key version used for new encryptions.
	ActiveVersion() int
}

// KeyringKMS implements KeyManagementService using an internal versioned Keyring.
type KeyringKMS struct {
	keyring *Keyring
}

// NewKeyringKMS returns a KeyManagementService backed by the given Keyring.
func NewKeyringKMS(keyring *Keyring) *KeyringKMS {
	return &KeyringKMS{keyring: keyring}
}

func (k *KeyringKMS) Encrypt(ctx context.Context, plaintext []byte) ([]byte, error) {
	return k.keyring.Encrypt(plaintext)
}

func (k *KeyringKMS) Decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	return k.keyring.Decrypt(ciphertext)
}

func (k *KeyringKMS) ActiveVersion() int {
	return k.keyring.ActiveVersion()
}
