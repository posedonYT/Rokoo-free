package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

type fakeWriter struct {
	msgs     []kafka.Message
	err      error
	deadline bool
}

func (f *fakeWriter) WriteMessages(ctx context.Context, m ...kafka.Message) error {
	if d, ok := ctx.Deadline(); ok && time.Until(d) <= writeTimeout {
		f.deadline = true
	}
	f.msgs = append(f.msgs, m...)
	return f.err
}

func TestWriteBatch(t *testing.T) {
	batch := []Message{
		{ID: 1, Topic: "a", Key: "k1", Payload: []byte(`{"id":"e1"}`)},
		{ID: 2, Topic: "b", Key: "k2", Payload: []byte(`not json`)},
	}
	tests := []struct {
		name    string
		batch   []Message
		werr    error
		wantIDs []int64
	}{
		{"ok", batch, nil, []int64{1, 2}},
		{"write error", batch, errors.New("kafka down"), nil},
		{"empty", nil, nil, []int64{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeWriter{err: tc.werr}
			ids, err := writeBatch(context.Background(), w, tc.batch)
			if !errors.Is(err, tc.werr) {
				t.Fatalf("err=%v", err)
			}
			if !w.deadline {
				t.Error("write must use timeout context")
			}
			if len(ids) != len(tc.wantIDs) {
				t.Fatalf("ids=%v want %v", ids, tc.wantIDs)
			}
			for i := range ids {
				if ids[i] != tc.wantIDs[i] {
					t.Errorf("ids[%d]=%d", i, ids[i])
				}
			}
		})
	}
}

func TestToKafka(t *testing.T) {
	msgs, ids := toKafka([]Message{
		{ID: 7, Topic: "t", Key: "k", Payload: []byte(`{"id":"evt"}`)},
		{ID: 8, Topic: "t", Key: "k", Payload: []byte(`bad`)},
	})
	if len(msgs) != 2 || ids[0] != 7 || ids[1] != 8 {
		t.Fatalf("msgs=%v ids=%v", msgs, ids)
	}
	if string(msgs[0].Headers[0].Value) != "evt" || string(msgs[1].Headers[0].Value) != "" {
		t.Errorf("headers: %v %v", msgs[0].Headers, msgs[1].Headers)
	}
	if msgs[0].Topic != "t" || string(msgs[0].Key) != "k" {
		t.Errorf("bad msg %v", msgs[0])
	}
}
