package hub

import "testing"

type fakeConn struct {
	id  string
	got [][]byte
}

func (f *fakeConn) UserID() string { return f.id }
func (f *fakeConn) Send(payload []byte) bool {
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
