package postgres_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/avi-p/yesbook/internal/adapter/postgres"
	"github.com/avi-p/yesbook/migrations"
)

func TestMigrateUpIsIdempotent(t *testing.T) {
	url := testDatabaseURL(t)
	if err := postgres.Migrate(url); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(url); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateDirtyState(t *testing.T) {
	url := testDatabaseURL(t)
	if err := postgres.Migrate(url); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE schema_migrations SET dirty = true`); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := pool.Exec(context.Background(), `UPDATE schema_migrations SET dirty = false`)
		pool.Close()
		if err != nil {
			t.Errorf("clear dirty flag: %v", err)
			return
		}
		if err := postgres.Migrate(url); err != nil {
			t.Errorf("restore schema: %v", err)
		}
	})
	err = postgres.Migrate(url)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "dirty") {
		t.Fatalf("dirty migrate = %v", err)
	}
}

func TestMigrateDownAndUp(t *testing.T) {
	url := testDatabaseURL(t)
	if err := postgres.Migrate(url); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := postgres.Migrate(url); err != nil {
			t.Errorf("restore schema: %v", err)
		}
	})
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, strings.Replace(url, "postgres://", "pgx5://", 1))
	if err != nil {
		t.Fatal(err)
	}
	err = m.Down()
	srcErr, dbErr := m.Close()
	if err != nil || srcErr != nil || dbErr != nil {
		t.Fatal(errors.Join(err, srcErr, dbErr))
	}
	if err := postgres.Migrate(url); err != nil {
		t.Fatal(err)
	}
}

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("YESBOOK_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("YESBOOK_TEST_DATABASE_URL is not set")
	}
	return url
}
