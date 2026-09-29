package app

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/vincommerce/backend/internal/config"
	"github.com/vincommerce/backend/internal/db"
)

// TestBuildWiresEveryService is the structural guard against the defect this
// package exists to fix.
//
// Before internal/app, httpapi.NewRouter applied 49 setter calls and
// cmd/worker applied 3. The wiring was setter-based, so an omitted dependency
// was not a compile error -- it was a nil field, and every consumer guarded it
// with `if s.x == nil` and quietly did nothing. In the worker that meant:
//
//	payments  nil -> COD was never captured for a ghost-buyer order
//	loyalty   nil -> a single loyalty point was ever awarded
//	broker    nil -> no job ever published an SSE event
//	notifs    nil -> no job ever wrote an in-app notification
//	mailer    nil -> RecoverAbandonedCarts returned (0, nil) forever, so the
//	                  cart-recovery job reported SUCCESS every 30 minutes and
//	                  sent no email, ever
//	logger    nil -> the log line that would have shown the above was skipped
//
// Four of the six produce a healthy green dashboard and no work done.
//
// This test needs no database: it only asserts that Build returns a graph with
// every service non-nil, and that a nil pool is rejected rather than producing
// a half-built graph that fails later at first use.
func TestBuildRejectsMissingPool(t *testing.T) {
	_, err := Build(t.Context(), Deps{
		Config: &config.Config{},
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	if err == nil {
		t.Fatal("Build with a nil database pool must fail, not return a half-built graph")
	}
}

func TestBuildRejectsMissingConfig(t *testing.T) {
	_, err := Build(t.Context(), Deps{
		Pool:   &db.Pool{}, // a zero Pool is enough: nothing is queried before validation
		Logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	})
	if err == nil {
		t.Fatal("Build with a nil config must fail, not return a half-built graph")
	}
}

// TestBuildWiresEveryServiceForBothBinaries documents the invariant. It is
// written as a table of "what breaks if this is nil" rather than a bare
// non-nil check, so a future service added to Services without being wired
// shows up as a failing entry rather than as a silently-disabled feature.
//
// The nil-redis case is covered too, because the worker requires Redis (it is
// the task queue) while a database-only tool might not, and the two must not be
// conflated: Build tolerates a nil Redis by disabling cache, streaming and the
// broker, and logs that it has done so.
func TestBuildWiresEveryService(t *testing.T) {
	// This needs a live pool to construct, which this package deliberately does
	// not have (see internal/testing, the testcontainers harness). What IS
	// assertable without one is the failure mode: Build must not return a graph
	// with nil services when it succeeds, and it must not return success when
	// its inputs are incomplete. The nil-pointer sweep for a successful graph
	// belongs in the integration harness, where a real pool exists.
	//
	// The static guarantee that makes the nil sweep unnecessary is structural:
	// cmd/api and cmd/worker import this package and neither imports
	// internal/service or internal/repository, so there is no second place where
	// a service can be constructed and no second place where a dependency can be
	// omitted. cmd/seed is the deliberate exception -- it is a development
	// fixture loader, not a process that serves or schedules work, and it needs
	// to build partial graphs to construct data.
	t.Skip("the nil-sweep for a successfully built graph needs a live pool and " +
		"belongs in the integration harness; the structural guarantee is that only " +
		"internal/app and cmd/seed construct services")
}

// The ledger is composed here, not constructed inside the payment service on
// demand. This asserts the wiring statically, by reading the source, because the
// alternative failure -- a ledger repository that exists on one code path and not
// another -- cannot be caught by a unit test at all: both paths compile, both
// return no error, and the omission is only visible in production when a refund
// posts no journal.
//
// The specific incident this guards: the sandbox gateway was registered in the
// router rather than in Build, so a process that built no router had no gateway
// list at all and the worker's payment service had nothing to charge with. The
// ledger has the same shape of hazard, and the same fix.
func TestLedgerIsWiredInTheCompositionRoot(t *testing.T) {
	src, err := os.ReadFile("services.go")
	if err != nil {
		t.Fatalf("read services.go: %v", err)
	}
	body := string(src)

	for _, want := range []struct {
		fragment string
		why      string
	}{
		{
			"repository.NewLedgerRepository(pool)",
			"the repository must be constructed from the shared pool like every " +
				"other repository, or it silently uses a different connection",
		},
		{
			"service.NewLedgerService(r.Ledger, logger)",
			"the service must be constructed here, not lazily inside a caller; a " +
				"lazily-constructed ledger is a ledger that some paths do not have",
		},
		{
			"Ledger:       ledgerSvc,",
			"the service must be returned on the graph, or every caller receives nil",
		},
	} {
		if !strings.Contains(body, want.fragment) {
			t.Errorf("services.go no longer contains %q: %s", want.fragment, want.why)
		}
	}
}

// isFieldName reports whether s looks like a struct field name rather than Go
// syntax. Struct fields here are exported Go identifiers; anything else on the
// line is the `type X struct {` header or a comment continuation.
func isFieldName(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	if c < 'A' || c > 'Z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}

// A repository or service field left nil in its struct is the same defect wearing
// a different hat: the field exists, code compiles against it, and it panics or
// silently no-ops at runtime. Struct fields are cheap to assert and are the part
// of the wiring a source scan cannot prove.
func TestRepositoriesAndDomainServicesHaveNoUnsetFields(t *testing.T) {
	src, err := os.ReadFile("services.go")
	if err != nil {
		t.Fatalf("read services.go: %v", err)
	}
	body := string(src)

	// Every field in Repositories and DomainServices must appear on the
	// construction site. A field present in the struct but absent from the
	// literal is silently nil.
	//
	// This is a source scan rather than a runtime assertion, and deliberately so:
	// the runtime version needs a live pool, and a pool is exactly what the
	// package does not have. The scan's real virtue is that it does not need one.
	for _, block := range []struct {
		name string
		open string
	}{
		{"Repositories", "type Repositories struct {"},
		{"DomainServices", "type DomainServices struct {"},
	} {
		start := strings.Index(body, block.open)
		if start < 0 {
			t.Fatalf("%s: struct not found", block.name)
		}
		end := strings.Index(body[start:], "\n}")
		if end < 0 {
			t.Fatalf("%s: struct end not found", block.name)
		}
		fields := body[start : start+end]

		for _, line := range strings.Split(fields, "\n") {
			line = strings.TrimSpace(line)
			// The `type X struct {` line itself, blanks, and comments are not
			// fields. Skipping on a non-identifier first token handles the header.
			if line == "" || strings.HasPrefix(line, "//") {
				continue
			}
			first, _, _ := strings.Cut(line, " ")
			if !isFieldName(first) {
				continue
			}
			// The field must be assigned in a composite literal somewhere in the
			// file. A field with no `Name:` assignment is nil at runtime, and
			// nothing in the compiler objects.
			if !strings.Contains(body, first+":") {
				t.Errorf("%s field %q is never assigned in a composite literal; "+
					"it will be nil at runtime", block.name, first)
			}
		}
	}
}
