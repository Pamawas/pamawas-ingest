package testutils

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

type Migration struct {
	Version string
	SQL     string
}

type MigrationRunner struct {
	db         *sql.DB
	migrations []Migration
}

func NewMigrationRunner(db *sql.DB, migrationsDir string) (*MigrationRunner, error) {
	mr := &MigrationRunner{db: db}
	migrations, err := mr.loadMigrationsFromDir(migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load migrations: %w", err)
	}
	mr.migrations = migrations
	return mr, nil
}

func (mr *MigrationRunner) loadMigrationsFromDir(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory %s: %w", dir, err)
	}

	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		// #nosec G304 - path is constructed from controlled directory and filename
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read migration %s: %w", path, err)
		}
		version := strings.TrimSuffix(entry.Name(), ".sql")
		migrations = append(migrations, Migration{Version: version, SQL: string(content)})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations, nil
}

func (mr *MigrationRunner) ensureMigrationsTable(ctx context.Context) error {
	query := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		)
	`
	_, err := mr.db.ExecContext(ctx, query)
	return err
}

func (mr *MigrationRunner) getAppliedMigrations(ctx context.Context) (map[string]bool, error) {
	rows, err := mr.db.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return make(map[string]bool), nil
	}
	//errcheck:ignore
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

func (mr *MigrationRunner) Run(ctx context.Context) error {
	// Ensure migrations table exists
	if err := mr.ensureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("failed to ensure migrations table: %w", err)
	}

	// Get already applied migrations
	applied, err := mr.getAppliedMigrations(ctx)
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Run pending migrations
	for _, migration := range mr.migrations {
		if applied[migration.Version] {
			continue
		}

		start := time.Now()

		tx, err := mr.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin transaction for %s: %w", migration.Version, err)
		}

		if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
			//errcheck:ignore
			tx.Rollback()
			return fmt.Errorf("failed to execute migration %s: %w", migration.Version, err)
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", migration.Version); err != nil {
			//errcheck:ignore
			tx.Rollback()
			return fmt.Errorf("failed to record migration %s: %w", migration.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %s: %w", migration.Version, err)
		}

		_ = time.Since(start)
	}

	return nil
}

// SetupTestDB creates a test database and runs migrations
func SetupTestDB(t interface{ Fatal(args ...interface{}) }, dbURL string, migrationsDir string) *sql.DB {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Run migrations
	runner, err := NewMigrationRunner(db, migrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	if runErr := runner.Run(ctx); runErr != nil {
		t.Fatal(runErr)
	}

	// Clean up tables after migrations
	_, err = db.ExecContext(ctx, `
		TRUNCATE TABLE events, idempotency_records RESTART IDENTITY CASCADE;
	`)
	if err != nil {
		t.Fatal(err)
	}

	return db
}