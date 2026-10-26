package prepareagents

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := OpenStore(path, "panel.local")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestFreshStoreHasIDColumn(t *testing.T) {
	store := openTestStore(t)

	version, err := store.GetMetadata(metaKeySchemaVersion)
	if err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version != "1" {
		t.Errorf("schema_version = %q, want %q", version, "1")
	}

	var idCol int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name = 'id'`).Scan(&idCol); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	if idCol != 1 {
		t.Errorf("agents.id column missing (count=%d), want 1", idCol)
	}
}

func TestUpsertPersistsNumericIDAndSelectActionableReturnsIt(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if err := store.UpsertDiscoveredAgent(ctx, "key-42", 42, "host42", "10.0.0.42", "linux", true); err != nil {
		t.Fatalf("UpsertDiscoveredAgent: %v", err)
	}

	// Re-discovery updates the id on the existing row.
	if err := store.UpsertDiscoveredAgent(ctx, "key-42", 99, "host42", "10.0.0.42", "linux", true); err != nil {
		t.Fatalf("UpsertDiscoveredAgent (update): %v", err)
	}

	actionable, err := store.SelectActionable(ctx, time.Now())
	if err != nil {
		t.Fatalf("SelectActionable: %v", err)
	}
	if len(actionable) != 1 {
		t.Fatalf("got %d actionable, want 1", len(actionable))
	}
	if actionable[0].ID != 99 {
		t.Errorf("actionable id = %d, want 99", actionable[0].ID)
	}
}
