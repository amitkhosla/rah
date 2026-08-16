package control

import (
	"context"
	"fmt"
	"log"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileCryptoStep handles crypto operation step types.
//
// Supported actions:
//   - crypto_aes_encrypt: AES-256-GCM with bake-time key
//   - crypto_aes_decrypt: AES-256-GCM with bake-time key
//   - crypto_aes_encrypt_var: AES-256-GCM with runtime key from slot
//   - crypto_aes_decrypt_var: AES-256-GCM with runtime key from slot
//   - crypto_hmac_sha256: HMAC-SHA256 with bake-time key
//   - crypto_hmac_sha1: HMAC-SHA1 with bake-time key (legacy)
//   - crypto_sha256: SHA-256 no-key hash
//   - crypto_md5: MD5 no-key hash (legacy)
//
// Common fields:
//   - Source: input slot name (plaintext, ciphertext, or data to hash)
//   - As: output slot name
//   - Key: secret ref for bake-time key (e.g., "env:AES_KEY", "gsm://...")
//   - Input["key_var"]: slot name containing runtime key bytes (for *_var variants)
func (c *Compiler) compileCryptoStep(step StepConfig) error {
	switch step.Action {
	case "crypto_aes_encrypt":
		return c.compileAESEncrypt(step)
	case "crypto_aes_decrypt":
		return c.compileAESDecrypt(step)
	case "crypto_aes_encrypt_var":
		return c.compileAESEncryptVar(step)
	case "crypto_aes_decrypt_var":
		return c.compileAESDecryptVar(step)
	case "crypto_hmac_sha256":
		return c.compileHMACSha256(step)
	case "crypto_hmac_sha1":
		return c.compileHMACSha1(step)
	case "crypto_sha256":
		return c.compileSHA256(step)
	case "crypto_md5":
		return c.compileMD5(step)
	default:
		return fmt.Errorf("unknown crypto action: %q", step.Action)
	}
}

// compileAESEncrypt handles crypto_aes_encrypt: AES-256-GCM with bake-time key.
func (c *Compiler) compileAESEncrypt(step StepConfig) error {
	if c.SecretsMgr == nil {
		return fmt.Errorf("crypto_aes_encrypt requires SecretsMgr — configure secrets_manager in gateway config")
	}

	if step.Key == "" {
		return fmt.Errorf("crypto_aes_encrypt: 'Key' is required")
	}

	if step.Source == "" {
		return fmt.Errorf("crypto_aes_encrypt: 'Source' is required")
	}

	if step.As == "" {
		return fmt.Errorf("crypto_aes_encrypt: 'As' is required")
	}

	// Resolve key at bake time
	keyBytes, err := c.SecretsMgr.Resolve(context.Background(), step.Key)
	if err != nil {
		return fmt.Errorf("crypto_aes_encrypt: resolve key %q: %w", step.Key, err)
	}

	// Resolve source and result slots
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("crypto_aes_encrypt Source: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("crypto_aes_encrypt As: %w", err)
	}

	// Build the AES encrypt instruction
	instr, err := steps.AESEncryptStep(srcSlot, resultSlot, append([]byte(nil), keyBytes...))
	if err != nil {
		return fmt.Errorf("crypto_aes_encrypt: build instruction: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileAESDecrypt handles crypto_aes_decrypt: AES-256-GCM with bake-time key.
func (c *Compiler) compileAESDecrypt(step StepConfig) error {
	if c.SecretsMgr == nil {
		return fmt.Errorf("crypto_aes_decrypt requires SecretsMgr — configure secrets_manager in gateway config")
	}

	if step.Key == "" {
		return fmt.Errorf("crypto_aes_decrypt: 'Key' is required")
	}

	if step.Source == "" {
		return fmt.Errorf("crypto_aes_decrypt: 'Source' is required")
	}

	if step.As == "" {
		return fmt.Errorf("crypto_aes_decrypt: 'As' is required")
	}

	// Resolve key at bake time
	keyBytes, err := c.SecretsMgr.Resolve(context.Background(), step.Key)
	if err != nil {
		return fmt.Errorf("crypto_aes_decrypt: resolve key %q: %w", step.Key, err)
	}

	// Resolve source and result slots
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("crypto_aes_decrypt Source: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("crypto_aes_decrypt As: %w", err)
	}

	// Build the AES decrypt instruction
	instr, err := steps.AESDecryptStep(srcSlot, resultSlot, append([]byte(nil), keyBytes...))
	if err != nil {
		return fmt.Errorf("crypto_aes_decrypt: build instruction: %w", err)
	}

	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileAESEncryptVar handles crypto_aes_encrypt_var: AES-256-GCM with runtime key from slot.
func (c *Compiler) compileAESEncryptVar(step StepConfig) error {
	if step.Source == "" {
		return fmt.Errorf("crypto_aes_encrypt_var: 'Source' is required")
	}

	if step.As == "" {
		return fmt.Errorf("crypto_aes_encrypt_var: 'As' is required")
	}

	keyVar := step.Input["key_var"]
	if keyVar == "" {
		return fmt.Errorf("crypto_aes_encrypt_var: 'key_var' in Input is required")
	}

	// Resolve source, key, and result slots
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("crypto_aes_encrypt_var Source: %w", err)
	}

	keySlot, err := c.getSlot(keyVar)
	if err != nil {
		return fmt.Errorf("crypto_aes_encrypt_var key_var: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("crypto_aes_encrypt_var As: %w", err)
	}

	// Build the AES encrypt (slot key) instruction
	instr := steps.AESEncryptSlotKeyStep(srcSlot, keySlot, resultSlot)
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileAESDecryptVar handles crypto_aes_decrypt_var: AES-256-GCM with runtime key from slot.
func (c *Compiler) compileAESDecryptVar(step StepConfig) error {
	if step.Source == "" {
		return fmt.Errorf("crypto_aes_decrypt_var: 'Source' is required")
	}

	if step.As == "" {
		return fmt.Errorf("crypto_aes_decrypt_var: 'As' is required")
	}

	keyVar := step.Input["key_var"]
	if keyVar == "" {
		return fmt.Errorf("crypto_aes_decrypt_var: 'key_var' in Input is required")
	}

	// Resolve source, key, and result slots
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("crypto_aes_decrypt_var Source: %w", err)
	}

	keySlot, err := c.getSlot(keyVar)
	if err != nil {
		return fmt.Errorf("crypto_aes_decrypt_var key_var: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("crypto_aes_decrypt_var As: %w", err)
	}

	// Build the AES decrypt (slot key) instruction
	instr := steps.AESDecryptSlotKeyStep(srcSlot, keySlot, resultSlot)
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileHMACSha256 handles crypto_hmac_sha256: HMAC-SHA256 with bake-time key.
func (c *Compiler) compileHMACSha256(step StepConfig) error {
	if c.SecretsMgr == nil {
		return fmt.Errorf("crypto_hmac_sha256 requires SecretsMgr — configure secrets_manager in gateway config")
	}

	if step.Key == "" {
		return fmt.Errorf("crypto_hmac_sha256: 'Key' is required")
	}

	if step.Source == "" {
		return fmt.Errorf("crypto_hmac_sha256: 'Source' is required")
	}

	if step.As == "" {
		return fmt.Errorf("crypto_hmac_sha256: 'As' is required")
	}

	// Resolve key at bake time
	keyBytes, err := c.SecretsMgr.Resolve(context.Background(), step.Key)
	if err != nil {
		return fmt.Errorf("crypto_hmac_sha256: resolve key %q: %w", step.Key, err)
	}

	// Resolve source and result slots
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("crypto_hmac_sha256 Source: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("crypto_hmac_sha256 As: %w", err)
	}

	// Build the HMAC-SHA256 instruction
	instr := steps.HMACSha256Step(srcSlot, resultSlot, append([]byte(nil), keyBytes...))
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileHMACSha1 handles crypto_hmac_sha1: HMAC-SHA1 with bake-time key (legacy).
func (c *Compiler) compileHMACSha1(step StepConfig) error {
	if c.SecretsMgr == nil {
		return fmt.Errorf("crypto_hmac_sha1 requires SecretsMgr — configure secrets_manager in gateway config")
	}

	if step.Key == "" {
		return fmt.Errorf("crypto_hmac_sha1: 'Key' is required")
	}

	if step.Source == "" {
		return fmt.Errorf("crypto_hmac_sha1: 'Source' is required")
	}

	if step.As == "" {
		return fmt.Errorf("crypto_hmac_sha1: 'As' is required")
	}

	// Log warning for SHA1 usage
	log.Println("WARNING: SHA1 is cryptographically weak; use hmac_sha256 unless required for legacy compatibility")

	// Resolve key at bake time
	keyBytes, err := c.SecretsMgr.Resolve(context.Background(), step.Key)
	if err != nil {
		return fmt.Errorf("crypto_hmac_sha1: resolve key %q: %w", step.Key, err)
	}

	// Resolve source and result slots
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("crypto_hmac_sha1 Source: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("crypto_hmac_sha1 As: %w", err)
	}

	// Build the HMAC-SHA1 instruction
	instr := steps.HMACSha1Step(srcSlot, resultSlot, append([]byte(nil), keyBytes...))
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileSHA256 handles crypto_sha256: SHA-256 hash (no key).
func (c *Compiler) compileSHA256(step StepConfig) error {
	if step.Source == "" {
		return fmt.Errorf("crypto_sha256: 'Source' is required")
	}

	if step.As == "" {
		return fmt.Errorf("crypto_sha256: 'As' is required")
	}

	// Resolve source and result slots
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("crypto_sha256 Source: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("crypto_sha256 As: %w", err)
	}

	// Build the SHA256 instruction
	instr := steps.SHA256HashStep(srcSlot, resultSlot)
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}

// compileMD5 handles crypto_md5: MD5 hash (no key, legacy).
func (c *Compiler) compileMD5(step StepConfig) error {
	if step.Source == "" {
		return fmt.Errorf("crypto_md5: 'Source' is required")
	}

	if step.As == "" {
		return fmt.Errorf("crypto_md5: 'As' is required")
	}

	// Log warning for MD5 usage
	log.Println("WARNING: MD5 is cryptographically weak; use sha256 unless required for legacy checksums")

	// Resolve source and result slots
	srcSlot, err := c.getSlot(step.Source)
	if err != nil {
		return fmt.Errorf("crypto_md5 Source: %w", err)
	}

	resultSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("crypto_md5 As: %w", err)
	}

	// Build the MD5 instruction
	instr := steps.MD5HashStep(srcSlot, resultSlot)
	c.GlobalTable = append(c.GlobalTable, instr)
	return nil
}
