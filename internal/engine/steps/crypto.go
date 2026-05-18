package steps

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"sync"

	"rah/internal/engine"
	"rah/internal/rctx"
)

// Package-level pools for key-agnostic hash functions.
var sha256Pool = sync.Pool{New: func() any { return sha256.New() }}
var md5Pool = sync.Pool{New: func() any { return md5.New() }}

// HMACSha256Step computes HMAC-SHA256 of ByteSlots[src] using a bake-time key.
// Output is written as a hex string into ByteSlots[result].
// Each unique key gets its own per-closure pool (avoids key confusion between flows).
func HMACSha256Step(src, result int, staticKey []byte) engine.Instruction {
	keyCopy := append([]byte(nil), staticKey...)
	pool := &sync.Pool{New: func() any { return hmac.New(sha256.New, keyCopy) }}
	return engine.Instruction{
		Name: "HMAC_SHA256",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			h := pool.Get().(hash.Hash)
			h.Reset()
			h.Write(ctx.ByteSlots[src])
			// Append digest into arena — 32 bytes always fits in inline arena.
			raw := ctx.Alloc(sha256.Size)
			raw = h.Sum(raw[:0])
			pool.Put(h)
			// Hex-encode into arena: 64 bytes.
			out := ctx.Alloc(hex.EncodedLen(len(raw)))
			hex.Encode(out, raw)
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// HMACSha1Step computes HMAC-SHA1 of ByteSlots[src] using a bake-time key.
// Output is written as a hex string into ByteSlots[result].
func HMACSha1Step(src, result int, staticKey []byte) engine.Instruction {
	keyCopy := append([]byte(nil), staticKey...)
	pool := &sync.Pool{New: func() any { return hmac.New(sha1.New, keyCopy) }}
	return engine.Instruction{
		Name: "HMAC_SHA1",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			h := pool.Get().(hash.Hash)
			h.Reset()
			h.Write(ctx.ByteSlots[src])
			raw := ctx.Alloc(sha1.Size)
			raw = h.Sum(raw[:0])
			pool.Put(h)
			out := ctx.Alloc(hex.EncodedLen(len(raw)))
			hex.Encode(out, raw)
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// SHA256HashStep computes SHA-256 of ByteSlots[src] and writes hex to ByteSlots[result].
func SHA256HashStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "SHA256_HASH",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			h := sha256Pool.Get().(hash.Hash)
			h.Reset()
			h.Write(ctx.ByteSlots[src])
			raw := ctx.Alloc(sha256.Size)
			raw = h.Sum(raw[:0])
			sha256Pool.Put(h)
			out := ctx.Alloc(hex.EncodedLen(len(raw)))
			hex.Encode(out, raw)
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// MD5HashStep computes MD5 of ByteSlots[src] and writes hex to ByteSlots[result].
// Note: MD5 is cryptographically weak; use only for legacy compatibility/checksums.
func MD5HashStep(src, result int) engine.Instruction {
	return engine.Instruction{
		Name: "MD5_HASH",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			h := md5Pool.Get().(hash.Hash)
			h.Reset()
			h.Write(ctx.ByteSlots[src])
			raw := ctx.Alloc(md5.Size)
			raw = h.Sum(raw[:0])
			md5Pool.Put(h)
			out := ctx.Alloc(hex.EncodedLen(len(raw)))
			hex.Encode(out, raw)
			ctx.ByteSlots[result] = out
			return state.PC + 1
		},
	}
}

// AESEncryptStep encrypts ByteSlots[src] with AES-GCM and writes ciphertext (nonce||ciphertext)
// to ByteSlots[result]. The key is baked at compile time; AEAD is pre-computed.
// A fresh 12-byte nonce is prepended to the output so decryption can derive it.
func AESEncryptStep(src, result int, key []byte) (engine.Instruction, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return engine.Instruction{}, fmt.Errorf("aes_encrypt: invalid key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return engine.Instruction{}, fmt.Errorf("aes_encrypt: NewGCM: %w", err)
	}
	nonceSize := aead.NonceSize()
	overhead := aead.Overhead()
	return engine.Instruction{
		Name: "AES_ENCRYPT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			plain := ctx.ByteSlots[src]
			// Output: nonce (12 bytes) || ciphertext+tag
			totalLen := nonceSize + len(plain) + overhead
			buf := ctx.Alloc(totalLen)
			nonce := buf[:nonceSize]
			if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
				ctx.Failed = true
				return engine.StopPlan
			}
			// Seal appends ciphertext+tag into buf[nonceSize:nonceSize]
			ciphertext := aead.Seal(buf[nonceSize:nonceSize], nonce, plain, nil)
			ctx.ByteSlots[result] = buf[:nonceSize+len(ciphertext)]
			return state.PC + 1
		},
	}, nil
}

// AESDecryptStep decrypts ByteSlots[src] (nonce||ciphertext) with AES-GCM.
// Clears result slot and sets Failed on authentication failure.
func AESDecryptStep(src, result int, key []byte) (engine.Instruction, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return engine.Instruction{}, fmt.Errorf("aes_decrypt: invalid key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return engine.Instruction{}, fmt.Errorf("aes_decrypt: NewGCM: %w", err)
	}
	nonceSize := aead.NonceSize()
	return engine.Instruction{
		Name: "AES_DECRYPT",
		Action: func(ctx *rctx.Context, state *engine.ExecutionState) int16 {
			in := ctx.ByteSlots[src]
			if len(in) < nonceSize {
				ctx.ByteSlots[result] = nil
				ctx.Failed = true
				ctx.ErrorCode = 400
				return engine.StopPlan
			}
			nonce := in[:nonceSize]
			ciphertext := in[nonceSize:]
			// Open writes plaintext into a new allocation; no in-place decryption possible with GCM.
			plain, err := aead.Open(ctx.Alloc(len(ciphertext))[:0], nonce, ciphertext, nil)
			if err != nil {
				ctx.ByteSlots[result] = nil
				ctx.Failed = true
				ctx.ErrorCode = 400
				return engine.StopPlan
			}
			ctx.ByteSlots[result] = plain
			return state.PC + 1
		},
	}, nil
}
