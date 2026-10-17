package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"strconv"
	"strings"
)

const (
	// CurrentVersion is the latest encryption envelope version
	CurrentVersion = 1
	// VersionPrefix is the prefix for versioned envelopes (e.g., "v1::")
	VersionPrefix = "v"
)

var (
	ErrInvalidVersion    = errors.New("unsupported envelope version")
	ErrInvalidCiphertext = errors.New("invalid ciphertext format")
	ErrDecryptionFailed  = errors.New("decryption failed: authentication tag mismatch")
	ErrKeyLength         = errors.New("encryption key must be 32 bytes")
	ErrVersionParse      = errors.New("failed to parse envelope version")
)

// Envelope represents a versioned encryption envelope
type Envelope struct {
	Version    int
	Nonce      []byte
	Ciphertext []byte
}

// Encrypt encrypts plaintext using AES-256-GCM with the provided 32-byte key.
// The returned ciphertext is a versioned envelope: v1::nonce||ciphertext (hex encoded)
func Encrypt(plaintext, key []byte) ([]byte, error) {
	return EncryptWithVersion(plaintext, key, CurrentVersion)
}

// EncryptWithVersion encrypts plaintext using AES-256-GCM with the provided 32-byte key
// and writes the specified envelope version header (e.g. v2::...).
func EncryptWithVersion(plaintext, key []byte, version int) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrKeyLength
	}
	if version <= 0 {
		return nil, ErrInvalidVersion
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Build versioned envelope: v{version}::nonce||ciphertext
	envelope := Envelope{
		Version:    version,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}

	return envelope.Marshal()
}

// Decrypt decrypts a ciphertext produced by Encrypt.
// Supports both versioned envelopes (v1::...) and legacy format (nonce||ciphertext).
func Decrypt(ciphertext, key []byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrKeyLength
	}

	// Try to parse as versioned envelope first
	env, err := ParseEnvelope(ciphertext)
	if err == nil {
		return env.Decrypt(key)
	}

	// Fall back to legacy format (nonce||ciphertext) for backward compatibility
	return decryptLegacy(ciphertext, key)
}

// ParseEnvelope parses a versioned envelope from bytes
func ParseEnvelope(data []byte) (*Envelope, error) {
	if len(data) < 4 { // minimum "v1::"
		return nil, ErrInvalidCiphertext
	}

	str := string(data)
	if !strings.HasPrefix(str, VersionPrefix) {
		return nil, ErrInvalidCiphertext
	}

	parts := strings.SplitN(str, "::", 2)
	if len(parts) != 2 {
		return nil, ErrInvalidCiphertext
	}

	versionStr := strings.TrimPrefix(parts[0], VersionPrefix)
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return nil, ErrVersionParse
	}

	if version <= 0 {
		return nil, ErrInvalidVersion
	}

	// Decode hex payload
	payload, err := hexDecode(parts[1])
	if err != nil {
		return nil, ErrInvalidCiphertext
	}

	block, err := aes.NewCipher(make([]byte, 32)) // dummy key for nonce size
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(payload) < nonceSize {
		return nil, ErrInvalidCiphertext
	}

	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]

	return &Envelope{
		Version:    version,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}, nil
}

// Decrypt decrypts the envelope using the provided key
func (e *Envelope) Decrypt(key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, e.Nonce, e.Ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// Marshal serializes the envelope to bytes (hex encoded)
func (e *Envelope) Marshal() ([]byte, error) {
	payload := make([]byte, len(e.Nonce)+len(e.Ciphertext))
	copy(payload, e.Nonce)
	copy(payload[len(e.Nonce):], e.Ciphertext)

	hexPayload := hexEncode(payload)
	envelopeStr := VersionPrefix + strconv.Itoa(e.Version) + "::" + hexPayload
	return []byte(envelopeStr), nil
}

// decryptLegacy decrypts legacy format (nonce||ciphertext without version prefix)
func decryptLegacy(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// hexEncode encodes bytes to hex string
func hexEncode(data []byte) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, len(data)*2)
	for i, b := range data {
		result[i*2] = hexChars[b>>4]
		result[i*2+1] = hexChars[b&0x0f]
	}
	return string(result)
}

// hexDecode decodes hex string to bytes
func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("invalid hex string")
	}
	result := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		hi := decodeHexChar(s[i])
		lo := decodeHexChar(s[i+1])
		if hi == 0xff || lo == 0xff {
			return nil, errors.New("invalid hex character")
		}
		result[i/2] = (hi << 4) | lo
	}
	return result, nil
}

func decodeHexChar(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10
	default:
		return 0xff
	}
}
