package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRel(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("read %v: %v", parts, err)
	}
	return string(raw)
}

// THE TWO ENDS HAVE TO AGREE.
//
// A producer that enqueues one task name and a consumer registered under another
// is the quietest possible failure: the enqueue succeeds, the queue accepts it,
// and no handler ever matches. Nothing errors, nothing logs, and the emails simply
// never arrive.
//
// This is the same shape as the CSV import template, where the served header and
// the importer's skip-prefix had to agree and neither side would have noticed if
// they stopped.
func TestTheProducerAndConsumerUseTheSameTaskName(t *testing.T) {
	email := readRel(t, "email.go")
	workerSrc := readRel(t, "worker.go")

	if !strings.Contains(email, `const TaskSendEmail = "email:send"`) {
		t.Errorf("TaskSendEmail is not defined where the producer uses it:\n%s", email)
	}
	// The consumer must be registered under the constant, not a literal that can
	// drift from it.
	if !strings.Contains(workerSrc, "mux.HandleFunc(TaskSendEmail, withTimeout(sendEmailHandler(") {
		t.Errorf("the send-email handler is not registered under TaskSendEmail.\n" +
			"An enqueue that matches no handler succeeds and delivers nothing")
	}
	// And the producer must use the same constant.
	if !strings.Contains(email, "asynq.NewTask(TaskSendEmail, payload)") {
		t.Errorf("the producer does not enqueue under TaskSendEmail:\n%s", email)
	}
}

// A nil mail client must FAIL the task, not return success.
//
// The worker's own Deps documentation says a nil service disables its job rather
// than failing the boot. That is right for a job that scans for work, and wrong
// here: a queue that drains while nothing is delivered looks exactly like a queue
// with nothing to do. The failure is the signal.
func TestAMissingMailClientFailsTheTaskRatherThanDroppingIt(t *testing.T) {
	email := readRel(t, "email.go")

	at := strings.Index(email, "func sendEmailHandler(")
	if at < 0 {
		t.Fatal("sendEmailHandler is gone; update this test")
	}
	body := email[at:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}

	nilAt := strings.Index(body, "client == nil")
	if nilAt < 0 {
		t.Errorf("the handler does not check for a missing mail client.\n%s\n"+
			"Without it a nil client panics inside the handler, which asynq retries "+
			"for ever", body)
		return
	}
	// Scoped to the nil-check BRANCH, not to the rest of the function. The first
	// version took everything from the check to the end, so a later
	// `return fmt.Errorf` for a different failure satisfied it -- and a mutant that
	// made a missing client return nil survived that assertion.
	branchEnd := strings.Index(body[nilAt:], "\n\t\t}")
	if branchEnd < 0 {
		t.Fatal("could not bound the nil-client branch")
	}
	nilBranch := body[nilAt : nilAt+branchEnd]
	if !strings.Contains(nilBranch, "return fmt.Errorf(") {
		t.Errorf("a missing mail client does not fail the task.\n%s\n"+
			"Returning nil would let the queue drain while nothing is delivered", nilBranch)
	}
	if strings.Contains(nilBranch, "return nil") {
		t.Errorf("a missing mail client returns success.\n%s", nilBranch)
	}

	// A malformed payload can never succeed, so it must not be retried.
	if !strings.Contains(body, "asynq.SkipRetry") {
		t.Errorf("a malformed payload is retried.\n%s\n"+
			"It will never parse, so retrying occupies the queue for ever", body)
	}
}

// The lookup is cheap and the send is not, so the send is what gets deferred --
// and a failure to defer must fall back to sending, because a full queue should
// cost a slow request rather than a missing receipt.
func TestEmailIsDeferredAndNeverDropped(t *testing.T) {
	src := readRel(t, "..", "service", "order_service.go")

	at := strings.Index(src, "func (s *OrderService) emailFor(")
	if at < 0 {
		t.Fatal("emailFor is gone; update this test")
	}
	body := src[at:]
	if end := strings.Index(body, "\nfunc "); end > 0 {
		body = body[:end]
	}

	enqueueAt := strings.Index(body, "s.enqueuer.EnqueueEmail(")
	if enqueueAt < 0 {
		t.Errorf("emailFor never enqueues.\n%s\n"+
			"Without it checkout still waits on SMTP", body)
		return
	}
	// The fallback must be in the enqueue's error branch.
	errBranch := body[enqueueAt:]
	if end := strings.Index(errBranch, "\n\t\t}"); end > 0 {
		errBranch = errBranch[:end]
	}
	if !strings.Contains(errBranch, "s.mailer.Send(") {
		t.Errorf("a failed enqueue does not fall back to sending.\n%s\n"+
			"An unreachable queue would then lose the receipt entirely", errBranch)
	}
	// And the no-enqueuer path still sends, so a process wired without a queue
	// behaves exactly as it did before this change.
	if !strings.Contains(body, "\t_ = s.mailer.Send(ctx, buyer.Email, subject, templateName, data)\n}") {
		t.Errorf("emailFor no longer sends inline when there is no enqueuer.\n%s\n"+
			"That is the pre-existing behaviour and a process without a queue must "+
			"keep delivering", body)
	}
}

// Both processes must be wired, and wiring only one is worse than wiring neither:
// an API that enqueues into a worker with no mail client fails every email at
// delivery time, after the request that could have sent it has already returned.
func TestBothProcessesAreWired(t *testing.T) {
	api := readRel(t, "..", "..", "cmd", "api", "main.go")
	workerMain := readRel(t, "..", "..", "cmd", "worker", "main.go")

	if !strings.Contains(api, "SetEmailEnqueuer(worker.NewEmailEnqueuer(") {
		t.Error("the API never sets the enqueuer, so nothing is ever deferred")
	}
	if !strings.Contains(workerMain, "Mail:") {
		t.Error("the worker is built without a mail client, so every deferred email " +
			"fails at delivery time -- after the API request that could have sent it " +
			"inline has already returned")
	}
}
