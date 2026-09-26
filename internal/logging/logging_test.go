package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewProducesStructuredJSON(t *testing.T) {
	var buf bytes.Buffer

	logger, err := New(&buf, "info")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info().
		Str("request_id", "req-123").
		Str("tenant_id", "tenant-456").
		Str("operation", "test_operation").
		Msg("test message")

	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	for _, field := range []string{
		"timestamp",
		"level",
		"message",
		"request_id",
		"tenant_id",
		"operation",
	} {
		if _, ok := entry[field]; !ok {
			t.Errorf("expected field %q in log entry", field)
		}
	}

	if entry["level"] != "info" {
		t.Errorf("level = %v, want info", entry["level"])
	}

	if entry["message"] != "test message" {
		t.Errorf("message = %v, want test message", entry["message"])
	}
}

func TestNewFiltersByConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer

	logger, err := New(&buf, "warn")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Debug().Msg("debug message")
	logger.Info().Msg("info message")
	logger.Warn().Msg("warn message")

	output := buf.String()

	if strings.Contains(output, "debug message") {
		t.Error("debug message should be filtered at warn level")
	}

	if strings.Contains(output, "info message") {
		t.Error("info message should be filtered at warn level")
	}

	if !strings.Contains(output, "warn message") {
		t.Error("warn message should be emitted at warn level")
	}
}

func TestNewDefaultsToInfoLevel(t *testing.T) {
	var buf bytes.Buffer

	logger, err := New(&buf, "")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Debug().Msg("debug message")
	logger.Info().Msg("info message")

	output := buf.String()

	if strings.Contains(output, "debug message") {
		t.Error("debug message should be filtered by the default info level")
	}

	if !strings.Contains(output, "info message") {
		t.Error("info message should be emitted by the default info level")
	}
}

func TestNewRejectsInvalidLogLevel(t *testing.T) {
	var buf bytes.Buffer

	if _, err := New(&buf, "verbose"); err == nil {
		t.Fatal("expected invalid log level to return an error")
	}
}
func TestNewAutomaticallyRedactsSensitiveFields(t *testing.T) {
	var buf bytes.Buffer

	logger, err := New(&buf, "info")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info().
		Str("api_key", "must-not-appear").
		Str("mnemonic", "must-also-not-appear").
		Str("operation", "redaction_test").
		Msg("test automatic redaction")

	output := buf.String()

	if strings.Contains(output, "must-not-appear") {
		t.Fatal("API key appeared in log output")
	}

	if strings.Contains(output, "must-also-not-appear") {
		t.Fatal("mnemonic appeared in log output")
	}

	if !strings.Contains(output, redactedValue) {
		t.Fatal("expected redacted marker in log output")
	}
}

func TestNewAddsStackTraceToErrorsAtDebugLevel(t *testing.T) {
	var buf bytes.Buffer

	logger, err := New(&buf, "debug")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Error().Msg("debug error")

	var entry map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}

	stack, ok := entry["stack"].(string)
	if !ok || stack == "" {
		t.Error("stack field is missing from debug-level error log")
	}
}

func TestNewDoesNotAddStackTraceToErrorsAtInfoLevel(t *testing.T) {
	var buf bytes.Buffer

	logger, err := New(&buf, "info")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Error().Msg("info-level error")

	var entry map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}

	if _, ok := entry["stack"]; ok {
		t.Error("stack field should not be present when LOG_LEVEL is info")
	}
}
