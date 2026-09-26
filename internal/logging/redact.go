package logging

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/rs/zerolog"
)

const redactedValue = "[REDACTED]"

type redactingWriter struct {
	out io.Writer
}

func newRedactingWriter(out io.Writer) *redactingWriter {
	return &redactingWriter{out: out}
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	return w.write(p)
}

func (w *redactingWriter) WriteLevel(_ zerolog.Level, p []byte) (int, error) {
	return w.write(p)
}

func (w *redactingWriter) write(p []byte) (int, error) {
	payload := bytes.TrimSpace(p)

	var entry map[string]interface{}
	if err := json.Unmarshal(payload, &entry); err != nil {
		return 0, fmt.Errorf("redact log entry: invalid JSON: %w", err)
	}

	redactMap(entry)

	redacted, err := json.Marshal(entry)
	if err != nil {
		return 0, fmt.Errorf("redact log entry: marshal JSON: %w", err)
	}

	redacted = append(redacted, '\n')

	if _, err := w.out.Write(redacted); err != nil {
		return 0, err
	}

	return len(p), nil
}

func redactMap(values map[string]interface{}) {
	for key, value := range values {
		if isSensitiveKey(key) {
			values[key] = redactedValue
			continue
		}

		switch nested := value.(type) {
		case map[string]interface{}:
			redactMap(nested)
		case []interface{}:
			redactSlice(nested)
		}
	}
}

func redactSlice(values []interface{}) {
	for _, value := range values {
		switch nested := value.(type) {
		case map[string]interface{}:
			redactMap(nested)
		case []interface{}:
			redactSlice(nested)
		}
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer(
		"_", "",
		"-", "",
		".", "",
		" ", "",
	).Replace(normalized)

	switch normalized {
	case "apikey",
		"secret",
		"secretkey",
		"walletsecret",
		"mnemonic",
		"password",
		"authorization",
		"accesstoken",
		"refreshtoken",
		"token",
		"privatekey",
		"masterencryptionkey":
		return true
	default:
		return false
	}
}
