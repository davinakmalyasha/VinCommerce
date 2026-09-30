package worker

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vincommerce/backend/internal/repository"
	"github.com/vincommerce/backend/internal/service"
)

// The worker's recurring schedule, as data.
//
// These assertions exist because a job missing from this table is completely
// invisible. The worker starts, its health check passes, and it silently never
// runs. The two money jobs are the sharpest case: `ReconcileRefunds` and
// `Reconcile` had zero callers, so a refund the gateway accepted stayed
// `submitted` forever and the derived balance caches were never checked against
// the journal -- with nothing anywhere reporting a problem.

// fullDeps is a worker wired with every gated service present.
//
// The values are zero-value structs on purpose: `jobSpecs` asks only whether a
// dependency is non-nil, so a zero value is a truthful "present" without needing
// a database or a gateway.
func fullDeps() Deps {
	return Deps{
		Sessions: &repository.SessionRepository{},
		Payment:  &service.PaymentService{},
		Ledger:   &service.LedgerService{},
	}
}

func specFor(t *testing.T, d Deps, taskType string) (jobSpec, bool) {
	t.Helper()
	for _, j := range jobSpecs(d) {
		if j.taskType == taskType {
			return j, true
		}
	}
	return jobSpec{}, false
}

func TestEveryJobIsScheduledWhenItsServiceIsWired(t *testing.T) {
	d := fullDeps()
	want := []string{TaskCancelExpiredOrders, TaskAutoCompleteOrders, TaskAdvanceShipped,
		TaskPriceAlerts, TaskBackInStock, TaskCartRecovery, TaskSellerDigest,
		TaskLowStock, TaskSellerPresence, TaskSessionPurge,
		// The two money jobs. These are the point of this test.
		TaskReconcileRefunds, TaskReconcileLedger,
	}
	for _, taskType := range want {
		if _, ok := specFor(t, d, taskType); !ok {
			t.Errorf("%s is not scheduled; the worker would start healthy and never run it", taskType)
		}
	}
}

func TestMoneyJobsAreScheduledWhenTheirServicesArePresent(t *testing.T) {
	// The specific regression: these two methods existed with zero callers, so
	// the ledger and the refund queue were inert.
	d := fullDeps()
	if _, ok := specFor(t, d, TaskReconcileRefunds); !ok {
		t.Error("refund reconciliation is not scheduled; accepted refunds would never settle")
	}
	if _, ok := specFor(t, d, TaskReconcileLedger); !ok {
		t.Error("ledger reconciliation is not scheduled; cache drift would go unnoticed")
	}
}

func TestGatedJobsAreDroppedRatherThanRegisteredToNothing(t *testing.T) {
	// A job registered against a nil service is a panic the first time it fires,
	// which is a task timeout with no useful message rather than a clear absence.
	cases := []struct {
		name    string
		task    string
		absent  func(*Deps)
		present func(*Deps)
	}{
		{"sessions", TaskSessionPurge,
			func(d *Deps) { d.Sessions = nil }, nil},
		{"payment", TaskReconcileRefunds,
			func(d *Deps) { d.Payment = nil }, nil},
		{"ledger", TaskReconcileLedger,
			func(d *Deps) { d.Ledger = nil }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := fullDeps()
			tc.absent(&d)
			if _, ok := specFor(t, d, tc.task); ok {
				t.Errorf("%s is scheduled with no service behind it", tc.task)
			}
		})
	}
}

func TestUngatedJobsSurviveAnEmptyWiring(t *testing.T) {
	// A minimally-wired worker must still run the jobs whose services it has.
	// Over-filtering is its own bug: it would silently disable order expiry, which
	// holds inventory hostage.
	d := Deps{Orders: &service.OrderService{}}
	for _, taskType := range []string{
		TaskCancelExpiredOrders, TaskAutoCompleteOrders, TaskAdvanceShipped,
		TaskCartRecovery, TaskLowStock,
	} {
		if _, ok := specFor(t, d, taskType); !ok {
			t.Errorf("%s disappeared when the money services were absent", taskType)
		}
	}
}

// The Unique TTL must EXCEED the job's period.
//
// asynq's dedup lock expires with the TTL, so a TTL below the interval means the
// lock has already lapsed when the next tick fires: the guard does nothing while
// appearing to protect. Every entry in this table was once wrong in exactly this
// way -- the first version set all ten TTLs just below their intervals, which
// made the dedup inert across the board. A check that cannot fail is worse than
// no check, because it reads like protection.
func TestEveryUniqueTTLExceedsItsJobInterval(t *testing.T) {
	for _, j := range jobSpecs(fullDeps()) {
		period, ok := cronPeriod(j.spec)
		if !ok {
			t.Errorf("%s has an unparseable schedule %q", j.taskType, j.spec)
			continue
		}
		if j.uniq <= period {
			t.Errorf("%s: unique TTL %v does not exceed its %v period, so the "+
				"dedup lock has lapsed before the next tick and does nothing",
				j.taskType, j.uniq, period)
		}
	}
}

// TestNoDuplicateTaskTypes guards against a copy-paste entry that shadows another
// job: asynq registers both, and only one handler ever runs.
func TestNoTaskTypeIsScheduledTwice(t *testing.T) {
	seen := map[string]bool{}
	for _, j := range jobSpecs(fullDeps()) {
		if seen[j.taskType] {
			t.Errorf("%s is scheduled more than once", j.taskType)
		}
		seen[j.taskType] = true
	}
}

// TestEveryJobHasARealInterval guards the `@every`/`@daily` forms. A malformed
// spec makes sched.Register fail at boot, so this is a cheap pre-flight.
func TestEveryJobHasAKnownScheduleForm(t *testing.T) {
	for _, j := range jobSpecs(fullDeps()) {
		if !strings.HasPrefix(j.spec, "@") {
			t.Errorf("%s: schedule %q should use an @every or @daily form", j.taskType, j.spec)
		}
	}
}

// Every task constant in this package must be both scheduled and handled.
//
// THE GENERAL FIX FOR A WHOLE CLASS OF BUG. The other tests here list the tasks
// they expect BY HAND, which means a new `Task*` constant is not covered by any of
// them: nothing cross-checks the constants against the schedule. So
// `TaskReleasePayoutReservations` existed -- with a comment explaining the fraud
// control it represented -- with no handler, no schedule entry, and no failing
// test. The worker started, reported healthy, and never released a single seller
// hold.
//
// Two things are wrong with a constant in that state and both are silent:
//
//   - no handler: asynq has nothing to dispatch to, so a task of that type would
//     be rejected at enqueue and the schedule entry is decorative
//   - no schedule entry: nothing ever enqueues it, so even a correct handler is
//     unreachable
//
// So the expected list is derived from the source rather than written out, and
// every constant must appear in both.
func TestEveryTaskConstantIsScheduledAndHandled(t *testing.T) {
	consts := taskConstantsFromSource(t)
	if len(consts) == 0 {
		t.Fatal("no task constants were found in worker.go; this test is not " +
			"checking anything, which is the failure mode it exists to prevent")
	}

	scheduled := map[string]bool{}
	for _, j := range jobSpecs(fullDeps()) {
		scheduled[j.taskType] = true
	}

	src := workerSource(t)
	for _, c := range taskConstantsFromSource(t) {
		if !scheduled[c.value] {
			t.Errorf("task %s (%q) is declared but never scheduled; nothing will "+
				"ever enqueue it, so any handler for it is unreachable", c.name, c.value)
		}
		// The mux registers the CONSTANT, not its value, so this has to match on
		// the name. Matching the value would find nothing at all and every task
		// would "fail" -- which is a test that cannot distinguish a real gap.
		if !strings.Contains(src, "mux.HandleFunc("+c.name+",") {
			t.Errorf("task %s (%q) has no handler registered on the mux; asynq has "+
				"nothing to dispatch to and a scheduled entry for it is decorative",
				c.name, c.value)
		}
	}
}

// taskConstant is one `Task* = "..."` declaration.
type taskConstant struct{ name, value string }

// taskConstantsFromSource reads every `Task* = "..."` constant out of worker.go.
//
// Derived rather than declared so that adding a constant is enough to make this
// test apply to it. A hand-written list is a list that has to be remembered, and
// the whole defect is something nobody remembered.
func taskConstantsFromSource(t *testing.T) []taskConstant {
	t.Helper()
	src := workerSource(t)
	re := regexp.MustCompile(`(Task\w+)\s*=\s*"([^"]+)"`)
	var out []taskConstant
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		out = append(out, taskConstant{name: m[1], value: m[2]})
	}
	return out
}

func workerSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatalf("read worker.go: %v", err)
	}
	return string(raw)
}

// cronPeriod parses the schedule forms this file uses.
//
// Hand-rolled rather than pulling in a cron library: the table is authored in Go,
// so the parse is checking the table against itself, and a test that validates
// the author rather than the data is not a test.
//
// It is exhaustive on purpose. A form this parser does not recognise makes the
// TTL assertion silently skip that row -- which is the exact failure mode of a
// guard that quietly does not apply.
func cronPeriod(spec string) (time.Duration, bool) {
	switch {
	case spec == "@daily":
		return 24 * time.Hour, true
	case spec == "@hourly":
		return time.Hour, true
	case strings.HasPrefix(spec, "@every "):
		d, err := time.ParseDuration(strings.TrimPrefix(spec, "@every "))
		if err != nil {
			return 0, false
		}
		return d, true
	}
	return 0, false
}
