package transcode

import (
	"sync"
	"testing"
)

func TestGetPivotReturnsEmptyBuffer(t *testing.T) {
	pb := GetPivot()
	if len(pb.Bytes()) != 0 {
		t.Fatalf("expected empty buffer, got len=%d", len(pb.Bytes()))
	}
	PutPivot(pb)
}

func TestPutAndGetReusesBackingArray(t *testing.T) {
	pb := GetPivot()
	pb.Append([]byte("hello world"))
	cap1 := cap(pb.b)
	PutPivot(pb)

	pb2 := GetPivot()
	if len(pb2.Bytes()) != 0 {
		t.Fatalf("expected reset buffer, got len=%d", len(pb2.Bytes()))
	}
	if cap(pb2.b) < cap1 {
		t.Fatalf("expected capacity to be preserved (>= %d), got %d", cap1, cap(pb2.b))
	}
	PutPivot(pb2)
}

func TestAppendGrowsBuffer(t *testing.T) {
	pb := GetPivot()
	pb.Append([]byte("foo"))
	pb.Append([]byte("bar"))
	got := string(pb.Bytes())
	if got != "foobar" {
		t.Fatalf("expected foobar, got %q", got)
	}
	PutPivot(pb)
}

func TestPivotConcurrent(t *testing.T) {
	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				pb := GetPivot()
				pb.Append([]byte("data"))
				if len(pb.Bytes()) == 0 {
					panic("empty after append")
				}
				PutPivot(pb)
			}
		}(i)
	}
	wg.Wait()
}
