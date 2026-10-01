package id

import (
	"testing"
	"time"
)

func TestSnowflakeMonotonic(t *testing.T) {
	g := New(1)
	prev := g.Next()
	for i := 0; i < 1000; i++ {
		next := g.Next()
		if next <= prev {
			t.Fatalf("id not increasing: %d <= %d", next, prev)
		}
		prev = next
	}
	if _, err := Parse(Format(prev)); err != nil {
		t.Fatal(err)
	}
}

func TestSnowflakeClockRollback(t *testing.T) {
	g := New(1)
	base := g.clock()
	first := g.Next()
	// simulate clock going back 5ms relative to the last issued timestamp
	g.last = base + 5
	calls := 0
	g.clock = func() int64 {
		calls++
		if calls == 1 {
			return base // rolled back
		}
		return base + 6 // caught up
	}
	start := time.Now()
	next := g.Next()
	if next <= first {
		t.Fatalf("id not increasing after rollback: %d <= %d", next, first)
	}
	if time.Since(start) < 4*time.Millisecond {
		t.Fatalf("expected to wait for clock catch-up")
	}
	if next>>timeShift < base+5 {
		t.Fatalf("timestamp went backwards: %d", next>>timeShift)
	}
}
