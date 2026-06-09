package gatewaylog

import "sync"

const defaultBufCap = 512 // initial capacity of each pooled buffer

// LogBuf is a reusable byte buffer used as the unit of data flowing through the ring.
type LogBuf struct {
	b []byte
}

// Write appends p to the buffer. Implements io.Writer.
func (lb *LogBuf) Write(p []byte) (int, error) {
	lb.b = append(lb.b, p...)
	return len(p), nil
}

// WriteString appends s to the buffer.
func (lb *LogBuf) WriteString(s string) (int, error) {
	lb.b = append(lb.b, s...)
	return len(s), nil
}

// Bytes returns the current content.
func (lb *LogBuf) Bytes() []byte {
	return lb.b
}

// Len returns current length.
func (lb *LogBuf) Len() int {
	return len(lb.b)
}

// Reset clears the buffer without releasing memory.
func (lb *LogBuf) Reset() {
	lb.b = lb.b[:0]
}

// BufPool wraps sync.Pool to manage LogBuf recycling.
type BufPool struct {
	p sync.Pool
}

// NewBufPool creates a pool whose buffers start at initCap bytes capacity.
func NewBufPool(initCap int) *BufPool {
	if initCap <= 0 {
		initCap = defaultBufCap
	}
	return &BufPool{
		p: sync.Pool{
			New: func() any {
				return &LogBuf{
					b: make([]byte, 0, initCap),
				}
			},
		},
	}
}

// Get returns a reset LogBuf from the pool (or allocates a new one).
func (p *BufPool) Get() *LogBuf {
	lb := p.p.Get().(*LogBuf)
	lb.Reset()
	return lb
}

// Put returns lb to the pool after Reset(). Safe to call with nil.
func (p *BufPool) Put(lb *LogBuf) {
	if lb == nil {
		return
	}
	lb.Reset()
	p.p.Put(lb)
}
