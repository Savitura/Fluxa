package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// These tests cover the last-owner guard in org_repo.go. The guard is the only
// thing standing between a tenant and having no administrator, so the tests
// assert two things separately: the decision itself (evaluateOwnerGuard) and
// the transaction that enforces it — that the lock is taken first, that a
// refusal writes no membership row, and that every attempt lands in the audit
// trail with its actor and target.

// ── stubs ───────────────────────────────────────────────────────────────────

// guardRow is a pgx.Row that replays one scripted result.
type guardRow struct {
	values []interface{}
	err    error
}

func (r *guardRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("guardRow: %d scan destinations but %d scripted values", len(dest), len(r.values))
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			s, ok := r.values[i].(string)
			if !ok {
				return fmt.Errorf("guardRow: value %d is %T, want string", i, r.values[i])
			}
			*p = s
		case *int:
			n, ok := r.values[i].(int)
			if !ok {
				return fmt.Errorf("guardRow: value %d is %T, want int", i, r.values[i])
			}
			*p = n
		default:
			return fmt.Errorf("guardRow: unsupported scan destination %T", d)
		}
	}
	return nil
}

// guardExec is one statement the transaction was asked to run.
type guardExec struct {
	query string
	args  []interface{}
}

// guardTx is a recording dbTx: QueryRow replays a queue of scripted reads and
// Exec records the statement, so a test can assert exactly which SQL reached
// the database and in what order.
type guardTx struct {
	mu        sync.Mutex
	rows      []guardRow
	execs     []guardExec
	commits   int
	rollbacks int
}

func (tx *guardTx) Begin(context.Context) (dbTx, error) {
	return nil, errors.New("guardTx: nested Begin is not expected")
}

func (tx *guardTx) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.execs = append(tx.execs, guardExec{query: query, args: args})
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func (tx *guardTx) QueryRow(_ context.Context, _ string, _ ...interface{}) pgx.Row {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if len(tx.rows) == 0 {
		return &guardRow{err: errors.New("guardTx: no scripted row left")}
	}
	row := tx.rows[0]
	tx.rows = tx.rows[1:]
	return &row
}

func (tx *guardTx) Commit(context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.commits++
	return nil
}

func (tx *guardTx) Rollback(context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	tx.rollbacks++
	return nil
}

func (tx *guardTx) membershipWrites() []guardExec {
	var out []guardExec
	for _, e := range tx.execs {
		switch norm(e.query) {
		case "UPDATE organization_members SET role = $1 WHERE tenant_id = $2 AND user_id = $3",
			"DELETE FROM organization_members WHERE tenant_id = $1 AND user_id = $2":
			out = append(out, e)
		}
	}
	return out
}

func (tx *guardTx) auditWrites() []guardExec {
	var out []guardExec
	for _, e := range tx.execs {
		if strings.HasPrefix(norm(e.query), "INSERT INTO organization_member_audit") {
			out = append(out, e)
		}
	}
	return out
}

// norm collapses a statement's whitespace so tests can compare it as one line.
func norm(query string) string { return strings.Join(strings.Fields(query), " ") }

// guardPool hands out one prepared transaction.
type guardPool struct{ tx *guardTx }

func (p guardPool) Begin(context.Context) (dbTx, error) { return p.tx, nil }

func newGuardRepo(tx *guardTx) *OrgRepo { return &OrgRepo{tx: guardPool{tx: tx}} }

// ── audit assertions ────────────────────────────────────────────────────────

// auditRecord is one decoded organization_member_audit row. The user columns
// are pointers in the insert, so a null is tracked separately from a value.
type auditRecord struct {
	tenant     string
	actor      string
	target     string
	action     string
	prevRole   string
	newRole    string
	outcome    string
	actorNull  bool
	targetNull bool
}

func auditRecordOf(t *testing.T, e guardExec) auditRecord {
	t.Helper()
	if len(e.args) != 7 {
		t.Fatalf("audit insert has %d args, want 7 (tenant, actor, target, action, prev, new, outcome)", len(e.args))
	}
	rec := auditRecord{}
	col := func(i int, into *string) *bool {
		t.Helper()
		if e.args[i] == nil {
			isNull := true
			return &isNull
		}
		s, ok := e.args[i].(string)
		if !ok {
			t.Fatalf("audit column %d is %T, want a string or nil", i, e.args[i])
		}
		*into = s
		isNull := false
		return &isNull
	}
	col(0, &rec.tenant)
	rec.actorNull = *col(1, &rec.actor)
	rec.targetNull = *col(2, &rec.target)
	col(3, &rec.action)
	col(4, &rec.prevRole)
	col(5, &rec.newRole)
	col(6, &rec.outcome)
	return rec
}

func wantAudit(t *testing.T, got auditRecord, want auditRecord) {
	t.Helper()
	if got != want {
		t.Fatalf("audit row =\n  %+v\nwant\n  %+v", got, want)
	}
}

func onlyAudit(t *testing.T, tx *guardTx) auditRecord {
	t.Helper()
	rows := tx.auditWrites()
	if len(rows) != 1 {
		t.Fatalf("recorded %d audit rows, want exactly 1", len(rows))
	}
	return auditRecordOf(t, rows[0])
}

func assertNoMembershipWrite(t *testing.T, tx *guardTx) {
	t.Helper()
	if writes := tx.membershipWrites(); len(writes) != 0 {
		t.Fatalf("membership table was written (%d statements), want none: %q", len(writes), norm(writes[0].query))
	}
}

// ── fixtures ────────────────────────────────────────────────────────────────

const (
	tenantID = "11111111-1111-1111-1111-111111111111"
	ownerA   = "aaaaaaaa-0000-0000-0000-000000000001"
	ownerB   = "aaaaaaaa-0000-0000-0000-000000000002"
	memberC  = "bbbbbbbb-0000-0000-0000-000000000003"
)

// preparedTx scripts the three reads the guard performs, in the order it issues
// them: lock the tenant, count owners, read the target's current role.
func preparedTx(owners int, role string) *guardTx {
	return &guardTx{rows: []guardRow{
		{values: []interface{}{tenantID}},
		{values: []interface{}{owners}},
		{values: []interface{}{role}},
	}}
}

// ── the invariant ───────────────────────────────────────────────────────────

func TestEvaluateOwnerGuard(t *testing.T) {
	tests := []struct {
		name           string
		owners         int
		targetIsOwner  bool
		keepsOwnership bool
		wantRefused    bool
	}{
		{"sole owner demoted is refused", 1, true, false, true},
		{"sole owner removed is refused", 1, true, false, true},
		{"sole owner re-stated as owner is allowed", 1, true, true, false},
		{"one of two owners demoted is allowed", 2, true, false, false},
		{"one of two owners removed is allowed", 2, true, false, false},
		{"promoting a non-owner is always allowed", 1, false, true, false},
		{"demoting a non-owner is allowed", 1, false, false, false},
		{"an ownerless tenant refuses removal until an owner is promoted", 0, true, false, true},
		{"an ownerless tenant still allows a promotion", 0, false, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := evaluateOwnerGuard(tc.owners, tc.targetIsOwner, tc.keepsOwnership)
			if tc.wantRefused {
				if !errors.Is(err, domain.ErrLastOrgOwner) {
					t.Fatalf("evaluateOwnerGuard(owners=%d, targetIsOwner=%t, keepsOwnership=%t) = %v, want ErrLastOrgOwner",
						tc.owners, tc.targetIsOwner, tc.keepsOwnership, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("evaluateOwnerGuard(owners=%d, targetIsOwner=%t, keepsOwnership=%t) = %v, want nil",
					tc.owners, tc.targetIsOwner, tc.keepsOwnership, err)
			}
		})
	}
}

// ── the guard refuses the last owner ────────────────────────────────────────

func TestDemotingSoleOwnerReturnsConflictAndChangesNoRows(t *testing.T) {
	tx := preparedTx(1, domain.RoleOwner)

	err := newGuardRepo(tx).UpdateMemberRole(context.Background(), tenantID, ownerA, ownerA, domain.RoleAdmin)
	if !errors.Is(err, domain.ErrLastOrgOwner) {
		t.Fatalf("UpdateMemberRole() error = %v, want ErrLastOrgOwner (mapped to 409 by the handler)", err)
	}
	assertNoMembershipWrite(t, tx)
	wantAudit(t, onlyAudit(t, tx), auditRecord{
		tenant:   tenantID,
		actor:    ownerA,
		target:   ownerA,
		action:   domain.OrgAuditRoleUpdated,
		prevRole: domain.RoleOwner,
		newRole:  domain.RoleAdmin,
		outcome:  domain.OrgAuditOutcomeRejected,
	})
	if tx.commits != 1 {
		t.Fatalf("commits = %d, want 1: the refusal must be committed so the audit row survives", tx.commits)
	}
}

func TestRemovingSoleOwnerReturnsConflictAndChangesNoRows(t *testing.T) {
	tx := preparedTx(1, domain.RoleOwner)

	err := newGuardRepo(tx).RemoveMember(context.Background(), tenantID, ownerA, ownerA)
	if !errors.Is(err, domain.ErrLastOrgOwner) {
		t.Fatalf("RemoveMember() error = %v, want ErrLastOrgOwner (mapped to 409 by the handler)", err)
	}
	assertNoMembershipWrite(t, tx)
	wantAudit(t, onlyAudit(t, tx), auditRecord{
		tenant: tenantID,
		actor:  ownerA,
		target: ownerA,
		action: domain.OrgAuditMemberRemoved,
		// A removal has no resulting role; the role it gave up is still kept.
		prevRole: domain.RoleOwner,
		outcome:  domain.OrgAuditOutcomeRejected,
	})
}

func TestSelfDemotionByTheOnlyOwnerIsRecordedAgainstItself(t *testing.T) {
	tx := preparedTx(1, domain.RoleOwner)

	// The actor demoting themselves is the exact shape of the bug: nobody is
	// left who can undo it, and the audit must name the same user twice.
	err := newGuardRepo(tx).UpdateMemberRole(context.Background(), tenantID, ownerA, ownerA, domain.RoleViewer)
	if !errors.Is(err, domain.ErrLastOrgOwner) {
		t.Fatalf("UpdateMemberRole() error = %v, want ErrLastOrgOwner", err)
	}
	rec := onlyAudit(t, tx)
	if rec.actorNull || rec.targetNull {
		t.Fatalf("self-demotion audit must name an actor and a target, got %+v", rec)
	}
	if rec.actor != rec.target {
		t.Fatalf("audit actor = %q, target = %q; want the same user", rec.actor, rec.target)
	}
	if rec.outcome != domain.OrgAuditOutcomeRejected {
		t.Fatalf("audit outcome = %q, want %q", rec.outcome, domain.OrgAuditOutcomeRejected)
	}
	assertNoMembershipWrite(t, tx)
}

// ── the guard allows the operation when an owner remains ────────────────────

func TestDemotingOneOfTwoOwnersSucceedsAndIsAudited(t *testing.T) {
	tx := preparedTx(2, domain.RoleOwner)

	if err := newGuardRepo(tx).UpdateMemberRole(context.Background(), tenantID, memberC, ownerA, domain.RoleAdmin); err != nil {
		t.Fatalf("UpdateMemberRole() error = %v, want nil: a second owner remains", err)
	}
	writes := tx.membershipWrites()
	if len(writes) != 1 {
		t.Fatalf("recorded %d membership writes, want 1", len(writes))
	}
	if got := writes[0].args; got[0] != domain.RoleAdmin || got[1] != tenantID || got[2] != ownerA {
		t.Fatalf("update args = %v, want [%s %s %s]", got, domain.RoleAdmin, tenantID, ownerA)
	}
	wantAudit(t, onlyAudit(t, tx), auditRecord{
		tenant:   tenantID,
		actor:    memberC,
		target:   ownerA,
		action:   domain.OrgAuditRoleUpdated,
		prevRole: domain.RoleOwner,
		newRole:  domain.RoleAdmin,
		outcome:  domain.OrgAuditOutcomeOK,
	})
	if tx.commits != 1 {
		t.Fatalf("commits = %d, want 1", tx.commits)
	}
}

func TestRemovingANonOwnerLeavesTheLastOwnerAlone(t *testing.T) {
	tx := preparedTx(1, domain.RoleDeveloper)

	if err := newGuardRepo(tx).RemoveMember(context.Background(), tenantID, ownerA, memberC); err != nil {
		t.Fatalf("RemoveMember() error = %v, want nil: the target is not an owner", err)
	}
	writes := tx.membershipWrites()
	if len(writes) != 1 {
		t.Fatalf("recorded %d membership writes, want 1", len(writes))
	}
	if got := writes[0].args; got[0] != tenantID || got[1] != memberC {
		t.Fatalf("delete args = %v, want [%s %s]", got, tenantID, memberC)
	}
	wantAudit(t, onlyAudit(t, tx), auditRecord{
		tenant:   tenantID,
		actor:    ownerA,
		target:   memberC,
		action:   domain.OrgAuditMemberRemoved,
		prevRole: domain.RoleDeveloper,
		outcome:  domain.OrgAuditOutcomeOK,
	})
}

func TestDemotingANonOwnerIsNotBlockedByTheLastOwnerGuard(t *testing.T) {
	// The decision has to be driven by the role the target holds *now*, not by
	// the role it is being given. A sole owner and a developer being demoted to
	// a viewer is not a last-owner situation, and must be allowed.
	tx := preparedTx(1, domain.RoleDeveloper)

	if err := newGuardRepo(tx).UpdateMemberRole(context.Background(), tenantID, ownerA, memberC, domain.RoleViewer); err != nil {
		t.Fatalf("UpdateMemberRole() error = %v, want nil: the target is not an owner, so a single owner remains", err)
	}
	if len(tx.membershipWrites()) != 1 {
		t.Fatal("demoting a non-owner did not write the membership row")
	}
	wantAudit(t, onlyAudit(t, tx), auditRecord{
		tenant:   tenantID,
		actor:    ownerA,
		target:   memberC,
		action:   domain.OrgAuditRoleUpdated,
		prevRole: domain.RoleDeveloper,
		newRole:  domain.RoleViewer,
		outcome:  domain.OrgAuditOutcomeOK,
	})
}

func TestPromotionIsAlwaysAllowed(t *testing.T) {
	// A viewer becoming the second owner is the documented way out of the
	// guard, so it must not be blocked even with a single owner present.
	tx := preparedTx(1, domain.RoleViewer)

	if err := newGuardRepo(tx).UpdateMemberRole(context.Background(), tenantID, ownerA, memberC, domain.RoleOwner); err != nil {
		t.Fatalf("UpdateMemberRole() error = %v, want nil", err)
	}
	if len(tx.membershipWrites()) != 1 {
		t.Fatal("promotion did not write the membership row")
	}
	wantAudit(t, onlyAudit(t, tx), auditRecord{
		tenant:   tenantID,
		actor:    ownerA,
		target:   memberC,
		action:   domain.OrgAuditRoleUpdated,
		prevRole: domain.RoleViewer,
		newRole:  domain.RoleOwner,
		outcome:  domain.OrgAuditOutcomeOK,
	})
}

// ── a target outside the tenant ─────────────────────────────────────────────

func TestTargetInAnotherTenantIsNotFoundAndAudited(t *testing.T) {
	// The role lookup is scoped by tenant, so a user who belongs to some other
	// organization simply is not a member here.
	tx := &guardTx{rows: []guardRow{
		{values: []interface{}{tenantID}},
		{values: []interface{}{1}},
		{err: pgx.ErrNoRows},
	}}

	err := newGuardRepo(tx).UpdateMemberRole(context.Background(), tenantID, ownerA, memberC, domain.RoleAdmin)
	if !errors.Is(err, domain.ErrOrgMemberNotFound) {
		t.Fatalf("UpdateMemberRole() error = %v, want ErrOrgMemberNotFound (404)", err)
	}
	assertNoMembershipWrite(t, tx)
	wantAudit(t, onlyAudit(t, tx), auditRecord{
		tenant:     tenantID,
		actor:      ownerA,
		action:     domain.OrgAuditRoleUpdated,
		newRole:    domain.RoleAdmin,
		outcome:    domain.OrgAuditOutcomeNotFound,
		targetNull: true, // the target is not a user of this tenant
	})
}

// ── concurrency ─────────────────────────────────────────────────────────────

// raceStore stands in for the tenant row lock and the membership table: a mutex
// held from the `SELECT ... FOR UPDATE` until commit stands in for the row lock,
// and a role map stands in for organization_members.
//
// passTheLockPoint is what makes the concurrency tests measure behaviour rather
// than statement text. Each transaction calls it once it has finished locking
// the tenant and is about to read the owner count, and it waits there for the
// other transaction. With the row lock in place the second transaction cannot
// arrive until the first has committed, so the wait ends on its deadline and the
// two requests are genuinely serialized — the second one then reads the owner
// count the first committed. Drop the `FOR UPDATE` and both transactions arrive
// together and read the same owner count, which is precisely the race the guard
// exists to close, and the tests fail with two successes.
type raceStore struct {
	// rowLock is the tenant row lock. It also makes every read and write of roles
	// safe, exactly as the real row lock does.
	rowLock sync.Mutex
	roles   map[string]string

	arriveMu sync.Mutex
	arrived  int
	both     chan struct{}
	bothOnce sync.Once
}

func newRaceStore(roles map[string]string) *raceStore {
	return &raceStore{roles: roles, both: make(chan struct{})}
}

func (s *raceStore) passTheLockPoint() {
	s.arriveMu.Lock()
	s.arrived++
	if s.arrived == 2 {
		s.bothOnce.Do(func() { close(s.both) })
	}
	s.arriveMu.Unlock()

	select {
	case <-s.both:
	case <-time.After(lockRendezvousTimeout):
	}
}

// lockRendezvousTimeout bounds the wait at the lock point. It only expires when
// the row lock is working, in which case the other transaction is blocked behind
// this one and will read the committed owner count instead.
const lockRendezvousTimeout = 50 * time.Millisecond

type raceTx struct {
	store  *raceStore
	locked bool
	closed bool
}

type racePool struct{ store *raceStore }

func (p racePool) Begin(context.Context) (dbTx, error) { return &raceTx{store: p.store}, nil }

func (tx *raceTx) Begin(context.Context) (dbTx, error) {
	return nil, errors.New("raceTx: nested Begin is not expected")
}

func (tx *raceTx) QueryRow(_ context.Context, query string, args ...interface{}) pgx.Row {
	q := norm(query)
	switch {
	case strings.HasPrefix(q, "SELECT id FROM tenants"):
		tenant, _ := args[0].(string)
		// `SELECT … FOR UPDATE` on the tenant is what serializes two concurrent
		// changes, modelled as a mutex held until commit or rollback. A read
		// without FOR UPDATE still returns a row — it just takes no lock — so the
		// tests measure the behaviour the lock buys, not the statement text.
		if strings.Contains(q, "FOR UPDATE") {
			tx.store.rowLock.Lock()
			tx.locked = true
		}
		tx.store.passTheLockPoint()
		return &guardRow{values: []interface{}{tenant}}
	case strings.HasPrefix(q, "SELECT COUNT(*)"):
		owners := 0
		for _, role := range tx.store.roles {
			if role == domain.RoleOwner {
				owners++
			}
		}
		return &guardRow{values: []interface{}{owners}}
	case strings.HasPrefix(q, "SELECT role FROM organization_members"):
		user, _ := args[1].(string)
		role, ok := tx.store.roles[user]
		if !ok {
			return &guardRow{err: pgx.ErrNoRows}
		}
		return &guardRow{values: []interface{}{role}}
	}
	return &guardRow{err: fmt.Errorf("raceTx: unexpected read %q", q)}
}

func (tx *raceTx) Exec(_ context.Context, query string, args ...interface{}) (pgconn.CommandTag, error) {
	switch norm(query) {
	case "UPDATE organization_members SET role = $1 WHERE tenant_id = $2 AND user_id = $3":
		role, _ := args[0].(string)
		user, _ := args[2].(string)
		if _, ok := tx.store.roles[user]; !ok {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		tx.store.roles[user] = role
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "DELETE FROM organization_members WHERE tenant_id = $1 AND user_id = $2":
		user, _ := args[1].(string)
		if _, ok := tx.store.roles[user]; !ok {
			return pgconn.NewCommandTag("DELETE 0"), nil
		}
		delete(tx.store.roles, user)
		return pgconn.NewCommandTag("DELETE 1"), nil
	}
	if strings.HasPrefix(norm(query), "INSERT INTO organization_member_audit") {
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.CommandTag{}, fmt.Errorf("raceTx: unexpected statement %q", norm(query))
}

func (tx *raceTx) Commit(context.Context) error {
	tx.release()
	return nil
}

func (tx *raceTx) Rollback(context.Context) error {
	tx.release()
	return nil
}

func (tx *raceTx) release() {
	if tx.locked && !tx.closed {
		tx.closed = true
		tx.store.rowLock.Unlock()
	}
}

func (s *raceStore) ownerCount() int {
	owners := 0
	for _, role := range s.roles {
		if role == domain.RoleOwner {
			owners++
		}
	}
	return owners
}

type concurrentResult struct {
	target string
	err    error
}

// runConcurrently releases both goroutines at the same instant, so neither
// request is serialized by the scheduler.
func runConcurrently(t *testing.T, run func(target string) error, targets ...string) []concurrentResult {
	t.Helper()
	start := make(chan struct{})
	results := make(chan concurrentResult, len(targets))
	for _, target := range targets {
		go func(target string) {
			<-start
			results <- concurrentResult{target: target, err: run(target)}
		}(target)
	}
	close(start)

	out := make([]concurrentResult, 0, len(targets))
	for range targets {
		out = append(out, <-results)
	}
	return out
}

func assertExactlyOneSucceeded(t *testing.T, results []concurrentResult) {
	t.Helper()
	succeeded, refused := 0, 0
	for _, r := range results {
		switch {
		case r.err == nil:
			succeeded++
		case errors.Is(r.err, domain.ErrLastOrgOwner):
			refused++
		default:
			t.Fatalf("request against %s failed unexpectedly: %v", r.target, r.err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("concurrent requests: %d succeeded and %d were refused, want exactly 1 of each", succeeded, refused)
	}
}

func TestConcurrentDemotionsOfTheLastTwoOwnersCannotBothSucceed(t *testing.T) {
	store := newRaceStore(map[string]string{ownerA: domain.RoleOwner, ownerB: domain.RoleOwner})
	repo := &OrgRepo{tx: racePool{store: store}}

	results := runConcurrently(t, func(target string) error {
		return repo.UpdateMemberRole(context.Background(), tenantID, memberC, target, domain.RoleAdmin)
	}, ownerA, ownerB)

	assertExactlyOneSucceeded(t, results)
	if got := store.ownerCount(); got != 1 {
		t.Fatalf("tenant has %d owners after two concurrent demotions, want 1", got)
	}
}

func TestConcurrentRemovalsOfTheLastTwoOwnersCannotBothSucceed(t *testing.T) {
	store := newRaceStore(map[string]string{ownerA: domain.RoleOwner, ownerB: domain.RoleOwner})
	repo := &OrgRepo{tx: racePool{store: store}}

	results := runConcurrently(t, func(target string) error {
		return repo.RemoveMember(context.Background(), tenantID, memberC, target)
	}, ownerA, ownerB)

	assertExactlyOneSucceeded(t, results)
	if got := store.ownerCount(); got != 1 {
		t.Fatalf("tenant has %d owners after two concurrent removals, want 1", got)
	}
}

func TestConcurrentDemotionAndRemovalOfTheLastTwoOwnersCannotBothSucceed(t *testing.T) {
	// A demotion racing a removal is the same invariant reached two ways; the
	// loser must be refused rather than left as the tenant's last owner.
	store := newRaceStore(map[string]string{ownerA: domain.RoleOwner, ownerB: domain.RoleOwner})
	repo := &OrgRepo{tx: racePool{store: store}}

	start := make(chan struct{})
	done := make(chan concurrentResult, 2)
	go func() {
		<-start
		done <- concurrentResult{target: ownerA, err: repo.UpdateMemberRole(context.Background(), tenantID, memberC, ownerA, domain.RoleAdmin)}
	}()
	go func() {
		<-start
		done <- concurrentResult{target: ownerB, err: repo.RemoveMember(context.Background(), tenantID, memberC, ownerB)}
	}()
	close(start)
	results := []concurrentResult{<-done, <-done}

	assertExactlyOneSucceeded(t, results)
	if got := store.ownerCount(); got != 1 {
		t.Fatalf("tenant has %d owners after a demotion racing a removal, want 1", got)
	}
}
