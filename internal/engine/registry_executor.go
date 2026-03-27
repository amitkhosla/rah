package engine

import (
	"rah/internal/rctx"
	"rah/internal/registry"
)

// RegistryExecutor implements OpFlusher for registry PUT operations.
// It routes each op to the correct PropStore in RegistryManager based on
// the op's Target field. The RegistryManager handles both in-memory updates
// and async persistence to the backing datastore transparently.
//
// Only OpPut operations are processed; OpGet is not supported for registry
// ops (registry reads use the lock-free hot-path methods directly).
//
// TenantID → alias resolution: the RegistryManager's Add* methods identify
// tenants by alias string (management-plane key), not by numeric TenantID.
// RegistryExecutor resolves TenantID → primary alias via GetTenantRecord
// before dispatching. If no record is found the op is silently dropped
// (the tenant does not exist in the management plane).
type RegistryExecutor struct {
	mgr *registry.RegistryManager
}

// NewRegistryExecutor creates a RegistryExecutor backed by the given manager.
func NewRegistryExecutor(mgr *registry.RegistryManager) *RegistryExecutor {
	return &RegistryExecutor{mgr: mgr}
}

// Submit processes all registry PUT ops in the batch synchronously
// (in-memory writes are immediate) then closes Done if set.
func (re *RegistryExecutor) Submit(batch rctx.Batch) {
	for _, op := range batch.Ops {
		if op.Type != rctx.OpPut {
			continue
		}

		// Resolve TenantID → primary alias. The Add* methods on RegistryManager
		// are alias-addressed; GetTenantRecord gives us the alias list.
		rec := re.mgr.GetTenantRecord(op.TenantID)
		if rec == nil || len(rec.Aliases) == 0 {
			continue // tenant unknown — drop the op
		}
		alias := rec.Aliases[0]
		key := string(op.Key)

		switch op.Target {
		case rctx.TargetRegistryURL:
			re.mgr.AddServiceURL(alias, key, op.Value)
		case rctx.TargetRegistryID:
			re.mgr.AddIdentifier(alias, key, op.Value)
		case rctx.TargetRegistryMeta:
			re.mgr.AddMeta(alias, key, op.Value)
		}
	}

	if batch.Done != nil {
		close(batch.Done)
	}
}
