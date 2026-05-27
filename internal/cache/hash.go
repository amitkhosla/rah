package cache

import (
	"encoding/binary"
	"hash/maphash"
	"sync"
)

// Two independently seeded hash functions used by Hash128 (public API / tests).
var (
	routingSeed     = maphash.MakeSeed() // H1: shard + bucket selection
	fingerprintSeed = maphash.MakeSeed() // H2: for Hash128 callers
)

// hasherPair holds two reusable maphash.Hash instances (one per seed).
// Used by Hash128 (public, two-call path) and kept for compatibility.
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

// routingPool holds single-hasher instances for the fast single-pass path.
var routingPool = sync.Pool{
	New: func() any {
		h := new(maphash.Hash)
		h.SetSeed(routingSeed)
		return h
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
//	≤5B: bit63=0 | keyLen(3b)[62:60] | tenantID(16b)[59:44] | key_exact(40b)[43:4] | tenantID[3:0]
//	 6B: bit63=1 | tenantID(16b)[62:47] | key_7bit(42b)[46:5] | tenantID[4:0]
//
// The tenantID low bits are embedded into the spare bits at positions [3:0] /
// [4:0]. Without this, two tenants with the same small key produce tags that
// differ only in the middle bits, meaning their shard selection (tag & 0xFF)
// is identical — they land in the same trie path and one tenant's write can
// silently overwrite the other's entry. Embedding tenantID into the low bits
// makes the shard = tag & 0xFF include tenant contribution, routing different
// tenants to distinct shards even when their keys are identical.
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
		// Embed tenantID's lower 5 bits into spare [4:0] so shard selection
		// (tag & shardMask) includes the tenant → no cross-tenant collisions.
		tag = (uint64(1) << 63) | (uint64(tenantID) << 47) | (k42 << 5) | uint64(tenantID&0x1F)
	} else {
		// Exact encoding: each byte stored verbatim, LSB-first.
		var k40 uint64
		for i, b := range key {
			k40 |= uint64(b) << (uint(i) * 8)
		}
		// Embed tenantID's lower 4 bits into spare [3:0].
		// shard = tag & 0xFF = (key[0]&0xF)<<4 | (tenantID&0xF), giving all 256
		// shards reachable even with few distinct key values across many tenants.
		tag = (uint64(n&0x7) << 60) | (uint64(tenantID) << 44) | (k40 << 4) | uint64(tenantID&0xF)
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

// hashH1Only computes H1 for routing using a single maphash call.
func hashH1Only(tenantID uint16, key []byte) uint64 {
	h := routingPool.Get().(*maphash.Hash)
	var t [2]byte
	binary.LittleEndian.PutUint16(t[:], tenantID)
	h.Reset()
	_, _ = h.Write(t[:])
	_, _ = h.Write(key)
	h1 := h.Sum64()
	routingPool.Put(h)
	return h1
}

// hashLaneBig16 produces a 16-bit sampling fingerprint from real key bytes.
// Samples 5 structural positions: first, n/4, n/2, 3n/4, last.
// Mixes tenantID so cross-tenant keys with identical bytes still differ.
// Used as H2 filter bits in tag[63:48] — independent from maphash H1.
func hashLaneBig16(tID uint16, key []byte) uint16 {
	ln := len(key)
	if ln == 0 {
		r := tID
		if r == 0 {
			r = 1
		}
		return r
	}
	mid := ln >> 1
	q1 := ln >> 2
	q3 := (ln * 3) >> 2
	h := uint32(key[0]) | uint32(key[ln-1])<<8
	h ^= uint32(key[mid]&0x0F) << 4
	h ^= uint32(key[q1]&0x07) << 9
	h ^= uint32(key[q3]&0x07) << 13
	h ^= uint32(tID)
	r := uint16(h ^ (h >> 16))
	if r == 0 {
		r = 1
	}
	return r
}
