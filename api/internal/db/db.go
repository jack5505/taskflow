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
	"github.com/jackc/pgx/v5/pgconn"
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

// LatestVersion возвращает номер самой свежей миграции в src — версию схемы, которую ждёт бинарник.
func LatestVersion(src fs.FS) (uint, error) {
	d, err := iofs.New(src, ".")
	if err != nil {
		return 0, fmt.Errorf("migrations source: %w", err)
	}
	defer func() { _ = d.Close() }()

	v, err := d.First()
	if err != nil {
		return 0, fmt.Errorf("migrations source: %w", err)
	}
	for {
		next, err := d.Next(v)
		if errors.Is(err, fs.ErrNotExist) {
			return v, nil
		}
		if err != nil {
			return 0, fmt.Errorf("migrations source: %w", err)
		}
		v = next
	}
}

// WaitForMigrations ждёт, пока версия схемы в базе станет не ниже последней миграции из src
// и не будет «грязной» (issue #137). Миграции применяет api при старте (ТЗ §10.2); воркер,
// запущенный раньше, иначе ходил бы в таблицы, которых ещё нет. Схема новее бинарника — норма:
// миграции вперёд-совместимые (CONTRIBUTING §8). Ошибки чтения версии не прерывают ожидание —
// база может перезапускаться; предел задаёт ctx.
func WaitForMigrations(ctx context.Context, g *gorm.DB, src fs.FS, poll time.Duration) error {
	want, err := LatestVersion(src)
	if err != nil {
		return err
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	var (
		cur     uint
		dirty   bool
		read    bool // версия хотя бы раз прочитана
		lastErr error
		logged  bool
	)
	for ctx.Err() == nil {
		v, d, err := schemaVersion(ctx, g)
		switch {
		case err != nil && ctx.Err() != nil:
			// Запрос прерван концом ожидания — в ошибке остаётся последнее прочитанное состояние.
			continue
		case err != nil:
			lastErr = err
		default:
			cur, dirty, read, lastErr = v, d, true, nil
			if cur >= want && !dirty {
				if logged {
					slog.Info("db: migrations ready", "version", cur)
				}
				return nil
			}
		}
		if !logged {
			slog.Info("db: waiting for migrations", "version", cur, "want", want, "dirty", dirty, "err", lastErr)
			logged = true
		}
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
	switch {
	case lastErr != nil:
		return fmt.Errorf("wait for migrations %d: %w; last error: %w", want, ctx.Err(), lastErr)
	case !read:
		return fmt.Errorf("wait for migrations %d: %w; schema version not read", want, ctx.Err())
	default:
		return fmt.Errorf("wait for migrations %d: %w; schema version %d (dirty=%t)", want, ctx.Err(), cur, dirty)
	}
}

// schemaVersion — текущая версия схемы и флаг dirty; пока таблицы версий нет — 0 без ошибки.
// Читает schema_migrations напрямую, а не через golang-migrate: тот при открытии берёт
// advisory-блокировку без учёта ctx и ждал бы, пока api закончит миграции.
func schemaVersion(ctx context.Context, g *gorm.DB) (uint, bool, error) {
	sqlDB, err := g.DB()
	if err != nil {
		return 0, false, err
	}
	var (
		v     int64
		dirty bool
	)
	err = sqlDB.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&v, &dirty)
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.As(err, &pgErr) && pgErr.Code == "42P01":
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("schema version: %w", err)
	}
	return uint(v), dirty, nil
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
