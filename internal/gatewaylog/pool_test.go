package gatewaylog

import (
	"bytes"
	"sync"
	"testing"
)

// TestLogBufWrite tests that Write appends correctly and Bytes/Len match.
func TestLogBufWrite(t *testing.T) {
	lb := &LogBuf{}

	// Test Write
	n, err := lb.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if n != 5 {
		t.Errorf("Write returned n=%d, want 5", n)
	}

	if !bytes.Equal(lb.Bytes(), []byte("hello")) {
		t.Errorf("Bytes returned %q, want \"hello\"", string(lb.Bytes()))
	}

	if lb.Len() != 5 {
		t.Errorf("Len returned %d, want 5", lb.Len())
	}

	// Test WriteString
	n, err = lb.WriteString(" world")
	if err != nil {
		t.Fatalf("WriteString returned error: %v", err)
	}
	if n != 6 {
		t.Errorf("WriteString returned n=%d, want 6", n)
	}

	if !bytes.Equal(lb.Bytes(), []byte("hello world")) {
		t.Errorf("Bytes returned %q, want \"hello world\"", string(lb.Bytes()))
	}

	if lb.Len() != 11 {
		t.Errorf("Len returned %d, want 11", lb.Len())
	}
}

// TestLogBufReset tests that Reset clears content but retains capacity.
func TestLogBufReset(t *testing.T) {
	lb := &LogBuf{}

	// Write some data
	lb.Write([]byte("test data"))
	originalCap := cap(lb.b)

	if lb.Len() != 9 {
		t.Errorf("Before reset, Len returned %d, want 9", lb.Len())
	}

	// Reset
	lb.Reset()

	if lb.Len() != 0 {
		t.Errorf("After reset, Len returned %d, want 0", lb.Len())
	}

	if !bytes.Equal(lb.Bytes(), []byte{}) {
		t.Errorf("After reset, Bytes returned %q, want empty", string(lb.Bytes()))
	}

	// Capacity should be retained
	if cap(lb.b) != originalCap {
		t.Errorf("After reset, capacity is %d, want %d", cap(lb.b), originalCap)
	}
}

// TestBufPoolGetPut tests that Get returns non-nil and Put/Get reuses underlying array.
func TestBufPoolGetPut(t *testing.T) {
	pool := NewBufPool(256)

	// Get should return non-nil
	lb1 := pool.Get()
	if lb1 == nil {
		t.Fatal("Get returned nil")
	}

	// Verify it has expected capacity
	if cap(lb1.b) < 256 {
		t.Errorf("Get returned buffer with capacity %d, want at least 256", cap(lb1.b))
	}

	// Write some data
	lb1.Write([]byte("test message"))
	originalCap := cap(lb1.b)

	// Put it back
	pool.Put(lb1)

	// Get another one - should be reset and reuse same memory
	lb2 := pool.Get()
	if lb2 == nil {
		t.Fatal("Get after Put returned nil")
	}

	if lb2.Len() != 0 {
		t.Errorf("Get after Put returned buffer with length %d, want 0 (reset)", lb2.Len())
	}

	if cap(lb2.b) < 256 {
		t.Errorf("Get after Put returned buffer with capacity %d, want at least 256", cap(lb2.b))
	}

	// Verify capacity is preserved (the key reuse optimization)
	if cap(lb2.b) == originalCap {
		t.Logf("Pool successfully reused buffer with same capacity: %d", cap(lb2.b))
	}
}

// TestBufPoolConcurrent tests concurrent Get/Write/Put with race detector.
func TestBufPoolConcurrent(t *testing.T) {
	pool := NewBufPool(256)
	numGoroutines := 50
	iterPerGoroutine := 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterPerGoroutine; j++ {
				lb := pool.Get()
				if lb == nil {
					t.Errorf("Get returned nil at iteration %d", j)
					return
				}

				// Write some data
				lb.WriteString("msg")
				if lb.Len() == 0 {
					t.Errorf("WriteString failed at iteration %d", j)
					return
				}

				// Put it back
				pool.Put(lb)
			}
		}(i)
	}

	wg.Wait()
}

// TestBufPoolPutNil tests that Put(nil) does not panic.
func TestBufPoolPutNil(t *testing.T) {
	pool := NewBufPool(256)

	// This should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Put(nil) caused panic: %v", r)
		}
	}()

	pool.Put(nil)
}
