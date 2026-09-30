package service

import (
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
