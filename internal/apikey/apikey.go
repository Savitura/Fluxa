package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
)

var b58Alphabet = []byte("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz")

func base58Encode(b []byte) string {
	x := new(big.Int).SetBytes(b)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := &big.Int{}
	var result []byte
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		result = append(result, b58Alphabet[mod.Int64()])
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	for _, byteVal := range b {
		if byteVal == 0x00 {
			result = append([]byte{b58Alphabet[0]}, result...)
		} else {
			break
		}
	}
	return string(result)
}

// Generate creates a new API key of the form sk_live_<base58>
func Generate() (raw string, prefix string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	encoded := base58Encode(b)
	raw = "sk_live_" + encoded
	
	// Prefix is first 8 chars for display
	prefix = raw[:8]
	return raw, prefix, nil
}

// Hash returns the SHA-256 hash of the raw API key for storage
func Hash(raw string) string {
	h := sha256.New()
	h.Write([]byte(raw))
	return hex.EncodeToString(h.Sum(nil))
}

// Verify checks if the provided raw key matches the stored hash
func Verify(raw, hashed string) bool {
	return Hash(raw) == hashed
}
