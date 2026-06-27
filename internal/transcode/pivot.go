package transcode

import "sync"

// pivotBuf is a reusable scratch buffer for multi-hop conversion steps
// (e.g. xml_to_avro: XML→JSON→Avro). Stored as a struct so sync.Pool
// stores a pointer — no boxing of the slice header.
type pivotBuf struct{ b []byte }

var pivotPool = sync.Pool{
	New: func() any { return &pivotBuf{b: make([]byte, 0, 4096)} },
}

// GetPivot returns a reset scratch buffer from the pool.
func GetPivot() *pivotBuf {
	pb := pivotPool.Get().(*pivotBuf)
	pb.b = pb.b[:0]
	return pb
}

// PutPivot returns the buffer to the pool.
// pivotBuf.b holds only []byte with no pointer fields beyond the backing array
// (which is owned by the pool), so no pointer clearing is needed.
func PutPivot(pb *pivotBuf) { pivotPool.Put(pb) }

// Bytes returns the current contents of the pivot buffer.
func (pb *pivotBuf) Bytes() []byte { return pb.b }

// Append appends p to the pivot buffer.
func (pb *pivotBuf) Append(p []byte) { pb.b = append(pb.b, p...) }
