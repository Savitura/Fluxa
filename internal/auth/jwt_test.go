package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// testSecret is a 32-byte secret used across all sub-tests.
var testSecret = []byte("test-jwt-secret-key-32-bytes-ok!")

// mint is a shorthand that fails the test on error.
func mint(t *testing.T, secret []byte, dur time.Duration) string {
	t.Helper()
	tok, err := GenerateToken("user-1", "tenant-1", "admin", "user@example.com", "access", secret, dur)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	return tok
}

// TestValidToken verifies that a freshly minted token round-trips cleanly.
func TestValidToken(t *testing.T) {
	tok := mint(t, testSecret, time.Hour)

	claims, err := ParseToken(tok, testSecret)
	if err != nil {
		t.Fatalf("ParseToken returned unexpected error: %v", err)
	}

	if claims.Sub != "user-1" {
		t.Errorf("Sub: want %q, got %q", "user-1", claims.Sub)
	}
	if claims.TenantID != "tenant-1" {
		t.Errorf("TenantID: want %q, got %q", "tenant-1", claims.TenantID)
	}
	if claims.Role != "admin" {
		t.Errorf("Role: want %q, got %q", "admin", claims.Role)
	}
	if claims.TokenType != "access" {
		t.Errorf("TokenType: want %q, got %q", "access", claims.TokenType)
	}
	if claims.Iss != TokenIssuer {
		t.Errorf("Iss: want %q, got %q", TokenIssuer, claims.Iss)
	}
	if claims.Aud != TokenAudience {
		t.Errorf("Aud: want %q, got %q", TokenAudience, claims.Aud)
	}
	if claims.Jti == "" {
		t.Error("Jti must not be empty")
	}
}

// TestWrongSecret verifies that a token verified with a different secret is
// rejected with a signature error.
func TestWrongSecret(t *testing.T) {
	tok := mint(t, testSecret, time.Hour)
	wrongSecret := []byte("wrong-secret-key-32-bytes-long!!")

	_, err := ParseToken(tok, wrongSecret)
	if err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

// TestExpiredToken verifies that a token whose exp is in the past is rejected.
func TestExpiredToken(t *testing.T) {
	tok := mint(t, testSecret, -time.Second)

	_, err := ParseToken(tok, testSecret)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

// TestWrongIssuer verifies that a token carrying a different iss is rejected.
func TestWrongIssuer(t *testing.T) {
	tok := mint(t, testSecret, time.Hour)
	tok = tamperClaim(t, tok, testSecret, func(c map[string]any) {
		c["iss"] = "evil-issuer"
	})

	_, err := ParseToken(tok, testSecret)
	if err == nil {
		t.Fatal("expected error for wrong issuer, got nil")
	}
}

// TestWrongAudience verifies that a token carrying a different aud is rejected.
func TestWrongAudience(t *testing.T) {
	tok := mint(t, testSecret, time.Hour)
	tok = tamperClaim(t, tok, testSecret, func(c map[string]any) {
		c["aud"] = "evil-audience"
	})

	_, err := ParseToken(tok, testSecret)
	if err == nil {
		t.Fatal("expected error for wrong audience, got nil")
	}
}

// TestTamperedPayload verifies that modifying the claims segment without
// re-signing causes a signature mismatch.
func TestTamperedPayload(t *testing.T) {
	tok := mint(t, testSecret, time.Hour)
	parts := strings.Split(tok, ".")

	// Decode, modify, re-encode the claims — but keep the original signature.
	rawClaims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}

	var claimsMap map[string]any
	if err := json.Unmarshal(rawClaims, &claimsMap); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	claimsMap["role"] = "owner" // attempt privilege escalation

	modified, err := json.Marshal(claimsMap)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(modified)
	// Leave parts[2] (signature) unchanged — it no longer matches.

	tampered := strings.Join(parts, ".")
	_, err = ParseToken(tampered, testSecret)
	if err == nil {
		t.Fatal("expected signature error for tampered payload, got nil")
	}
}

// TestAlgNoneRejection verifies that a token whose header declares "alg: none"
// is rejected before the (absent) signature is checked.
func TestAlgNoneRejection(t *testing.T) {
	tok := mint(t, testSecret, time.Hour)
	parts := strings.Split(tok, ".")

	// Replace the header with one that sets alg to "none".
	noneHeader := header{Alg: "none", Typ: "JWT"}
	rawHeader, _ := json.Marshal(noneHeader)
	parts[0] = base64.RawURLEncoding.EncodeToString(rawHeader)
	// Drop the signature to simulate a classic "none" attack.
	parts[2] = ""

	noneToken := strings.Join(parts, ".")
	_, err := ParseToken(noneToken, testSecret)
	if err == nil {
		t.Fatal("expected error for alg:none token, got nil")
	}
}

// TestJtiUnique verifies that every generated token carries a non-empty,
// unique jti so tokens can be individually revoked.
func TestJtiUnique(t *testing.T) {
	tok1 := mint(t, testSecret, time.Hour)
	tok2 := mint(t, testSecret, time.Hour)

	c1, err := ParseToken(tok1, testSecret)
	if err != nil {
		t.Fatalf("parse token 1: %v", err)
	}
	c2, err := ParseToken(tok2, testSecret)
	if err != nil {
		t.Fatalf("parse token 2: %v", err)
	}

	if c1.Jti == "" {
		t.Error("jti must not be empty")
	}
	if c1.Jti == c2.Jti {
		t.Errorf("jti must be unique per token; both got %q", c1.Jti)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// tamperClaim decodes the claims of tok, applies mutFn, re-encodes them, and
// re-signs with secret. This lets tests craft tokens that have a valid
// signature but wrong claim values — isolating issuer/audience checks from the
// signature check.
func tamperClaim(t *testing.T, tok string, secret []byte, mutFn func(map[string]any)) string {
	t.Helper()

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatal("malformed token in tamperClaim")
	}

	rawClaims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("tamperClaim decode: %v", err)
	}

	var claimsMap map[string]any
	if err := json.Unmarshal(rawClaims, &claimsMap); err != nil {
		t.Fatalf("tamperClaim unmarshal: %v", err)
	}

	mutFn(claimsMap)

	modified, err := json.Marshal(claimsMap)
	if err != nil {
		t.Fatalf("tamperClaim marshal: %v", err)
	}

	claimsB64 := base64.RawURLEncoding.EncodeToString(modified)
	unsigned := parts[0] + "." + claimsB64
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(unsigned))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return unsigned + "." + sig
}
