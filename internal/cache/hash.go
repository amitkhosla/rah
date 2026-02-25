package cache

import (
	"encoding/binary"

	"github.com/zeebo/xxh3"
)

func Hash128(tenantID uint16, key []byte) [16]byte {

	h := xxh3.New()

	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], tenantID)

	h.Write(buf[:])
	h.Write(key)

	sum := h.Sum128()

	var out [16]byte
	binary.LittleEndian.PutUint64(out[0:8], sum.Lo)
	binary.LittleEndian.PutUint64(out[8:16], sum.Hi)

	return out
}
