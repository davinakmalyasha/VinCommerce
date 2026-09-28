package worker

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/vincommerce/backend/internal/config"
)

// blackholeAddr returns a TCP address that accepts connections and then never
// replies — the shape of a firewall DROP or a Redis wedged on a full disk. A
// connection-refused port fails too fast to prove the context deadline is
// actually enforced.
func blackholeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open and say nothing.
			accepted <- c
		}
	}()
	t.Cleanup(func() {
		for {
			select {
			case c := <-accepted:
				_ = c.Close()
			default:
				return
			}
		}
	})
	return ln.Addr().String()
}

// The container HEALTHCHECK runs this in a short-lived process with an 8s
// budget. The Inspector API takes no context, so without the goroutine +
// select in CheckQueue a wedged Redis pins the probe until Docker kills it and
// the worker is reported unhealthy for the wrong reason.
func TestCheckQueueHonoursContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := CheckQueue(ctx, config.RedisConfig{Addr: blackholeAddr(t)})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	// The Inspector's own ReadTimeout is 5s; returning promptly proves the
	// caller's deadline won the race.
	if elapsed > 2*time.Second {
		t.Fatalf("CheckQueue took %s; the caller's deadline did not win", elapsed)
	}
}

func TestCheckQueueUnreachableRedis(t *testing.T) {
	// Port 1 is reserved and never listening: connection refused.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := CheckQueue(ctx, config.RedisConfig{Addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("CheckQueue returned no error for an unreachable Redis")
	}
}
