// Package logx — логгер с request_id в контексте запроса (ТЗ §9.3, §10.3).
// HTTP-слой кладёт логгер в context, сервисы и репозитории берут его через From.
package logx

import (
	"context"
	"log/slog"
)

type ctxKey struct{}

// With возвращает контекст с привязанным логгером.
func With(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From достаёт логгер из контекста; без него — slog.Default().
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
