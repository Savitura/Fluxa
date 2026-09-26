package postgres

import (
	"context"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// DB is the subset of pgxpool.Pool used by repositories. Keeping the
// dependency narrow allows repositories to route reads independently of writes.
type DB interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
	Begin(context.Context) (pgx.Tx, error)
}

type ReplicaAwareDB struct {
	primary   *pgxpool.Pool
	replica   *pgxpool.Pool
	fallbacks atomic.Uint64
}

func NewReplicaAwareDB(primary, replica *pgxpool.Pool) *ReplicaAwareDB {
	return &ReplicaAwareDB{primary: primary, replica: replica}
}

func (db *ReplicaAwareDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return db.primary.Exec(ctx, sql, args...)
}
func (db *ReplicaAwareDB) Begin(ctx context.Context) (pgx.Tx, error) { return db.primary.Begin(ctx) }
func (db *ReplicaAwareDB) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	if db.replica != nil && isRead(sql) {
		rows, err := db.replica.Query(ctx, sql, args...)
		if err == nil {
			return rows, nil
		}
		db.recordFallback(err)
	}
	return db.primary.Query(ctx, sql, args...)
}
func (db *ReplicaAwareDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if db.replica == nil || !isRead(sql) {
		return db.primary.QueryRow(ctx, sql, args...)
	}
	// The primary is deliberately NOT queried here. pgxpool.QueryRow acquires a
	// connection and only releases it when Scan is called, so eagerly building a
	// primary row that the happy path never scans leaks one primary connection
	// per read — the pool is exhausted within a few hundred reads and every
	// subsequent query then blocks until its context deadline. The primary is
	// consulted lazily, inside Scan, only when the replica actually fails.
	return &fallbackRow{
		ctx:        ctx,
		sql:        sql,
		args:       args,
		replica:    db.replica.QueryRow(ctx, sql, args...),
		primary:    db.primary,
		onFallback: db.recordFallback,
	}
}
func (db *ReplicaAwareDB) ReplicaAvailable(ctx context.Context) error {
	if db.replica == nil {
		return nil
	}
	return db.replica.Ping(ctx)
}
func (db *ReplicaAwareDB) FallbackCount() uint64 { return db.fallbacks.Load() }
func (db *ReplicaAwareDB) recordFallback(err error) {
	db.fallbacks.Add(1)
	log.Warn().Err(err).Msg("postgres read replica unavailable; falling back to primary")
}
func isRead(sql string) bool {
	s := strings.TrimSpace(strings.ToUpper(sql))
	return strings.HasPrefix(s, "SELECT") || strings.HasPrefix(s, "WITH") || strings.HasPrefix(s, "SHOW") || strings.HasPrefix(s, "EXPLAIN")
}

// rowQuerier is the slice of the primary pool fallbackRow needs. Narrowing it
// keeps the "primary is untouched unless the replica fails" rule testable
// without a live database.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
}

type fallbackRow struct {
	ctx        context.Context
	sql        string
	args       []interface{}
	replica    pgx.Row
	primary    rowQuerier
	onFallback func(error)
}

func (r *fallbackRow) Scan(dest ...interface{}) error {
	if err := r.replica.Scan(dest...); err != nil {
		r.onFallback(err)
		return r.primary.QueryRow(r.ctx, r.sql, r.args...).Scan(dest...)
	}
	return nil
}
