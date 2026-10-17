package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

// These tests cover the four-eyes rule on transfer approvals (#391): the
// creator of a transfer may never approve it, whatever required_approvals is,
// and API-key-created requests record the key that created them.

// approvalRow replays one scripted row for the CastVote SELECT ... FOR UPDATE.
type approvalRow struct {
	values []interface{}
	err    error
}

func (r *approvalRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("approvalRow: %d scan destinations but %d scripted values", len(dest), len(r.values))
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = r.values[i].(string)
		case *int:
			*p = r.values[i].(int)
		case *time.Time:
			*p = r.values[i].(time.Time)
		default:
			return fmt.Errorf("approvalRow: unsupported scan destination %T", d)
		}
	}
	return nil
}

// approvalTx is a pgx.Tx that records statements. Methods the repo never calls
// on a transaction fall through to the nil embedded interface and panic.
type approvalTx struct {
	pgx.Tx
	lockRow   *approvalRow
	execs     []string
	committed bool
}

func (tx *approvalTx) Exec(_ context.Context, query string, _ ...interface{}) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, query)
	return pgconn.NewCommandTag("OK"), nil
}

func (tx *approvalTx) QueryRow(_ context.Context, query string, _ ...interface{}) pgx.Row {
	if strings.Contains(query, "FOR UPDATE") {
		return tx.lockRow
	}
	return &approvalRow{err: errors.New("approvalTx: unexpected query " + query)}
}

func (tx *approvalTx) Commit(context.Context) error   { tx.committed = true; return nil }
func (tx *approvalTx) Rollback(context.Context) error { return nil }

// approvalDB hands out approvalTx and records non-transactional Exec calls.
type approvalDB struct {
	tx       *approvalTx
	execSQL  string
	execArgs []interface{}
}

func (db *approvalDB) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	db.execSQL, db.execArgs = query, args
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (db *approvalDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("approvalDB: Query not scripted")
}
func (db *approvalDB) QueryRow(context.Context, string, ...interface{}) pgx.Row {
	return &approvalRow{err: errors.New("approvalDB: QueryRow not scripted")}
}
func (db *approvalDB) Begin(context.Context) (pgx.Tx, error) { return db.tx, nil }

func TestCheckSelfApproval(t *testing.T) {
	cases := []struct {
		name     string
		creator  string
		actor    string
		decision string
		wantErr  bool
	}{
		{"creator approves", "user-1", "user-1", "approved", true},
		{"creator rejects", "user-1", "user-1", "rejected", false},
		{"other user approves", "user-1", "user-2", "approved", false},
		{"api-key creator, user approves", "", "user-2", "approved", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSelfApproval(tc.creator, tc.actor, tc.decision)
			if tc.wantErr != errors.Is(err, domain.ErrApprovalCreatorCannotApprove) {
				t.Fatalf("checkSelfApproval(%q,%q,%q) = %v, wantErr %v", tc.creator, tc.actor, tc.decision, err, tc.wantErr)
			}
		})
	}
}

func TestCastVoteBlocksCreatorEvenWithSingleRequiredApproval(t *testing.T) {
	for _, required := range []int{1, 2} {
		t.Run(fmt.Sprintf("required=%d", required), func(t *testing.T) {
			tx := &approvalTx{lockRow: &approvalRow{values: []interface{}{
				"txn-1", "user-1", required, "pending", time.Now().Add(time.Hour),
			}}}
			repo := NewTransferApprovalRepo(&approvalDB{tx: tx})

			_, release, err := repo.CastVote(context.Background(), "tenant-1", "req-1", "user-1", "approved", "")

			if !errors.Is(err, domain.ErrApprovalCreatorCannotApprove) {
				t.Fatalf("err = %v, want ErrApprovalCreatorCannotApprove", err)
			}
			if release {
				t.Fatal("self-approval must not release the transfer")
			}
			if len(tx.execs) != 0 || tx.committed {
				t.Fatalf("self-approval must write nothing; execs=%v committed=%v", tx.execs, tx.committed)
			}
		})
	}
}

func TestCreateRequestRecordsAPIKeyCreator(t *testing.T) {
	db := &approvalDB{}
	repo := NewTransferApprovalRepo(db)
	req := &domain.TransferApprovalRequest{
		ID: "req-1", TenantID: "tenant-1", TransactionID: "txn-1",
		CreatorAPIKeyID: "key-1", Asset: "USDC", Amount: decimal.NewFromInt(500),
		RequiredApprovals: 1, Status: "pending", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}

	if err := repo.CreateRequest(context.Background(), req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if !strings.Contains(db.execSQL, "creator_api_key_id") {
		t.Fatalf("insert does not write creator_api_key_id: %s", db.execSQL)
	}
	if db.execArgs[3] != "" || db.execArgs[4] != "key-1" {
		t.Fatalf("creator args = (%v, %v), want (\"\", \"key-1\")", db.execArgs[3], db.execArgs[4])
	}
}
