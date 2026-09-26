package logging

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
)

func TestRedactingWriterRedactsSensitiveFields(t *testing.T) {
	var buf bytes.Buffer

	writer := newRedactingWriter(&buf)
	logger := zerolog.New(writer)

	logger.Info().
		Str("api_key", "api-secret-value").
		Str("secret", "generic-secret-value").
		Str("wallet_secret", "wallet-secret-value").
		Str("mnemonic", "twelve secret words").
		Str("password", "super-secret-password").
		Str("authorization", "Bearer secret-token").
		Str("access_token", "access-secret").
		Str("private_key", "private-secret").
		Str("public_key", "public-value").
		Str("request_id", "req-123").
		Str("tenant_id", "tenant-456").
		Msg("redaction test")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v", err)
	}

	sensitiveFields := []string{
		"api_key",
		"secret",
		"wallet_secret",
		"mnemonic",
		"password",
		"authorization",
		"access_token",
		"private_key",
	}

	for _, field := range sensitiveFields {
		if entry[field] != redactedValue {
			t.Errorf("%s = %v, want %q", field, entry[field], redactedValue)
		}
	}

	if entry["public_key"] != "public-value" {
		t.Errorf("public_key should not be redacted, got %v", entry["public_key"])
	}

	if entry["request_id"] != "req-123" {
		t.Errorf("request_id should not be redacted, got %v", entry["request_id"])
	}

	if entry["tenant_id"] != "tenant-456" {
		t.Errorf("tenant_id should not be redacted, got %v", entry["tenant_id"])
	}
}

func TestRedactingWriterRedactsNestedSensitiveFields(t *testing.T) {
	var buf bytes.Buffer

	writer := newRedactingWriter(&buf)
	logger := zerolog.New(writer)

	logger.Info().
		Interface("credentials", map[string]interface{}{
			"api_key": "nested-secret",
			"profile": map[string]interface{}{
				"mnemonic":   "nested mnemonic",
				"public_key": "nested-public-value",
			},
		}).
		Msg("nested redaction test")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("redacted output is not valid JSON: %v", err)
	}

	credentials, ok := entry["credentials"].(map[string]interface{})
	if !ok {
		t.Fatal("credentials field is not an object")
	}

	if credentials["api_key"] != redactedValue {
		t.Errorf("nested api_key = %v, want %q", credentials["api_key"], redactedValue)
	}

	profile, ok := credentials["profile"].(map[string]interface{})
	if !ok {
		t.Fatal("profile field is not an object")
	}

	if profile["mnemonic"] != redactedValue {
		t.Errorf("nested mnemonic = %v, want %q", profile["mnemonic"], redactedValue)
	}

	if profile["public_key"] != "nested-public-value" {
		t.Errorf("nested public_key should not be redacted, got %v", profile["public_key"])
	}
}
