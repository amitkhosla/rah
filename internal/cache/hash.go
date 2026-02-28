package cache

import (
	"encoding/binary"
	"hash/fnv"
)

func Hash128(tenantID uint16, key []byte) [16]byte {
	// Seed with tenant id and derive two independent 64-bit digests.
	seed := []byte{byte(tenantID), byte(tenantID >> 8)}

	h1 := fnv.New64a()
	h1.Write(seed)
	h1.Write(key)
	lo := h1.Sum64()

	h2 := fnv.New64()
	h2.Write(seed)
	h2.Write(key)
	hi := h2.Sum64()

	var out [16]byte
	binary.LittleEndian.PutUint64(out[0:8], lo)
	binary.LittleEndian.PutUint64(out[8:16], hi)

	return out
}
