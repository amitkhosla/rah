package engine

import (
	"sync/atomic"
	"time"
)

// GlobalState is a single cache-line friendly struct
type GlobalClock struct {
	UnixSec  uint32
	MinuteID uint32
	HourID   uint32
	DayID    uint32
}

// Using atomic.Pointer[T] means Load() returns *T directly (no interface assertion needed)
var CurrentClock atomic.Pointer[GlobalClock]

func StartClockHeartbeat() {
	start := time.Now().Unix()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		for range ticker.C {
			now := time.Now().Unix()
			elapsed := uint32(now - start)

			// PRE-CALCULATE here so workers don't have to divide
			CurrentClock.Store(&GlobalClock{
				UnixSec:  elapsed,
				MinuteID: elapsed / 60,
				HourID:   elapsed / 3600,
				DayID:    elapsed / 86400,
			})
		}
	}()
}

func (g *GlobalClock) GetID(mode uint8) uint32 {
	switch mode {
	case 1:
		return uint32(g.UnixSec / 60) // MINUTE
	case 2:
		return uint32(g.UnixSec / 3600) // HOUR
	default:
		return uint32(g.UnixSec) // SECOND
	}
}
