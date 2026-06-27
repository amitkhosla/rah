package avro

import (
	"fmt"
	"sort"
)

// registry.go — schema registry keyed by 64-bit Rabin fingerprint (Avro standard).
// Write-once at startup; read-only at runtime. No locks in hot path.

// SchemaRegistry stores compiled AvroPrograms by schema fingerprint.
// Write-once at startup (Register); read-only at runtime (Lookup).
// Lookup is lock-free: binary search on a sorted immutable slice.
type SchemaRegistry struct {
	entries []registryEntry // sorted by Fingerprint ascending
}

type registryEntry struct {
	Fingerprint uint64
	Prog        *AvroProgram
}

// Register adds a compiled program keyed by the fingerprint of schemaJSON.
// Must be called only at bake time (not thread-safe). Duplicate fingerprints
// are silently replaced.
func (r *SchemaRegistry) Register(schemaJSON string, prog *AvroProgram) error {
	if prog == nil {
		return fmt.Errorf("avro: Register called with nil AvroProgram")
	}
	fp := Fingerprint(schemaJSON)
	// Check for duplicate
	lo, hi := 0, len(r.entries)
	for lo < hi {
		mid := (lo + hi) / 2
		if r.entries[mid].Fingerprint < fp {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(r.entries) && r.entries[lo].Fingerprint == fp {
		r.entries[lo].Prog = prog
		return nil
	}
	// Insert at lo to maintain sorted order
	r.entries = append(r.entries, registryEntry{})
	copy(r.entries[lo+1:], r.entries[lo:])
	r.entries[lo] = registryEntry{Fingerprint: fp, Prog: prog}
	return nil
}

// Lookup returns the AvroProgram for the given fingerprint, nil if not found.
// Lock-free: binary search on sorted slice (immutable after startup).
func (r *SchemaRegistry) Lookup(fingerprint uint64) *AvroProgram {
	lo, hi := 0, len(r.entries)
	for lo < hi {
		mid := (lo + hi) / 2
		e := r.entries[mid].Fingerprint
		if e < fingerprint {
			lo = mid + 1
		} else if e > fingerprint {
			hi = mid
		} else {
			return r.entries[mid].Prog
		}
	}
	return nil
}

// Len returns the number of registered schemas.
func (r *SchemaRegistry) Len() int { return len(r.entries) }

// Sort sorts entries by fingerprint. Call after bulk-loading entries to
// ensure binary search correctness. Register() already maintains sort order
// incrementally, so Sort() is only needed if entries are loaded out of order.
func (r *SchemaRegistry) Sort() {
	sort.Slice(r.entries, func(i, j int) bool {
		return r.entries[i].Fingerprint < r.entries[j].Fingerprint
	})
}

// ---------------------------------------------------------------------------
// Avro Rabin fingerprint (CRC-64-AVRO)
// ---------------------------------------------------------------------------
// Algorithm from the Avro specification:
//   https://avro.apache.org/docs/current/specification/#schema-fingerprints
//
// Uses polynomial 0xC96C5795D7870F42 (bit-reversed form of the Avro poly).

const avroFPEmpty = uint64(0xc15d213aa4d7a795)

// avroFPTable is the 256-entry lookup table for the CRC-64-AVRO algorithm.
// Initialised once in init().
var avroFPTable [256]uint64

func init() {
	const poly = uint64(0xC96C5795D7870F42)
	for i := 0; i < 256; i++ {
		fp := uint64(i)
		for b := 0; b < 8; b++ {
			mask := -(fp & 1)
			fp = (fp >> 1) ^ (poly & mask)
		}
		avroFPTable[i] = fp
	}
}

// Fingerprint computes the Avro Rabin fingerprint for a canonical schema string.
// The result is deterministic: same input always produces the same uint64.
func Fingerprint(schemaJSON string) uint64 {
	fp := avroFPEmpty
	for i := 0; i < len(schemaJSON); i++ {
		b := schemaJSON[i]
		fp = (fp >> 8) ^ avroFPTable[(byte(fp)^b)&0xff]
	}
	return fp
}

// fingerprintBytes is the []byte variant of Fingerprint, used internally.
func fingerprintBytes(data []byte) uint64 {
	fp := avroFPEmpty
	for _, b := range data {
		fp = (fp >> 8) ^ avroFPTable[(byte(fp)^b)&0xff]
	}
	return fp
}
