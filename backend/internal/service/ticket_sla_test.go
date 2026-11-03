package service

import (
	"testing"
	"time"
)

func TestSlaDueForMatchesThePublishedPolicy(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		priority string
		want     time.Duration
	}{
		{"urgent", 4 * time.Hour},
		{"high", 24 * time.Hour},
		{"normal", 24 * time.Hour},
		{"low", 7 * 24 * time.Hour},
		// Unknown priorities must not produce a zero time (instant breach)
		// or a nil deadline (no SLA at all); they take the normal tier.
		{"", 24 * time.Hour},
		{"whenever", 24 * time.Hour},
	}
	for _, c := range cases {
		due := slaDueFor(c.priority, now)
		if due == nil {
			t.Errorf("slaDueFor(%q) = nil, want +%v", c.priority, c.want)
			continue
		}
		if got := due.Sub(now); got != c.want {
			t.Errorf("slaDueFor(%q) = +%v, want +%v", c.priority, got, c.want)
		}
	}
}
