// Package db — подключение к PostgreSQL через GORM и миграции golang-migrate (ТЗ §10.2).
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const defaultConnectTimeout = 10 * time.Second

// Open открывает пул соединений GORM и проверяет связь с базой в пределах ctx.
// Автопинг GORM отключён: иначе первое подключение шло бы без таймаута и не прерывалось по SIGTERM.
func Open(ctx context.Context, databaseURL string) (*gorm.DB, error) {
	sqlDB, err := openPool(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	g, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		Logger:               logger.Discard,
		TranslateError:       true,
		DisableAutomaticPing: true,
	})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("gorm open: %w", err)
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
// Подключение ограничено ctx; на время миграций открывается отдельное соединение.
func Migrate(ctx context.Context, databaseURL string, src fs.FS) error {
	m, err := newMigrate(ctx, databaseURL, src)
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
func MigrateDown(ctx context.Context, databaseURL string, src fs.FS) error {
	m, err := newMigrate(ctx, databaseURL, src)
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
func Version(ctx context.Context, databaseURL string, src fs.FS) (version uint, ok bool, err error) {
	m, err := newMigrate(ctx, databaseURL, src)
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

// openPool открывает *sql.DB через pgx с таймаутом подключения и проверяет его в пределах ctx.
func openPool(ctx context.Context, databaseURL string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: %w", err)
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	sqlDB := stdlib.OpenDB(*cfg)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	return sqlDB, nil
}

// newMigrate открывает отдельное соединение: драйвер golang-migrate закрывает его в Close,
// поэтому пул GORM ему отдавать нельзя.
func newMigrate(ctx context.Context, databaseURL string, src fs.FS) (*migrate.Migrate, error) {
	sqlDB, err := openPool(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	driver, err := pgxmigrate.WithInstance(sqlDB, &pgxmigrate.Config{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("migrate driver: %w", err)
	}
	d, err := iofs.New(src, ".")
	if err != nil {
		_ = driver.Close()
		return nil, fmt.Errorf("migrations source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", d, "pgx5", driver)
	if err != nil {
		_ = driver.Close()
		return nil, fmt.Errorf("migrate init: %w", err)
	}
	return m, nil
}

// closeMigrate закрывает источник и соединение миграций.
func closeMigrate(m *migrate.Migrate) {
	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		slog.Warn("db: migrate close", "src", srcErr, "db", dbErr)
	}
}
