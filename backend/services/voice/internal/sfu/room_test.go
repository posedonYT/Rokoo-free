package sfu

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLeaveReportsEmptyAndGC(t *testing.T) {
	room := NewRoom("r", nil, nil)
	require.NoError(t, room.Join("a", func(string, []byte) {}))
	require.True(t, room.Leave("a"))
	require.True(t, room.Empty())
	require.True(t, room.Leave("missing"))
}

func TestRoomFull(t *testing.T) {
	room := NewRoom("r", nil, nil)
	room.SetLimits(1, 1)
	require.NoError(t, room.Join("a", func(string, []byte) {}))
	err := room.Join("b", func(string, []byte) {})
	require.Error(t, err)
	require.True(t, IsRoomFull(err))
}

func TestConcurrentJoinLeave(t *testing.T) {
	room := NewRoom("r", nil, nil)
	room.SetLimits(8, 4)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := string(rune('a' + n))
			_ = room.Join(id, func(string, []byte) {})
			room.Leave(id)
		}(i)
	}
	wg.Wait()
	require.True(t, room.Empty())
}
