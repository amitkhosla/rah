package clock

import (
	"time"
)

// GlobalState is a single cache-line friendly struct
type GlobalClock struct {
	ElapsedSec  uint32
	MinuteID    uint32
	HourID      uint32
	DayID       uint32
	UnixCurTime int64
}

var CurrentClock GlobalClock

func (clock *GlobalClock) SetTime(now int64, elapsed uint32) {
	clock.ElapsedSec = elapsed
	clock.MinuteID = elapsed / 60
	clock.HourID = elapsed / 3600
	clock.DayID = elapsed / 86400
	clock.UnixCurTime = now
}

func StartClockHeartbeat() {
	start := time.Now().Unix()

	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		for range ticker.C {
			now := time.Now().Unix()
			elapsed := uint32(now - start)
			CurrentClock.ElapsedSec = elapsed
			CurrentClock.MinuteID = elapsed / 60
			CurrentClock.HourID = elapsed / 3600
			CurrentClock.DayID = elapsed / 86400
			CurrentClock.UnixCurTime = now
		}
	}()
}
