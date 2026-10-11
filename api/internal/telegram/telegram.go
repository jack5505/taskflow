// Package telegram — Telegram-бот (ТЗ §8): приём обновлений, идемпотентность update_id, разбор
// команд и кнопок, отправка сообщений из воркера с лимитами Telegram (§8.8).
//
// Поток обновления: Telegram → POST /api/v1/telegram/webhook (секрет в заголовке) → Intake
// запоминает update_id в Redis на 24 часа и ставит задание telegram:update → ответ 200 сразу.
// Воркер: Processor разбирает обновление и вызывает обработчик Router; нажатие кнопки
// подтверждается answerCallbackQuery не позже чем через 3 секунды. Ответы и прочие сообщения
// уходят заданиями telegram:send: Deliverer держит лимит 20 сообщений в минуту на группу,
// при 429 повторяет через retry_after, после 5 неудачных попыток пишет в лог и отбрасывает.
//
// Бот выключен, пока не задан TELEGRAM_BOT_TOKEN: маршрут webhook не регистрируется (404),
// воркер не берёт задания бота.
package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/jack5505/taskflow/api/internal/config"
)

// Типы заданий asynq.
const (
	TaskUpdate = "telegram:update" // обработать обновление от Telegram
	TaskSend   = "telegram:send"   // отправить, изменить или удалить сообщение
)

// WebhookPath — путь приёма обновлений относительно /api/v1 (§9.2).
const WebhookPath = "/telegram/webhook"

// allowedUpdates — какие обновления просим у Telegram: сообщения (команды), кнопки и изменение
// участия бота в группе (§8.2: удаление бота из группы).
var allowedUpdates = []string{"message", "callback_query", "my_chat_member"}

// pollTimeout — сколько getUpdates ждёт новых обновлений в режиме long polling.
const pollTimeout = 30 * time.Second

// API — методы Bot API, которыми пользуется бот. *bot.Bot их реализует; в тестах тот же
// *bot.Bot ходит в фейковый сервер Bot API.
type API interface {
	SendMessage(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error)
	EditMessageText(ctx context.Context, params *bot.EditMessageTextParams) (*models.Message, error)
	DeleteMessage(ctx context.Context, params *bot.DeleteMessageParams) (bool, error)
	AnswerCallbackQuery(ctx context.Context, params *bot.AnswerCallbackQueryParams) (bool, error)
}

// NewBot — клиент Bot API по настройкам. getMe при создании не вызывается: недоступный Telegram
// не должен мешать подняться API и воркеру. opts дополняют настройки (обработчик long polling).
func NewBot(cfg *config.Config, opts ...bot.Option) (*bot.Bot, error) {
	base := []bot.Option{
		bot.WithSkipGetMe(),
		bot.WithServerURL(strings.TrimRight(cfg.TelegramAPIURL, "/")),
		bot.WithHTTPClient(pollTimeout, &http.Client{Timeout: pollTimeout + 10*time.Second}),
		bot.WithErrorsHandler(func(err error) { slog.Error("telegram", "err", err) }),
	}
	b, err := bot.New(cfg.TelegramBotToken, append(base, opts...)...)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	return b, nil
}

// SetWebhook сообщает Telegram адрес webhook и секрет заголовка (§8.8). Вызывается при старте
// API в режиме webhook; ошибка не мешает API работать — её видно в логе.
func SetWebhook(ctx context.Context, cfg *config.Config) error {
	b, err := NewBot(cfg)
	if err != nil {
		return err
	}
	url := strings.TrimRight(cfg.BaseURL, "/") + "/api/v1" + WebhookPath
	if _, err := b.SetWebhook(ctx, &bot.SetWebhookParams{
		URL: url, SecretToken: cfg.TelegramWebhookSecret, AllowedUpdates: allowedUpdates,
	}); err != nil {
		return fmt.Errorf("telegram: setWebhook: %w", err)
	}
	slog.Info("telegram: webhook set", "url", url)
	return nil
}
