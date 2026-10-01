package httpserver

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"
)

type wsWriter struct {
	ch   chan []byte
	done chan struct{}
	once sync.Once
}

func newWriter(conn *websocket.Conn) *wsWriter {
	w := &wsWriter{ch: make(chan []byte, 64), done: make(chan struct{})}
	go func() {
		for {
			select {
			case <-w.done:
				return
			case payload := <-w.ch:
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := conn.Write(ctx, websocket.MessageText, payload)
				cancel()
				if err != nil {
					w.Close()
					return
				}
			}
		}
	}()
	return w
}

func (w *wsWriter) Send(payload []byte) bool {
	select {
	case <-w.done:
		return false
	case w.ch <- append([]byte(nil), payload...):
		return true
	default:
		return false
	}
}

func (w *wsWriter) Depth() int { return len(w.ch) }

func (w *wsWriter) Close() {
	w.once.Do(func() { close(w.done) })
}
