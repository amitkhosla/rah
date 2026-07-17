package control

import (
	"fmt"

	"github.com/amitkhosla/rah/internal/engine/steps"
)

// compileLoadLLMKey resolves and appends the load_llm_key instruction.
//
// The instruction fetches a per-tenant LLM API key from the CredentialRegistry
// at request time and writes the plaintext into a ByteSlot. A subsequent
// llm_call step can then reference that slot via api_key_slot for per-tenant
// model access without embedding keys at bake time.
//
// step.KeyIdentifier â†’ slot holding the credential name to look up (e.g. "anthropic_key")
// step.As            â†’ slot to write the resolved plaintext API key into
//
// Requires CredMgr to be set on the Compiler (same interface used by load_credential).
func (c *Compiler) compileLoadLLMKey(step StepConfig) error {
	if c.CredMgr == nil {
		return fmt.Errorf("load_llm_key step requires a credential registry â€” set CredMgr on the Compiler")
	}

	credNameSlot, err := c.getSlot(step.KeyIdentifier)
	if err != nil {
		return fmt.Errorf("load_llm_key: credential name slot (key_identifier): %w", err)
	}
	outSlot, err := c.getSlot(step.As)
	if err != nil {
		return fmt.Errorf("load_llm_key: output slot (as): %w", err)
	}

	cfg := steps.LoadLLMKeyConfig{
		CredNameSlot: credNameSlot,
		OutSlot:      outSlot,
		Reg:          c.CredMgr,
	}
	c.GlobalTable = append(c.GlobalTable, steps.LoadLLMKey(cfg))
	return nil
}
