package engine

import (
	"context"
	"fmt"
	"rah/internal/datastore"
	"rah/internal/rctx"
)

// SlotOverflowAdapter wraps a datastore.KeyValueStore to implement
// rctx.SlotOverflowStore. All keys are stored under a fixed tenant and domain
// so they stay isolated from application data.
//
// Typical usage — disk backend for bare-metal, GCS for Cloud Run:
//
//	store, _ := datastoreMgr.GetStore(config.DomainSlotOverflow)
//	fm.SlotOverflowStore = engine.NewSlotOverflowAdapter(store)
type SlotOverflowAdapter struct {
	store  datastore.KeyValueStore
	tenant datastore.Tenant
	domain string
}

// NewSlotOverflowAdapter creates a SlotOverflowAdapter.
// tenant is typically "system" or the gateway instance name.
// domain is the logical namespace, e.g. "slot-overflow".
func NewSlotOverflowAdapter(store datastore.KeyValueStore, tenant, domain string) rctx.SlotOverflowStore {
	return &SlotOverflowAdapter{
		store:  store,
		tenant: datastore.Tenant(tenant),
		domain: domain,
	}
}

func (a *SlotOverflowAdapter) SlotPut(key string, value []byte) error {
	return a.store.Put(context.Background(), a.tenant, a.scopedKey(key), value)
}

func (a *SlotOverflowAdapter) SlotGet(key string) ([]byte, error) {
	val, ok, err := a.store.Get(context.Background(), a.tenant, a.scopedKey(key))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("slot overflow key not found: %s", key)
	}
	return val, nil
}

func (a *SlotOverflowAdapter) SlotDelete(key string) error {
	return a.store.Delete(context.Background(), a.tenant, a.scopedKey(key))
}

func (a *SlotOverflowAdapter) scopedKey(key string) string {
	return a.domain + ":" + key
}
