package state

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestStoreSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := &Store{LastUID: 42, Deliveries: make(map[string]Delivery)}
	store.MarkSuccess("key-1", "message-1", 42, "user@example.com")
	store.MarkFailed("key-2", "message-2", 43, "user@example.com", errors.New("smtp failed"))
	if err := store.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.LastUID != 42 {
		t.Fatalf("LastUID = %d, want 42", loaded.LastUID)
	}
	if !loaded.IsSent("key-1") {
		t.Fatal("key-1 should be marked sent")
	}
	if loaded.IsSent("key-2") {
		t.Fatal("failed delivery should not be marked sent")
	}
	if loaded.Deliveries["key-2"].RetryCount != 1 {
		t.Fatalf("RetryCount = %d, want 1", loaded.Deliveries["key-2"].RetryCount)
	}
}

func TestLoadMissingFileReturnsEmptyStore(t *testing.T) {
	store, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if store.LastUID != 0 || len(store.Deliveries) != 0 {
		t.Fatalf("store = %#v", store)
	}
}
