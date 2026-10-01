package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestWriterOverflowCloses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		writer := newWriter(conn)
		defer writer.Close()
		blocked := false
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if !writer.Send([]byte(`{"type":"x"}`)) {
				blocked = true
				break
			}
		}
		if !blocked {
			t.Error("expected overflow")
		}
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(300 * time.Millisecond)
	_ = conn.Close(websocket.StatusNormalClosure, "")
}

func TestWriterCloseConcurrentSend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		writer := newWriter(conn)
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					writer.Send([]byte(`{"type":"x"}`))
				}
			}()
		}
		time.Sleep(10 * time.Millisecond)
		writer.Close()
		wg.Wait()
		writer.Close()
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+srv.URL[4:], nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")
	time.Sleep(500 * time.Millisecond)
}
