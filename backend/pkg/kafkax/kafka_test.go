package kafkax

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

type fakeDLQ struct {
	msgs []kafka.Message
	err  error
}

func (f *fakeDLQ) WriteMessages(_ context.Context, m ...kafka.Message) error {
	if f.err != nil {
		return f.err
	}
	f.msgs = append(f.msgs, m...)
	return nil
}

func TestHandleWithRetry(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name      string
		failFirst int // сколько первых вызовов handler падает (-1 = всегда)
		dlqErr    error
		wantCalls int
		wantDLQ   int
		wantErr   error
		wantSleep []time.Duration
	}{
		{"ok first try", 0, nil, 1, 0, nil, nil},
		{"ok after 2 failures", 2, nil, 3, 0, nil, retryBackoffs[:2]},
		{"ok on last retry", 4, nil, 5, 0, nil, retryBackoffs},
		{"persistent failure goes to dlq", -1, nil, 5, 1, nil, retryBackoffs},
		{"dlq failure returns error", -1, boom, 5, 0, boom, retryBackoffs},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := func(context.Context, kafka.Message) error {
				calls++
				if tc.failFirst < 0 || calls <= tc.failFirst {
					return errors.New("handler failed")
				}
				return nil
			}
			dlq := &fakeDLQ{err: tc.dlqErr}
			var slept []time.Duration
			sleep := func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
			msg := kafka.Message{Topic: "guild.events", Key: []byte("k"), Value: []byte("v")}

			err := handleWithRetry(context.Background(), h, msg, retryBackoffs, dlq, sleep)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if calls != tc.wantCalls {
				t.Errorf("calls=%d want %d", calls, tc.wantCalls)
			}
			if len(dlq.msgs) != tc.wantDLQ {
				t.Fatalf("dlq=%d want %d", len(dlq.msgs), tc.wantDLQ)
			}
			if len(slept) != len(tc.wantSleep) {
				t.Fatalf("sleeps=%v want %v", slept, tc.wantSleep)
			}
			for i := range slept {
				if slept[i] != tc.wantSleep[i] {
					t.Errorf("sleep[%d]=%v want %v", i, slept[i], tc.wantSleep[i])
				}
			}
			if tc.wantDLQ == 1 && dlq.msgs[0].Topic != "guild.events.dlq" {
				t.Errorf("dlq topic=%s", dlq.msgs[0].Topic)
			}
		})
	}
}

func TestRetryBackoffs(t *testing.T) {
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond}
	for i := range want {
		if retryBackoffs[i] != want[i] {
			t.Fatalf("backoff[%d]=%v", i, retryBackoffs[i])
		}
	}
}

func TestHandleWithRetryContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dlq := &fakeDLQ{}
	err := handleWithRetry(ctx, func(context.Context, kafka.Message) error { return errors.New("x") },
		kafka.Message{Topic: "t"}, retryBackoffs, dlq, sleepCtx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if len(dlq.msgs) != 0 {
		t.Fatal("must not write dlq on cancel")
	}
}

func TestDLQTopic(t *testing.T) {
	if DLQTopic("a.b") != "a.b.dlq" {
		t.Fatal()
	}
}
