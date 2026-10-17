package crypto

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrKeyringEmpty       = errors.New("keyring must contain at least one key")
	ErrActiveKeyNotFound  = errors.New("active key version not found in keyring")
	ErrKeyVersionNotFound = errors.New("key version not found in keyring")
	ErrInvalidKeyVersion  = errors.New("key version must be positive")
)

// Keyring manages multiple versioned encryption keys to support zero-downtime key rotation.
type Keyring struct {
	activeVersion int
	keys          map[int][]byte
}

// NewKeyring creates a Keyring with the specified active version and versioned keys map.
func NewKeyring(activeVersion int, keys map[int][]byte) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, ErrKeyringEmpty
	}
	if activeVersion <= 0 {
		return nil, ErrInvalidKeyVersion
	}
	for ver, k := range keys {
		if ver <= 0 {
			return nil, fmt.Errorf("invalid key version %d: version must be positive", ver)
		}
		if len(k) != 32 {
			return nil, fmt.Errorf("key for version %d must be exactly 32 bytes, got %d", ver, len(k))
		}
	}
	if _, ok := keys[activeVersion]; !ok {
		return nil, fmt.Errorf("%w: version %d", ErrActiveKeyNotFound, activeVersion)
	}

	keysCopy := make(map[int][]byte, len(keys))
	for v, k := range keys {
		copied := make([]byte, len(k))
		copy(copied, k)
		keysCopy[v] = copied
	}

	return &Keyring{
		activeVersion: activeVersion,
		keys:          keysCopy,
	}, nil
}

// NewKeyringFromRaw wraps a single 32-byte key as version 1.
func NewKeyringFromRaw(key []byte) (*Keyring, error) {
	return NewKeyring(1, map[int][]byte{1: key})
}

// NewKeyringFromHexStrings parses an active key and optional rotated keys from hex strings.
// retiredKeysHex is a comma-separated list of "version:hexKey" pairs (e.g. "1:0011...,2:2233...").
func NewKeyringFromHexStrings(activeVersion int, activeKeyHex string, retiredKeysHex string) (*Keyring, error) {
	activeBytes, err := hex.DecodeString(strings.TrimSpace(activeKeyHex))
	if err != nil {
		return nil, fmt.Errorf("active key hex decode: %w", err)
	}
	keys := map[int][]byte{activeVersion: activeBytes}

	if retiredKeysHex != "" {
		for _, part := range strings.Split(retiredKeysHex, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			kv := strings.SplitN(part, ":", 2)
			if len(kv) != 2 {
				return nil, fmt.Errorf("invalid key version specification %q: expected <version>:<hexKey>", part)
			}
			ver, err := strconv.Atoi(strings.TrimSpace(kv[0]))
			if err != nil || ver <= 0 {
				return nil, fmt.Errorf("invalid key version in %q", part)
			}
			kBytes, err := hex.DecodeString(strings.TrimSpace(kv[1]))
			if err != nil {
				return nil, fmt.Errorf("key hex decode for version %d: %w", ver, err)
			}
			keys[ver] = kBytes
		}
	}

	return NewKeyring(activeVersion, keys)
}

// ActiveVersion returns the active encryption key version.
func (k *Keyring) ActiveVersion() int {
	return k.activeVersion
}

// Key returns the key bytes for a given version.
func (k *Keyring) Key(version int) ([]byte, error) {
	key, ok := k.keys[version]
	if !ok {
		return nil, fmt.Errorf("%w: version %d", ErrKeyVersionNotFound, version)
	}
	return key, nil
}

// ActiveKey returns the bytes of the active encryption key.
func (k *Keyring) ActiveKey() []byte {
	return k.keys[k.activeVersion]
}

// Versions returns all available key versions in the keyring.
func (k *Keyring) Versions() []int {
	vers := make([]int, 0, len(k.keys))
	for v := range k.keys {
		vers = append(vers, v)
	}
	return vers
}

// Encrypt encrypts plaintext using the active key and active version envelope.
func (k *Keyring) Encrypt(plaintext []byte) ([]byte, error) {
	return EncryptWithVersion(plaintext, k.keys[k.activeVersion], k.activeVersion)
}

// Decrypt decrypts ciphertext using the key corresponding to its envelope version.
// If the ciphertext is in legacy format (no version prefix), it attempts decryption
// with the active key, and then other available versions if necessary.
func (k *Keyring) Decrypt(ciphertext []byte) ([]byte, error) {
	env, err := ParseEnvelope(ciphertext)
	if err == nil {
		key, ok := k.keys[env.Version]
		if !ok {
			return nil, fmt.Errorf("%w: version %d (active: %d)", ErrKeyVersionNotFound, env.Version, k.activeVersion)
		}
		return env.Decrypt(key)
	}

	// Fallback for legacy (unversioned) format: try active key first
	if pt, err := decryptLegacy(ciphertext, k.keys[k.activeVersion]); err == nil {
		return pt, nil
	}

	// Try all other versions in keyring for legacy format
	for ver, key := range k.keys {
		if ver == k.activeVersion {
			continue
		}
		if pt, err := decryptLegacy(ciphertext, key); err == nil {
			return pt, nil
		}
	}

	return nil, ErrDecryptionFailed
}
