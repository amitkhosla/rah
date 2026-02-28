package cache

import (
	"encoding/binary"
	"hash/maphash"
)

var cacheHashSeed = maphash.MakeSeed()

func Hash128(tenantID uint16, key []byte) [16]byte {
	var out [16]byte

	binary.LittleEndian.PutUint64(out[0:8], hash64WithTenant(tenantID, key))
	binary.LittleEndian.PutUint64(out[8:16], keyFingerprint64(key))

	return out
}

func hash64WithTenant(tenantID uint16, key []byte) uint64 {
	var h maphash.Hash
	h.SetSeed(cacheHashSeed)

	var t [2]byte
	binary.LittleEndian.PutUint16(t[:], tenantID)
	_, _ = h.Write(t[:])
	_, _ = h.Write(key)
	return h.Sum64()
}

func keyFingerprint64(key []byte) uint64 {
	n := len(key)
	if n == 0 {
		return 0
	}

	idx := [8]int{0, n / 2, n / 4, (3 * n) / 4, n - 1, n/2 + 1, n/4 + 1, (3*n)/4 + 1}
	var b [8]byte
	for i := range idx {
		j := idx[i]
		if j >= n {
			j = n - 1
		}
		b[i] = key[j]
	}
	return binary.LittleEndian.Uint64(b[:])
}
