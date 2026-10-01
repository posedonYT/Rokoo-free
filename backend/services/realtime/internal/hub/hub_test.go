package hub

import "testing"

type fakeConn struct {
	id     string
	got    [][]byte
	full   bool
	closed bool
}

func (f *fakeConn) UserID() string { return f.id }
func (f *fakeConn) Close()         { f.closed = true }
func (f *fakeConn) Send(payload []byte) bool {
	if f.full {
		return false
	}
	f.got = append(f.got, append([]byte(nil), payload...))
	return true
}

func TestHubDeliversToEverySession(t *testing.T) {
	h := New()
	a := &fakeConn{id: "1"}
	b := &fakeConn{id: "1"}
	h.Add(a)
	h.Add(b)
	h.Send("1", []byte("hi"))
	if len(a.got) != 1 || len(b.got) != 1 {
		t.Fatalf("sessions=%d %d", len(a.got), len(b.got))
	}
	h.Remove(a)
	if !h.Online("1") {
		t.Fatal("second session should stay online")
	}
	h.Remove(b)
	if h.Online("1") {
		t.Fatal("expected offline")
	}
}

func TestHubClosesSlowConsumers(t *testing.T) {
	h := New()
	slow := &fakeConn{id: "1", full: true}
	ok := &fakeConn{id: "1"}
	h.Add(slow)
	h.Add(ok)
	if h.Send("1", []byte("x")) {
		t.Fatal("expected Send to report a drop")
	}
	if !slow.closed || ok.closed {
		t.Fatalf("slow.closed=%v ok.closed=%v", slow.closed, ok.closed)
	}
	if len(ok.got) != 1 {
		t.Fatalf("healthy session got %d", len(ok.got))
	}
	if !h.Send("1", []byte("y")) || len(ok.got) != 2 || len(slow.got) != 0 {
		t.Fatal("slow session should be gone after drop")
	}
}
