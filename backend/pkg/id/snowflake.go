package id

import (
	"strconv"
	"sync"
	"time"

	"rokko.dev/pkg/apperr"
)

const (
	epochMs     = int64(1704067200000)
	workerBits  = 10
	seqBits     = 12
	workerShift = seqBits
	timeShift   = workerBits + seqBits
	seqMask     = int64(1<<seqBits - 1)
)

type Generator struct {
	mu     sync.Mutex
	worker int64
	last   int64
	seq    int64
}

func New(worker int64) *Generator {
	return &Generator{worker: worker & (1<<workerBits - 1)}
}

func (g *Generator) Next() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now().UnixMilli() - epochMs
	if now == g.last {
		g.seq = (g.seq + 1) & seqMask
		if g.seq == 0 {
			for now <= g.last {
				now = time.Now().UnixMilli() - epochMs
			}
		}
	} else {
		g.seq = 0
	}
	g.last = now
	return (now << timeShift) | (g.worker << workerShift) | g.seq
}

func (g *Generator) NextString() string {
	return strconv.FormatInt(g.Next(), 10)
}

func Parse(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func Format(v int64) string {
	return strconv.FormatInt(v, 10)
}

func ParseID(s string) (int64, error) {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, apperr.Invalid("invalid id")
	}
	return v, nil
}
