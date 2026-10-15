package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
	"github.com/vincommerce/backend/internal/mail"
)

// TaskSendEmail delivers one transactional email.
//
// Reads the payload and calls mail.Send. It does nothing else: no database, no
// retry policy beyond asynq's, no interpretation. The sending process is not the
// process that decided the email was owed, which is the whole point -- see
// EmailEnqueuer.
const TaskSendEmail = "email:send"

// emailPayload is the task body.
//
// Marshalled here rather than stored as a struct pointer so the task survives a
// restart: an asynq task is bytes in Redis, and a payload that only exists as a
// Go value in the producer's memory is not a queue.
type emailPayload struct {
	To       string         `json:"to"`
	Subject  string         `json:"subject"`
	Template string         `json:"template"`
	Data     map[string]any `json:"data,omitempty"`
}

// EmailEnqueuer is the producer half: it defers a transactional email to the
// worker instead of sending it inline.
//
// WHY THIS EXISTS. `mail.Client.Send` costs one SMTP round trip per call, and the
// checkout path makes up to three of them before it can respond. So a buyer
// submitting an order waits on the mail server for work that has nothing to do
// with whether their order was accepted -- and when SMTP is slow rather than down,
// the request is slow rather than failing, which is the harder case to notice.
//
// It lives in this package, not in `service`, because it is the piece that knows
// about asynq: the service layer takes it as an interface it defines itself, so the
// import boundary holds and the service can be tested without Redis.
type EmailEnqueuer struct {
	client *asynq.Client
}

// NewEmailEnqueuer builds the producer.
func NewEmailEnqueuer(client *asynq.Client) *EmailEnqueuer {
	return &EmailEnqueuer{client: client}
}

// EnqueueEmail returns an error rather than swallowing it, so the caller can fall
// back to sending directly. A full or unreachable queue must cost a slow request,
// never a missing receipt.
func (e *EmailEnqueuer) EnqueueEmail(
	ctx context.Context, to, subject, template string, data map[string]any,
) error {
	if e == nil || e.client == nil {
		return fmt.Errorf("no queue client is wired")
	}
	payload, err := json.Marshal(emailPayload{To: to, Subject: subject, Template: template, Data: data})
	if err != nil {
		return err
	}
	// EnqueueContext, not Enqueue: a request-scoped context that is already
	// cancelled should not leave a task behind that nobody is waiting for.
	_, err = e.client.EnqueueContext(ctx, asynq.NewTask(TaskSendEmail, payload))
	return err
}

// sendEmailHandler delivers one email.
//
// A failure is returned, not logged and dropped: asynq retries, and a receipt that
// was never sent is exactly the kind of thing that should be retried rather than
// reported once into a log nobody is reading.
func sendEmailHandler(client *mail.Client, logger *slog.Logger) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		if client == nil {
			// The worker is wired without SMTP. Reported rather than silently
			// succeeding, or the queue would drain and look healthy while nothing
			// was ever delivered.
			return fmt.Errorf("no mail client is wired; cannot deliver %s", TaskSendEmail)
		}
		var p emailPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			// A malformed payload will never parse, so retrying it is pointless --
			// asynq.SkipRetry stops the task occupying the queue for ever.
			logger.Error("undeliverable email task: payload is not valid json", "error", err.Error())
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		if p.To == "" || p.Template == "" {
			logger.Error("undeliverable email task: missing recipient or template",
				"has_to", p.To != "", "has_template", p.Template != "")
			return fmt.Errorf("%w: missing recipient or template", asynq.SkipRetry)
		}
		return client.Send(ctx, p.To, p.Subject, p.Template, p.Data)
	}
}
