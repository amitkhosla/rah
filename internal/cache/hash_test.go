package cache

import (
	"encoding/binary"
	"testing"
)

func TestKeyFingerprint64UsesExpectedPositions(t *testing.T) {
	key := []byte("abcdefghijklmnop") // n=16
	got := keyFingerprint64(key)

	// positions: first, n/2, n/4, 3n/4, last, n/2+1, n/4+1, 3n/4+1
	// => [0,8,4,12,15,9,5,13] => a i e m p j f n
	want := binary.LittleEndian.Uint64([]byte{'a', 'i', 'e', 'm', 'p', 'j', 'f', 'n'})
	if got != want {
		t.Fatalf("fingerprint mismatch: got=%d want=%d", got, want)
	}
}

func TestKeyFingerprint64HandlesShortKeysWithClamping(t *testing.T) {
	key := []byte("ab")
	got := keyFingerprint64(key)
	want := binary.LittleEndian.Uint64([]byte{'a', 'b', 'a', 'b', 'b', 'b', 'b', 'b'})
	if got != want {
		t.Fatalf("fingerprint mismatch: got=%d want=%d", got, want)
	}
}
