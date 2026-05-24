package apikey

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
)

// RestoreFromStore loads all Apps and API Keys from the backing store into the
// in-memory indexes. It must be called once at startup before serving requests.
// A nil store is silently ignored (memory-only mode).
func RestoreFromStore(s Store) error {
	if s == nil {
		return nil
	}

	appBlobs, err := s.ListApps(context.Background())
	if err != nil {
		return fmt.Errorf("apikey: list apps from store: %w", err)
	}
	for _, raw := range appBlobs {
		var a App
		if err := json.Unmarshal(raw, &a); err != nil {
			log.Printf("apikey: skip corrupt app record: %v", err)
			continue
		}
		UpsertApp(a)
		BumpAppIDCounterIfNeeded(a.AppID)
	}

	keyBlobs, err := s.ListKeys(context.Background())
	if err != nil {
		return fmt.Errorf("apikey: list keys from store: %w", err)
	}
	for _, raw := range keyBlobs {
		var rec APIKeyRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			log.Printf("apikey: skip corrupt key record: %v", err)
			continue
		}
		UpsertKey(rec)
		BumpKeyIDCounterIfNeeded(rec.KeyID)
	}

	log.Printf("apikey: restored %d apps, %d keys", len(appBlobs), len(keyBlobs))
	return nil
}
