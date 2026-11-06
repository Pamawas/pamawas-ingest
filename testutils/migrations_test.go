package testutils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindMigrationsDir(t *testing.T) {
	dir := FindMigrationsDir()
	if dir == "" {
		t.Fatal("expected non-empty migrations dir")
	}

	// Verify that the discovered directory exists and has .sql files
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read discovered migrations dir %q: %v", dir, err)
	}

	hasSQL := false
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".sql" {
			hasSQL = true
			break
		}
	}

	if !hasSQL {
		t.Fatalf("discovered directory %q does not contain any .sql migration files", dir)
	}
}
