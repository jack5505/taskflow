// Package web встраивает собранный фронтенд (web/dist → api/internal/web/dist) в бинарник.
// В репозитории лежит только заглушка index.html; `make web-build` кладёт сюда настоящую сборку.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
