package cache

import (
	"encoding/binary"

	"github.com/zeebo/xxh3"
)

func Hash128(tenantID uint16, key []byte) [16]byte {

	// Seed the hash with the tenantID to provide isolation
	sum := xxh3.Hash128Seed(key, uint64(tenantID))

	var out [16]byte
	binary.LittleEndian.PutUint64(out[0:8], sum.Lo)
	binary.LittleEndian.PutUint64(out[8:16], sum.Hi)

	return out
}
