package sfu

import "errors"

var errRoomFull = errors.New("room_full")

func IsRoomFull(err error) bool { return errors.Is(err, errRoomFull) }
