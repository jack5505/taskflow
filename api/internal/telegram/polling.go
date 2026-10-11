package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/jack5505/taskflow/api/internal/config"
)

// RunPolling — long polling для локальной разработки (§8.8: в production только webhook).
// Снимает webhook (иначе getUpdates отвечает 409) и принимает обновления тем же Intake, что и
// webhook: идемпотентность и очередь одинаковые. Отличие: обновление, которое не удалось
// принять (Redis недоступен), только пишется в лог — getUpdates уже сдвинул offset, повторной
// доставки не будет. Для локальной разработки это приемлемо. Возвращается по отмене ctx.
func RunPolling(ctx context.Context, cfg *config.Config, in *Intake) error {
	b, err := NewBot(cfg, bot.WithAllowedUpdates(allowedUpdates), bot.WithDefaultHandler(func(ctx context.Context, _ *bot.Bot, upd *models.Update) {
		raw, err := json.Marshal(upd)
		if err == nil {
			_, err = in.Accept(ctx, upd.ID, raw)
		}
		if err != nil {
			slog.Error("telegram: polling update not accepted", "update_id", upd.ID, "err", err)
		}
	}))
	if err != nil {
		return err
	}
	if _, err := b.DeleteWebhook(ctx, &bot.DeleteWebhookParams{}); err != nil {
		return fmt.Errorf("telegram: deleteWebhook: %w", err)
	}
	slog.Info("telegram: long polling started")
	b.Start(ctx)
	return nil
}
