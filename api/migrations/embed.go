// Package migrations содержит SQL-миграции golang-migrate, встроенные в бинарник.
// Они применяются при старте `taskflow api` (ТЗ §10.2); GORM AutoMigrate не используется.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
