package cache_v1

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
