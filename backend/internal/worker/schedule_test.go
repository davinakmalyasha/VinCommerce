package worker

import (
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
