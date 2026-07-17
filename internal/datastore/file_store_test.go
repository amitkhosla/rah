package datastore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/amitkhosla/rah/internal/config"
)

func TestFileStorePersistsData(t *testing.T) {
	dir := t.TempDir()
	cfg := config.StoreConfig{
		Name:    "disk",
		Kind:    config.StoreDisk,
		Enabled: true,
		Connection: config.StoreConnection{
			Path: dir,
		},
	}

	s1, err := newFileStore(cfg, "flows")
	if err != nil {
		t.Fatalf("newFileStore() err = %v", err)
	}
	if err := s1.Put(context.Background(), Tenant("__global__"), "flow:hello", []byte(`[{"action":"noop"}]`)); err != nil {
		t.Fatalf("put err = %v", err)
	}
	_ = s1.Close()

	s2, err := newFileStore(cfg, "flows")
	if err != nil {
		t.Fatalf("newFileStore(2) err = %v", err)
	}
	got, ok, err := s2.Get(context.Background(), Tenant("__global__"), "flow:hello")
	if err != nil || !ok {
		t.Fatalf("get err=%v ok=%v", err, ok)
	}
	if string(got) != `[{"action":"noop"}]` {
		t.Fatalf("unexpected value: %s", string(got))
	}

	keys, err := s2.ListKeys(context.Background(), Tenant("__global__"), "flow:")
	if err != nil {
		t.Fatalf("list keys err=%v", err)
	}
	if len(keys) != 1 || keys[0] != "hello" {
		t.Fatalf("unexpected keys: %#v (file=%s)", keys, filepath.Join(dir, "store_flows.json"))
	}
}
