package lookup

import (
	"encoding/binary"
	"hash/maphash"
	"sync"
)

// Two independently seeded hash functions.
// A key cannot simultaneously collide in both dimensions with probability
// better than 2^-64, so a false positive at the index level is caught by
// the full 128-bit check in region.Read() (header.Fingerprint comparison).
var (
	routingSeed     = maphash.MakeSeed() // H1: shard + bucket selection
	fingerprintSeed = maphash.MakeSeed() // H2: stored in EntryHeader for region-level verification
)

// hasherPair holds two reusable maphash.Hash instances (one per seed).
// Seeded once at pool creation; Reset() restores to post-SetSeed state without
// re-invoking SetSeed, saving ~20ns per Hash128 call on the LongKey path.
type hasherPair struct {
	r maphash.Hash // routing    (seed = routingSeed)
	f maphash.Hash // fingerprint (seed = fingerprintSeed)
}

var hasherPool = sync.Pool{
	New: func() any {
		p := new(hasherPair)
		p.r.SetSeed(routingSeed)
		p.f.SetSeed(fingerprintSeed)
		return p
	},
}

// Hash128 produces a 128-bit fingerprint for (tenantID, key).
//
//	[0:8]  H1 — routing hash: determines shard and bucket within the index.
//	[8:16] H2 — fingerprint: stored in slab EntryHeader, verified on region.Read().
//
// Both components include tenantID so identical keys owned by different tenants
// always produce distinct fingerprints (cross-tenant collision is impossible).
func Hash128(tenantID uint16, key []byte) [16]byte {
	p := hasherPool.Get().(*hasherPair)

	var t [2]byte
	binary.LittleEndian.PutUint16(t[:], tenantID)

	p.r.Reset()
	_, _ = p.r.Write(t[:])
	_, _ = p.r.Write(key)
	h1 := p.r.Sum64()

	p.f.Reset()
	_, _ = p.f.Write(t[:])
	_, _ = p.f.Write(key)
	h2 := p.f.Sum64()

	hasherPool.Put(p)

	var out [16]byte
	binary.LittleEndian.PutUint64(out[0:8], h1)
	binary.LittleEndian.PutUint64(out[8:16], h2)
	return out
}

// makeTagTiny builds the tinyIdx tag for keys ≤ 6 bytes.
// The tag is a lossless encoding of (tenantID, key): no hash, no collision.
//
// Layout:
//
//	≤5B: bit63=0 | keyLen(3b)[62:60] | tenantID(16b)[59:44] | key_exact(40b)[43:4] | spare[3:0]
//	 6B: bit63=1 | tenantID(16b)[62:47] | key_7bit(42b)[46:5] | spare[4:0]
//
// For 6B keys all bytes must be < 128; callers routing to tinyIdx guarantee this.
func makeTagTiny(tenantID uint16, key []byte) uint64 {
	n := len(key)
	var tag uint64
	if n == 6 {
		// 7-bit strip: each byte contributes 7 bits, total 42 bits.
		var k42 uint64
		for i, b := range key {
			k42 |= uint64(b&0x7F) << (uint(i) * 7)
		}
		tag = (uint64(1) << 63) | (uint64(tenantID) << 47) | (k42 << 5)
	} else {
		// Exact encoding: each byte stored verbatim, LSB-first.
		var k40 uint64
		for i, b := range key {
			k40 |= uint64(b) << (uint(i) * 8)
		}
		tag = (uint64(n&0x7) << 60) | (uint64(tenantID) << 44) | (k40 << 4)
	}
	// Sanitise against trie sentinel values.
	if tag == iEmpty {
		tag |= 4 // set spare bit 2
	}
	if tag == iTombstone {
		tag ^= 4 // flip spare bit 2
	}
	return tag
}

// makeTagHashH2 builds the hashIdx tag from a 128-bit fingerprint, packing
// the upper 16 bits of H2 into bits 48-63 of the tag so InlineIndex's
// h2ForTag extracts real fingerprint bytes instead of routing bits.
//
// Layout:
//
//	[47: 0] lower 48 bits of H1 (routing bits for trie traversal)
//	[63:48] upper 16 bits of H2 (fingerprint bytes for H2 filter)
func makeTagHashH2(fp [16]byte) uint64 {
	h1 := binary.LittleEndian.Uint64(fp[0:8])
	h2 := binary.LittleEndian.Uint64(fp[8:16])
	h2word16 := uint16(h2 >> 48)
	if h2word16 == 0 {
		h2word16 = 1
	}
	tag := (h1 & 0x0000FFFFFFFFFFFF) | (uint64(h2word16) << 48)
	if tag == iEmpty {
		tag |= 1 << 48
	}
	if tag == iTombstone {
		tag ^= 1 << 48
	}
	return tag
}
