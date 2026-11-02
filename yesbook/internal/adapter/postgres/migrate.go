package postgres

import (
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/avi-p/yesbook/migrations"
)

// Migrate applies every pending migration. A database already at the latest
// version is not an error.
func Migrate(url string) (err error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, migrateURL(url))
	if err != nil {
		return fmt.Errorf("open migrator: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		err = errors.Join(err, srcErr, dbErr)
	}()
	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		err = fmt.Errorf("apply migrations: %w", upErr)
		return err
	}
	return nil
}

// migrateURL switches a postgres:// URL to the scheme of the pgx v5 driver.
func migrateURL(url string) string {
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if rest, ok := strings.CutPrefix(url, prefix); ok {
			return "pgx5://" + rest
		}
	}
	return url
}
