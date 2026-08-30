package postgres

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type refreshTokenDBStub struct {
	query        string
	args         []interface{}
	rowsAffected int64
}

func (s *refreshTokenDBStub) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	s.query = query
	s.args = args
	return pgconn.NewCommandTag(fmt.Sprintf("INSERT 0 %d", s.rowsAffected)), nil
}

func (*refreshTokenDBStub) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	panic("unexpected Query call")
}

func (*refreshTokenDBStub) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	panic("unexpected QueryRow call")
}

func (*refreshTokenDBStub) Begin(context.Context) (pgx.Tx, error) {
	panic("unexpected Begin call")
}

func TestRefreshTokenRepoRevokeIfActive(t *testing.T) {
	expiresAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	token := "refresh-token"
	wantHash := sha256.Sum256([]byte(token))

	activeDB := &refreshTokenDBStub{rowsAffected: 1}
	active, err := NewRefreshTokenRepo(activeDB).RevokeIfActive(context.Background(), token, expiresAt)
	if err != nil {
		t.Fatalf("RevokeIfActive() error = %v", err)
	}
	if !active {
		t.Fatal("RevokeIfActive() = false, want true for an inserted token")
	}
	if !strings.Contains(activeDB.query, "ON CONFLICT (token_hash) DO NOTHING") {
		t.Fatalf("query does not make duplicate revocation atomic: %q", activeDB.query)
	}
	if got, ok := activeDB.args[0].([]byte); !ok || string(got) != string(wantHash[:]) {
		t.Fatalf("token hash argument = %v, want SHA-256 hash", activeDB.args[0])
	}
	if got := activeDB.args[1]; got != expiresAt {
		t.Fatalf("expiry argument = %v, want %v", got, expiresAt)
	}

	duplicateDB := &refreshTokenDBStub{rowsAffected: 0}
	active, err = NewRefreshTokenRepo(duplicateDB).RevokeIfActive(context.Background(), token, expiresAt)
	if err != nil {
		t.Fatalf("duplicate RevokeIfActive() error = %v", err)
	}
	if active {
		t.Fatal("duplicate RevokeIfActive() = true, want false")
	}
}
