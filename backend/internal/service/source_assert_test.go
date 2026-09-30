package service

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/vincommerce/backend/internal/payments"
)

// Source-reading helpers for tests that assert on a file rather than on
// behaviour.
//
// WHY THESE EXIST. The double-refund and the inert cap are both invisible to
// behavioural tests: the first is a question of which function the webhook
// reaches, and reaching it requires a live database; the second is a query that
// returns a plausible number either way. Reading the source is crude, and it is
// the only thing that sees them.
//
// The trade-off is real and worth stating: a source assertion breaks on a
// refactor that preserves behaviour. That is an annoyance, not a soundness
// problem -- the failure is a test to re-aim, not a bug shipped. The inverse
// mistake, a behavioural test that cannot fail, is a soundness problem.
//
// Each of these has been paired with a behavioural test wherever one was
// possible: `decideRefundCap` and `refundStateCountsTowardCap` are pure and are
// tested by value; these assert only what the pure tests cannot reach.

// repoRoot is the backend module directory, from the service package.
func repoRoot() string { return ".." }

// readSource reads a file in the same package as the test.
func readSource(t *testing.T, name string) string {
	t.Helper()
	return readFileAt(t, filepath.Join(".", name))
}

// readRepositorySource reads a file in the repository package.
func readRepositorySource(t *testing.T, name string) string {
	t.Helper()
	return readFileAt(t, filepath.Join("..", "repository", name))
}

func readFileAt(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// repositoryRefundKeyFor mirrors repository.refundKeyFor.
//
// It exists so the two key builders can be compared WITHOUT the service test
// importing the repository's unexported helper -- which it cannot, and should not
// be able to. If the two ever disagree, a replayed webhook stops matching and
// every replay becomes a second refund, so the duplication is deliberate and the
// mismatch is a test failure rather than a runtime surprise.
func repositoryRefundKeyFor(gateway, ref string, amount float64) string {
	if ref == "" {
		return ""
	}
	return "provider:" + gateway + ":" + ref + ":" + formatRupiah(amount)
}

// formatRupiah renders a whole-rupiah amount the way both key builders do.
//
// Both sides use `%.0f` on a value already rounded to whole rupiah. Duplicated
// here for the same reason as the key itself: if the format drifted, the two keys
// would disagree and every replayed webhook would become a second refund.
func formatRupiah(v float64) string {
	return strconv.FormatFloat(math.Round(v), 'f', 0, 64)
}

// recordingRefundGateway counts gateway refund calls and models Midtrans's
// acceptance rule: cumulative refunds may not exceed the charge.
//
// That rule is the whole reason the double-refund is expensive rather than merely
// wrong. Without it a second full refund would obviously fail; because Midtrans
// only caps the TOTAL, a repeated PARTIAL refund is accepted and the buyer is
// paid twice.
type recordingRefundGateway struct {
	refundCalls   int
	charge        float64
	cumulativeRef float64
	rejected      []float64
	accepted      []float64
}

func (g *recordingRefundGateway) Name() string { return "midtrans" }

func (g *recordingRefundGateway) CreatePayment(
	context.Context, payments.CreatePaymentInput,
) (*payments.GatewayPayment, error) {
	return nil, nil
}
func (g *recordingRefundGateway) VerifyWebhook(context.Context, []byte, string) (bool, error) {
	return true, nil
}
func (g *recordingRefundGateway) ParseWebhook([]byte) (*payments.GatewayEvent, error) {
	return nil, nil
}

func (g *recordingRefundGateway) Refund(_ context.Context, in payments.RefundInput) (*payments.RefundResult, error) {
	g.refundCalls++
	if g.cumulativeRef+in.Amount > g.charge {
		g.rejected = append(g.rejected, in.Amount)
		return &payments.RefundResult{
			Status:  payments.RefundStatusFailed,
			Message: "exceeds the remaining refundable amount",
		}, nil
	}
	g.cumulativeRef += in.Amount
	g.accepted = append(g.accepted, in.Amount)
	return &payments.RefundResult{
		ProviderRef: "rf-" + in.Reference,
		Status:      payments.RefundStatusSucceeded,
	}, nil
}

func (g *recordingRefundGateway) RefundStatus(context.Context, string) ([]payments.GatewayRefund, error) {
	return nil, nil
}
