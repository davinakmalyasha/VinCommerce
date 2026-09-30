package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vincommerce/backend/internal/repository"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every account code the ledger can compose must FIT the column that stores it,
// and every personal account it composes must be creatable by the schema.
//
// Two failures, both latent, both of which disable the ledger for every money
// movement that touches a seller:
//
//  1. TOO NARROW. `ledger_accounts.code`, `ledger_entries.account_code` and
//     `account_balances.account_code` are VARCHAR(48). `PersonalAccount(userID)`
//     is "seller_available:" + a 36-character uuid = 53. Every posting that
//     debits or credits a personal account therefore fails on width -- and
//     `postLedger` logs the failure and returns nothing, so the wallet movement
//     commits with no journal behind it and nobody finds out until a trial
//     balance is read.
//  2. NEVER CREATED. `ledger_entries.account_code` REFERENCES
//     `ledger_accounts(code)`, and nothing ever calls `EnsurePersonalAccount`, so
//     the row does not exist and the insert fails on the foreign key instead.
//     Same swallowed error, same silent hole.
//
// The width is read out of the migration rather than hardcoded, so widening the
// column is what makes this pass -- and so re-narrowing it fails here instead of
// in production. The point is not the number 48; it is that the number and the
// codes are asserted against each other, which is the only way a change to either
// is caught before a payout.

// permissiveQuerier accepts any statement.
//
// Needed because `postLedgerStrict` now ENSURES ACCOUNTS before it posts, so a test
// that drives it needs a working handle -- a nil one would panic inside the ensure
// rather than reaching the posting validation the test is about. Its counters are
// what the account-existence assertions read.
type permissiveQuerier struct {
	mu     sync.Mutex
	exects []string
}

func (q *permissiveQuerier) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	q.mu.Lock()
	q.exects = append(q.exects, sql)
	q.mu.Unlock()
	return pgconn.CommandTag{}, nil
}

func (q *permissiveQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("Query is not used by a journal posting")
}

func (q *permissiveQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	return flexRow{}
}

// flexRow scans zeros into whatever destinations it is given.
//
// Exists because a BALANCED journal reaches `PostJournal`, which selects the
// journal id and then reads the row back -- so a fake that answers nothing panics
// deep inside the repository rather than at the thing under test. Filling zeros
// keeps both the one-destination and the fourteen-destination shapes working, so a
// test can use a realistic journal instead of a deliberately broken one.
type flexRow struct{}

func (flexRow) Scan(dest ...any) error {
	for _, d := range dest {
		switch p := d.(type) {
		case *string:
			*p = "00000000-0000-0000-0000-0000000000aa"
		case *float64:
			*p = 0
		case *int:
			*p = 0
		case *bool:
			*p = false
		case **time.Time:
			*p = nil
		case *time.Time:
			*p = time.Time{}
		default:
			return errors.New("flexRow: unsupported destination type")
		}
	}
	return nil
}

// accountEnsures counts the account-creation statements the ensure issued.
func (q *permissiveQuerier) accountEnsures() (accounts, balances int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, s := range q.exects {
		if strings.Contains(s, "INSERT INTO ledger_accounts") {
			accounts++
		}
		if strings.Contains(s, "INSERT INTO account_balances") {
			balances++
		}
	}
	return accounts, balances
}

// personalAccountUserID is the ONLY decision in ensuring accounts exist, and it is
// pure, so it is tested by value rather than by source.
//
// Getting it wrong fails in both directions. Missing a personal account means the
// journal's foreign key rejects the insert and `postLedger` swallows it -- the money
// moves with nothing recording it, which is the defect this whole change exists to
// close. Treating a SYSTEM account as personal is worse: `ledger_accounts_user_side`
// requires a system account to have no `user_id`, so creating one fails the
// constraint -- and it would fail on the very first capture, which posts to
// `escrow_held` and `gateway_clearing`.
func TestAPersonalAccountIsDistinguishedFromASystemAccount(t *testing.T) {
	const uid = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"

	for _, c := range []struct {
		code    string
		wantID  string
		persona bool
	}{
		{PersonalAccount(uid), uid, true},
		{SellerHeldAccount(uid), uid, true},
		// System accounts: seeded by 00043, never missing, never per-user.
		{AccEscrowHeld, "", false},
		{AccGatewayClearing, "", false},
		{AccCodReceivable, "", false},
		{AccSellerPending, "", false},
		{AccBankClearing, "", false},
		{AccPlatformCommission, "", false},
		// A bare prefix with no id is not an account. Creating one would insert a
		// row with an empty user_id, which is neither a valid personal account nor a
		// valid system one.
		{personalAccountPrefix, "", false},
		{heldAccountPrefix, "", false},
		// A prefix-shaped string that is NOT in the namespace.
		{"seller_pending:" + uid, "", false},
	} {
		gotID, gotPersona := personalAccountUserID(c.code)
		if gotPersona != c.persona || gotID != c.wantID {
			t.Errorf("personalAccountUserID(%q) = (%q, %v), want (%q, %v)",
				c.code, gotID, gotPersona, c.wantID, c.persona)
		}
	}
}

// A posting must ensure the accounts it needs, ONCE each, before it reaches Post.
//
// Behavioural rather than a source assertion, because the ensure is a real
// statement and the count is observable. `postLedgerStrict` validates the journal's
// entries before it touches the repository, so an unbalanced spec fails there --
// AFTER the ensure has run, which is exactly the ordering being asserted.
func TestAPostingEnsuresEveryPersonalAccountItPostsTo(t *testing.T) {
	svc := &PaymentService{ledger: NewLedgerService(&repository.LedgerRepository{}, nil)}
	q := &permissiveQuerier{}

	// Two legs on the SAME seller, one on a system account, one on a second seller.
	spec := JournalSpec{
		IdempotencyKey: "test:two-sellers",
		TxType:         TxTypePayout,
		Entries: []LedgerEntry{
			Debit(PersonalAccount(testUserID), 50000),
			Debit(PersonalAccount(testUserID), 20000),
			Credit(AccSellerPending, 70000),
			Credit(PersonalAccount("11111111-1111-1111-1111-111111111111"), 70000),
		},
	}
	// Deliberately unbalanced, so Post returns an error we do not have to care
	// about; the ensure has already happened by then.
	_ = svc.postLedgerStrict(t.Context(), q, spec)

	accounts, balances := q.accountEnsures()
	// Two distinct personal codes: the seller's available account and the
	// seller's... second one. NOT three -- the two legs on the same code must be
	// issued once, or the fix is noisy enough to get disabled later.
	if accounts != 2 {
		t.Errorf("issued %d account creations, want 2 (one per distinct personal code):\n%v",
			accounts, q.exects)
	}
	// Every account needs a balance row too, or `applyBalances` has nothing to
	// upsert into and the first posting fails on that foreign key instead.
	if balances != accounts {
		t.Errorf("issued %d balance rows for %d accounts; both are needed or the "+
			"posting fails on the other foreign key", balances, accounts)
	}
	// And the system account must NOT have been created.
	for _, s := range q.exects {
		if strings.Contains(s, AccSellerPending) || strings.Contains(s, "seller_pending") {
			t.Errorf("a system account was ensured:\n%s\n"+
				"ledger_accounts_user_side requires a system account to have no "+
				"user_id, so creating one fails on the first capture", s)
		}
	}
}

// The SWALLOWING posting must ensure accounts too. It is the one escrow release,
// payout request, payout settlement and post-release refund use -- every path that
// silently lost its journal.
func TestTheSwallowingPostingAlsoEnsuresAccounts(t *testing.T) {
	svc := &PaymentService{ledger: NewLedgerService(&repository.LedgerRepository{}, nil)}
	q := &permissiveQuerier{}

	svc.postLedger(t.Context(), q, JournalSpec{
		IdempotencyKey: "test:release",
		TxType:         TxTypeEscrowRelease,
		Entries: []LedgerEntry{
			Debit(AccEscrowHeld, 100000),
			Credit(PersonalAccount(testUserID), 98000),
			Credit(AccPlatformCommission, 2000),
		},
	})

	accounts, _ := q.accountEnsures()
	if accounts != 1 {
		t.Errorf("the swallowing posting issued %d account creations, want 1:\n%v",
			accounts, q.exects)
	}
}

// The service and the repository each hold a copy of the namespace prefixes, and
// the repository's comment has long claimed a test compares them. Now one does --
// there are TWO namespaces and a third could be added without either side noticing.
func TestAccountPrefixesMatchTheService(t *testing.T) {
	repoSrc, err := os.ReadFile(filepath.Join("..", "repository", "ledger_repo.go"))
	if err != nil {
		t.Fatalf("read ledger_repo.go: %v", err)
	}
	for _, prefix := range []string{personalAccountPrefix, heldAccountPrefix} {
		if !strings.Contains(string(repoSrc), `"`+prefix+`"`) {
			t.Errorf("the repository does not know the %q namespace:\n"+
				"the two copies must agree, or a posting to that account is ensured "+
				"under one name and looked up under another", prefix)
		}
	}
	if len(personalAccountPrefixes) != len(repository.PersonalAccountPrefixes()) {
		t.Errorf("the service knows %d personal namespaces and the repository %d:\n"+
			"  service:    %v\n  repository: %v",
			len(personalAccountPrefixes), len(repository.PersonalAccountPrefixes()),
			personalAccountPrefixes, repository.PersonalAccountPrefixes())
	}
}

// failingQuerier rejects every statement, so the account-ensure step FAILS.
//
// Needed because the distinction the two posting helpers make is precisely what
// happens when the ensure fails: the strict one must stop, the swallowing one must
// continue and log. A fake that always succeeds cannot tell those apart, which is
// how mutation M31 -- turning the strict ensure failure into a log -- survived the
// first version of these tests.
//
// It RECORDS every statement, across Exec AND QueryRow, because "returned an
// error" is not the property under test. Under M31 the helper logs the ensure
// failure, calls `Post` anyway, and `Post` fails too -- so both versions return an
// error and an error-value assertion passes against the broken one. The difference
// is whether the posting was ATTEMPTED at all.
//
// QueryRow matters for the same reason: `PostJournal` issues its
// `INSERT INTO ledger_journals ... RETURNING id` as a QueryRow, so a fake that
// recorded only Exec would see no attempt in either version and M31 would survive
// again.
type failingQuerier struct {
	err  error
	mu   sync.Mutex
	seen []string
}

func (q *failingQuerier) record(sql string) {
	q.mu.Lock()
	q.seen = append(q.seen, sql)
	q.mu.Unlock()
}

func (q *failingQuerier) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	q.record(sql)
	return pgconn.CommandTag{}, q.err
}

func (q *failingQuerier) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	q.record(sql)
	return nil, q.err
}

func (q *failingQuerier) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	q.record(sql)
	return failingRow{err: q.err}
}

// attemptedPost reports whether the journal insert was tried, by any verb.
func (q *failingQuerier) attemptedPost() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, s := range q.seen {
		if strings.Contains(s, "INSERT INTO ledger_journals") {
			return true
		}
	}
	return false
}

type failingRow struct{ err error }

func (r failingRow) Scan(dest ...any) error { return r.err }

// A failed account ensure must STOP the strict posting BEFORE it posts.
//
// Otherwise the foreign key rejects the insert anyway, `Post` returns the error,
// and -- on the swallowing path -- the money has already moved with nothing
// recording it. That is the whole defect, and the strict helper exists to prevent
// it. Stopping is also the only way the operator learns: the caller gets an error it
// can act on, and the journal is never half-written.
func TestAFailedAccountEnsureStopsTheStrictPosting(t *testing.T) {
	boom := errors.New("permission denied for table ledger_accounts")
	svc := &PaymentService{ledger: NewLedgerService(&repository.LedgerRepository{}, nil)}
	q := &failingQuerier{err: boom}

	err := svc.postLedgerStrict(t.Context(), q, JournalSpec{
		IdempotencyKey: "test:ensure-fails",
		TxType:         TxTypeEscrowRelease,
		Entries: []LedgerEntry{
			Debit(AccEscrowHeld, 100000),
			Credit(PersonalAccount(testUserID), 100000),
		},
	})
	if err == nil {
		t.Fatal("a failed account ensure let the posting continue; the foreign key " +
			"would reject the insert anyway and the money would move unrecorded")
	}
	// The assertion that distinguishes this from "logged and carried on". Both
	// versions return an error -- the ensure's, or Post's -- so the error VALUE says
	// nothing. Whether the journal insert was attempted says everything.
	if q.attemptedPost() {
		t.Error("the posting continued to the journal insert after the account " +
			"ensure failed; a posting that cannot ensure its accounts must not reach " +
			"the database")
	}
}

// The swallowing path is different ON PURPOSE: the movement has already committed,
// so returning an error would invite a retry of money that already moved. It must log
// and continue -- which means it must not panic either, since the ensure is now the
// first thing it does.
func TestAFailedAccountEnsureDoesNotPanicTheSwallowingPosting(t *testing.T) {
	svc := &PaymentService{ledger: NewLedgerService(&repository.LedgerRepository{}, nil)}
	// Must not panic: a panic in a post-commit path is the worst possible outcome.
	svc.postLedger(t.Context(), &failingQuerier{err: errors.New("boom")}, JournalSpec{
		IdempotencyKey: "test:ensure-fails",
		TxType:         TxTypeEscrowRelease,
		Entries: []LedgerEntry{
			Debit(AccEscrowHeld, 100000),
			Credit(PersonalAccount(testUserID), 100000),
		},
	})
}

// A posting helper with no ledger at all must refuse before it tries to ensure
// anything, rather than dereferencing a nil service.
func TestAPostingWithNoLedgerRefusesRatherThanEnsuring(t *testing.T) {
	svc := &PaymentService{} // no ledger
	if err := svc.postLedgerStrict(t.Context(), &permissiveQuerier{}, JournalSpec{
		IdempotencyKey: "test:no-ledger",
		TxType:         TxTypePayout,
		Entries:        []LedgerEntry{Debit(PersonalAccount(testUserID), 1)},
	}); err == nil {
		t.Error("a posting with no ledger returned no error; it would create an " +
			"account and post nothing, which is the shape of the defect")
	}
}

// accountColumnWidth returns the width an account-code column ends up with after
// every migration has run.
//
// NOT just the width 00043 declared. 00043 created these columns at VARCHAR(48)
// and 00046 widened them, so reading the CREATE TABLE alone reports a width the
// database no longer has and the test fails forever against correct code. A check
// that reads the wrong half of the schema is worse than none, because it is right
// about a thing that is not true.
//
// So: the declaration first, then every later `ALTER COLUMN ... TYPE VARCHAR(n)`
// for that table and column, in migration order. The result is the schema a fresh
// database would actually have.
func accountColumnWidth(t *testing.T, table, column string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "db", "migrations"))
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	// Readdir is sorted by filename, and the files are zero-padded, so this is
	// migration order. Assert it rather than assuming: an unsorted application
	// would silently apply the wrong width.
	sort.Strings(names)

	decl := regexp.MustCompile(column + `\s+VARCHAR\((\d+)\)`)
	alter := regexp.MustCompile(
		`ALTER\s+TABLE\s+` + table + `\s+ALTER\s+COLUMN\s+` + column + `\s+TYPE\s+VARCHAR\((\d+)\)`)

	width := -1
	found := false
	for _, name := range names {
		raw, rerr := os.ReadFile(filepath.Join("..", "db", "migrations", name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		body := string(raw)
		if !found {
			if m := decl.FindStringSubmatch(body); m != nil {
				width, found = atoiOrFail(t, m[1], name), true
			}
			continue
		}
		if m := alter.FindStringSubmatch(body); m != nil {
			width = atoiOrFail(t, m[1], name)
		}
	}
	if !found {
		t.Fatalf("no VARCHAR width found for %s.%s in any migration; this test "+
			"cannot check anything and would pass for the wrong reason", table, column)
	}
	return width
}

func atoiOrFail(t *testing.T, s, where string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("%s: %q is not a number", where, s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// accountCodesUnderTest is every code the service can produce.
//
// A real uuid shape, because the width problem is arithmetic on the uuid's
// length: 8-4-4-4-12 is 36 characters including the hyphens, and a short test id
// would fit where a real one does not.
const (
	testUserID      = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	testUserIDShort = "abc"
)

func composedAccountCodes() map[string]string {
	codes := map[string]string{}
	for _, s := range []string{
		AccBankClearing, AccGatewayClearing, AccCodReceivable, AccRefundPayable,
		AccChargebackReceivable, AccEscrowHeld, AccSellerAvailable, AccSellerPending,
		AccSellerHeld, AccTaxPayablePpn, AccTaxWithheldPph22, AccPlatformEquity,
		AccPlatformCommission, AccPlatformAds, AccPlatformPromo, AccOtherIncome,
		AccGatewayFee, AccRefundWriteoff, AccChargebackLoss, AccRoundingDust,
	} {
		codes[s] = s
	}
	codes["PersonalAccount(real uuid)"] = PersonalAccount(testUserID)
	codes["PersonalAccount(short)"] = PersonalAccount(testUserIDShort)
	codes["SellerHeldAccount(real uuid)"] = SellerHeldAccount(testUserID)
	return codes
}

func TestEveryAccountCodeFitsTheColumnThatStoresIt(t *testing.T) {
	width := accountColumnWidth(t, "ledger_accounts", "code")
	entriesWidth := accountColumnWidth(t, "ledger_entries", "account_code")
	balancesWidth := accountColumnWidth(t, "account_balances", "account_code")

	for name, code := range composedAccountCodes() {
		for _, col := range []struct {
			where string
			width int
		}{
			{"ledger_accounts.code", width},
			{"ledger_entries.account_code", entriesWidth},
			{"account_balances.account_code", balancesWidth},
		} {
			if len(code) > col.width {
				t.Errorf("%s = %q is %d characters, and %s is VARCHAR(%d); every "+
					"posting to it fails on width, and postLedger swallows the error so "+
					"the money moves with no journal",
					name, code, len(code), col.where, col.width)
			}
		}
	}
}

// The posting helpers that touch a personal account, asserted directly. These are
// the four that must work for escrow release, a refund after release, a withdrawal
// and a failed-withdrawal reversal; none of them can succeed on the current
// schema.
func TestThePostingsThatTouchAPersonalAccountCanBeWritten(t *testing.T) {
	width := accountColumnWidth(t, "ledger_entries", "account_code")
	for name, entries := range map[string][]LedgerEntry{
		"escrow release":    releaseEntries(testUserID, 100000, 98000, 2000),
		"refund, released":  refundEntries(testUserID, 30000, 29400, 600, true),
		"payout requested":  payoutRequestedEntries(testUserID, 50000),
		"payout failed":     payoutFailedEntries(testUserID, 50000),
		"seller hold":       sellerHoldEntries(testUserID, 50000),
		"seller hold freed": sellerHoldReleaseEntries(testUserID, 50000),
	} {
		for _, e := range entries {
			if len(e.Account) > width {
				t.Errorf("%s posts to %q (%d chars) into a VARCHAR(%d)", name, e.Account, len(e.Account), width)
			}
		}
	}
}

// A personal account must be creatable, and there must be ROOM for two per user:
// the spendable account and the held account. `idx_ledger_accounts_user` is
// `UNIQUE (user_id) WHERE NOT is_system`, which permits exactly one, so
// `seller_held:<uuid>` cannot be created alongside `seller_available:<uuid>` --
// and the reconcile query already looks for it there.
func TestTheSchemaAllowsBothPersonalAccountsForOneSeller(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "db", "migrations", "00043_double_entry_ledger.sql"))
	if err != nil {
		t.Fatalf("read 00043: %v", err)
	}
	migrations := string(raw)

	// Every migration, not just 00043, since the constraint may be relaxed later.
	entries, err := os.ReadDir(filepath.Join("..", "db", "migrations"))
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			b, rerr := os.ReadFile(filepath.Join("..", "db", "migrations", e.Name()))
			if rerr == nil {
				migrations += "\n" + string(b)
			}
		}
	}

	if !strings.Contains(migrations, "ledger_accounts_purpose_key") &&
		!regexp.MustCompile(`ON\s+ledger_accounts\s*\(\s*user_id\s*,\s*purpose\s*\)`).MatchString(migrations) {
		t.Errorf("no unique key distinguishes a seller's AVAILABLE account from their "+
			"HELD one, so at most one personal account per user can exist:\n"+
			"  %s\n"+
			"SellerHeldAccount composes a code that must be inserted, and reconcileWallets "+
			"already LEFT JOINs account_balances on it, so the held half of the design "+
			"cannot be built without this", SellerHeldAccount(testUserID))
	}
}
