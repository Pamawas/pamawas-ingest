package testutils

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

//go:embed ../../migrations/*.sql
var embeddedMigrations embed.FS

type Migration struct {
	Version string
	SQL     string
}

type MigrationRunner struct {
	db         *sql.DB
	migrations []Migration
}

func NewMigrationRunner(db *sql.DB) (*MigrationRunner, error) {
	mr := &MigrationRunner{db: db}
	migrations, err := mr.loadEmbeddedMigrations()
	if err != nil {
		return nil, fmt.Errorf("failed to load migrations: %w", err)
	}
	mr.migrations = migrations
	return mr, nil
}

func (mr *MigrationRunner) loadEmbeddedMigrations() ([]Migration, error) {
	entries, err := embeddedMigrations.ReadDir("migrations")
	if err != nil {
		return nil, err
	}

	var migrations []Migration
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		content, err := embeddedMigrations.ReadFile(filepath.Join("migrations", entry.Name()))
		if err != nil {
			return nil, err
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
	defer func() {
		if err := rows.Close(); err != nil {
			// ignore
		}
	}()

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
			if rbErr := tx.Rollback(); rbErr != nil {
				// ignore
			}
			return fmt.Errorf("failed to execute migration %s: %w", migration.Version, err)
		}

		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", migration.Version); err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				// ignore
			}
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
func SetupTestDB(t interface{ Fatal(args ...interface{}) }, dbURL string) *sql.DB {
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Run migrations
	runner, err := NewMigrationRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
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