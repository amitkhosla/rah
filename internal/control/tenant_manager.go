package control

//In compiler side, we might not require this stuff added, as this is data and not api definition.
import (
	"rah/internal/registry"
)

// TenantUpdate represents the incoming JSON command
type TenantUpdate struct {
	TenantID uint32 `json:"tenant_id"`
	Delete   struct {
		Aliases   []string `json:"aliases"`
		Endpoints []string `json:"endpoints"`
		Metadata  []string `json:"metadata"`
	} `json:"delete"`
	Upsert struct {
		Aliases   []string          `json:"aliases"`
		Endpoints map[string]string `json:"endpoints"`
		Metadata  map[string]string `json:"metadata"`
	} `json:"upsert"`
}

type TenantController struct {
	Reg *registry.RegistryManager
}

func (c *TenantController) ApplyUpdate(upd TenantUpdate) {
	// We don't need a local 'exists' check because resolveOrCreateTenant
	// handles the slot allocation internally.

	// 1. Handle Upserts (Prioritize aliases to establish IDs)
	for _, alias := range upd.Upsert.Aliases {
		// For each endpoint/metadata, we call AddTenantData.
		// This handles resolving the Alias to a Row and the Key to a Column.
		for key, value := range upd.Upsert.Endpoints {
			c.Reg.AddTenantData(alias, key, []byte(value))
		}
		for key, value := range upd.Upsert.Metadata {
			c.Reg.AddTenantData(alias, key, []byte(value))
		}
	}

	// 2. Handle Deletions
	// Note: In a Matrix, you can't easily "delete" a single cell without zeroing it.
	for _, alias := range upd.Delete.Aliases {
		c.Reg.DeleteTenant(alias) // This zeros the whole row and recycles the tID
	}
}
