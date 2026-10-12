package telegram

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/user"
)

// Виды одноразовых кодов (§3.8).
const (
	CodeUserLink  = "user_link"  // привязка аккаунта (§8.1)
	CodeGroupBind = "group_bind" // подключение группы к проекту (§8.2)
)

// Срок жизни кодов (§3.8).
const (
	LinkCodeTTL = 10 * time.Minute
	BindCodeTTL = 15 * time.Minute
)

// CodeLength — длина кода (§8.1, §8.2).
const CodeLength = 8

// codeAlphabet — заглавная латиница и цифры без похожих 0/O и 1/I: код набирают руками.
// Ввод сравнивается без учёта регистра.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// OneTimeCode — строка таблицы one_time_codes (§3.8).
type OneTimeCode struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	Kind      string
	Code      string
	UserID    *uuid.UUID `gorm:"type:uuid"`
	ProjectID *uuid.UUID `gorm:"type:uuid"`
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (OneTimeCode) TableName() string { return "one_time_codes" }

// newCode — случайный код из CodeLength символов codeAlphabet (32 символа — ровно 5 бит на байт).
func newCode() string {
	b := make([]byte, CodeLength)
	_, _ = rand.Read(b) // crypto/rand.Read не возвращает ошибку (Go 1.24+)
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

// NormalizeCode — код из ввода пользователя: без пробелов по краям, в верхнем регистре.
func NormalizeCode(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// Ошибки привязки (§8.1); тексты ответов — в texts.go.
var (
	ErrCodeInvalid   = errors.New("telegram: код не найден или устарел")
	ErrLinkedToOther = errors.New("telegram: этот Telegram привязан к другому аккаунту")
)

// LinkCode — выданный код привязки: сам код, срок и ссылка на бота (§8.1).
type LinkCode struct {
	Code      string    `json:"code" example:"K7M2X9QD"`
	ExpiresAt time.Time `json:"expires_at"`
	// URL — https://t.me/<бот>?start=<код>; пусто, если имя бота не задано (TELEGRAM_BOT_USERNAME).
	URL string `json:"url" example:"https://t.me/taskflow_bot?start=K7M2X9QD"`
}

// Accounts — привязка аккаунтов Telegram (§8.1). Один сервис для профиля в вебе
// (POST /me/telegram-link-code, DELETE /me/telegram) и для команд бота /start, /unlink.
type Accounts struct {
	db          *gorm.DB
	botUsername string
	now         func() time.Time
}

func NewAccounts(db *gorm.DB, botUsername string) *Accounts {
	return &Accounts{db: db, botUsername: strings.TrimPrefix(botUsername, "@"), now: time.Now}
}

// IssueLinkCode выдаёт пользователю код привязки на 10 минут. Прежние неиспользованные коды
// пользователя гасятся: действует только последний показанный.
func (a *Accounts) IssueLinkCode(ctx context.Context, userID uuid.UUID) (*LinkCode, error) {
	now := a.now()
	c := OneTimeCode{ID: uuid.New(), Kind: CodeUserLink, UserID: &userID, ExpiresAt: now.Add(LinkCodeTTL)}
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Блокировка пользователя: два одновременных запроса иначе оба удалят «прежние» коды
		// до вставки и оставят два действующих.
		if err := user.LockRow(tx, userID); err != nil {
			return err
		}
		if err := dropOpenCodes(tx, CodeUserLink, "user_id", userID); err != nil {
			return err
		}
		// Совпадение с чужим кодом (1 из 2^40) — просто берём другой.
		for range 3 {
			c.Code = newCode()
			inserted, err := insertCode(tx, &c)
			if err != nil || inserted {
				return err
			}
		}
		return errors.New("telegram: не удалось выдать уникальный код")
	})
	if err != nil {
		return nil, err
	}
	out := &LinkCode{Code: c.Code, ExpiresAt: c.ExpiresAt.UTC()}
	if a.botUsername != "" {
		out.URL = "https://t.me/" + a.botUsername + "?start=" + c.Code
	}
	return out, nil
}

// Link привязывает Telegram-аккаунт tgID по коду (§8.1, шаг 3): записывает telegram_user_id и
// telegram_username, гасит код. Код неверный, истёк, использован или его владелец
// деактивирован — ErrCodeInvalid. Этот Telegram уже у другого пользователя — ErrLinkedToOther,
// код при этом не гасится: после /unlink им можно воспользоваться.
func (a *Accounts) Link(ctx context.Context, code string, tgID int64, username string) (*user.User, error) {
	var u *user.User
	err := a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		c, err := lockCode(tx, NormalizeCode(code), CodeUserLink)
		if err != nil {
			return err
		}
		if c == nil || c.UsedAt != nil || !a.now().Before(c.ExpiresAt) || c.UserID == nil {
			return ErrCodeInvalid
		}
		if u, err = user.LockActive(tx, *c.UserID); err != nil {
			return err
		}
		if u == nil {
			return ErrCodeInvalid
		}
		taken, err := user.TelegramTaken(tx, tgID, u.ID)
		if err != nil {
			return err
		}
		if taken {
			return ErrLinkedToOther
		}
		err = user.SetTelegram(tx, u, tgID, nullable(username))
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrLinkedToOther // тот же Telegram одновременно привязали к другому аккаунту
		}
		if err != nil {
			return fmt.Errorf("telegram: link: %w", err)
		}
		return useCode(tx, c.ID, a.now())
	})
	if err != nil {
		return nil, err
	}
	return u, nil
}

// Unlink снимает привязку Telegram у пользователя (§8.1, шаг 6). Без привязки — ничего не делает.
func (a *Accounts) Unlink(ctx context.Context, userID uuid.UUID) error {
	return user.ClearTelegram(a.db.WithContext(ctx), userID)
}

// ByTelegram — активный пользователь, привязанный к tgID; nil — не привязан. username
// (без @) обновляется, если изменился: §3.1 — при каждом сообщении боту.
func (a *Accounts) ByTelegram(ctx context.Context, tgID int64, username string) (*user.User, error) {
	u, err := user.ActiveByTelegram(a.db.WithContext(ctx), tgID)
	if err != nil || u == nil {
		return nil, err
	}
	if cur := deref(u.TelegramUsername); cur != username {
		if err := user.SetTelegramUsername(a.db.WithContext(ctx), u, nullable(username)); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// CleanupCodes удаляет коды, истёкшие больше суток назад (воркер, раз в сутки).
func (a *Accounts) CleanupCodes(ctx context.Context) (int64, error) {
	return deleteCodesExpiredBefore(a.db.WithContext(ctx), a.now().Add(-24*time.Hour))
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
