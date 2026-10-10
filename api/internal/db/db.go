// Package db — подключение к PostgreSQL через GORM и миграции golang-migrate (ТЗ §10.2).
package db

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // драйвер pgx5 для golang-migrate
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open открывает пул соединений GORM и проверяет связь с базой.
func Open(ctx context.Context, databaseURL string) (*gorm.DB, error) {
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL не задан")
	}
	g, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		Logger:         logger.Discard,
		TranslateError: true,
	})
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	sqlDB, err := g.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return g, nil
}

// Ping проверяет базу для /health (ТЗ §10.3).
func Ping(ctx context.Context, g *gorm.DB) error {
	sqlDB, err := g.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// Close закрывает пул соединений.
func Close(g *gorm.DB) error {
	sqlDB, err := g.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Migrate применяет все миграции из src (каталог с *.sql) вверх до последней.
func Migrate(databaseURL string, src fs.FS) error {
	m, err := newMigrate(databaseURL, src)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	v, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return err
	}
	slog.Info("db: migrations applied", "version", v, "dirty", dirty)
	return nil
}

// MigrateDown откатывает все миграции — нужен тестам и для проверки down-скриптов.
func MigrateDown(databaseURL string, src fs.FS) error {
	m, err := newMigrate(databaseURL, src)
	if err != nil {
		return err
	}
	defer closeMigrate(m)

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate down: %w", err)
	}
	return nil
}

// Version возвращает текущую версию схемы; ok=false, если миграции ещё не применялись.
func Version(databaseURL string, src fs.FS) (version uint, ok bool, err error) {
	m, err := newMigrate(databaseURL, src)
	if err != nil {
		return 0, false, err
	}
	defer closeMigrate(m)

	v, _, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

func newMigrate(databaseURL string, src fs.FS) (*migrate.Migrate, error) {
	d, err := iofs.New(src, ".")
	if err != nil {
		return nil, fmt.Errorf("migrations source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL(databaseURL))
	if err != nil {
		return nil, fmt.Errorf("migrate init: %w", err)
	}
	return m, nil
}

func closeMigrate(m *migrate.Migrate) {
	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		slog.Warn("db: migrate close", "src", srcErr, "db", dbErr)
	}
}

// pgxURL переводит postgres:// в схему драйвера pgx5 golang-migrate.
func pgxURL(u string) string {
	const prefix = "postgres://"
	if len(u) > len(prefix) && u[:len(prefix)] == prefix {
		return "pgx5://" + u[len(prefix):]
	}
	const prefix2 = "postgresql://"
	if len(u) > len(prefix2) && u[:len(prefix2)] == prefix2 {
		return "pgx5://" + u[len(prefix2):]
	}
	return u
}
