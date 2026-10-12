// Package me — профиль текущего пользователя и колокольчик уведомлений (ТЗ §7.7, §7.8, §9.2).
package me

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/notification"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/telegram"
	"github.com/jack5505/taskflow/api/internal/user"
)

func fieldError(field, reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: field, Reason: reason}})
}

// MyProject — проект пользователя с его ролью.
type MyProject struct {
	ID         uuid.UUID `json:"id"`
	Key        string    `json:"key"`
	Name       string    `json:"name"`
	Role       string    `json:"role" enums:"manager,member"`
	IsArchived bool      `json:"is_archived"`
}

// Profile — ответ GET /me: пользователь, личные уведомления §8.7 с умолчаниями, проекты и роли,
// запущенный таймер (§6.4; нет — null).
type Profile struct {
	*user.User
	NotificationSettings map[string]bool `json:"notification_settings"`
	Projects             []MyProject     `json:"projects"`
	Timer                *task.TimerView `json:"timer" extensions:"x-nullable"`
	// TelegramBotEnabled — бот подключён (задан TELEGRAM_BOT_TOKEN): можно получить код привязки.
	TelegramBotEnabled bool `json:"telegram_bot_enabled"`
}

// Service — профиль и уведомления текущего пользователя.
type Service struct {
	db          *gorm.DB
	now         func() time.Time
	telegramBot bool
}

func NewService(db *gorm.DB) *Service { return &Service{db: db, now: time.Now} }

// WithTelegramBot — бот включён: в профиле можно получить код привязки (§8.1).
func (s *Service) WithTelegramBot(enabled bool) *Service {
	s.telegramBot = enabled
	return s
}

// Profile — данные для GET /me.
func (s *Service) Profile(ctx context.Context, u *user.User) (*Profile, error) {
	mine, err := project.ProjectsOf(s.db.WithContext(ctx), u.ID)
	if err != nil {
		return nil, err
	}
	projects := make([]MyProject, len(mine))
	for i, p := range mine {
		projects[i] = MyProject(p)
	}
	timer, err := task.CurrentTimer(ctx, s.db, u.ID, s.now())
	if err != nil {
		return nil, err
	}
	return &Profile{User: u, NotificationSettings: user.EffectiveNotifications(u.NotificationSettings), Projects: projects, Timer: timer,
		TelegramBotEnabled: s.telegramBot}, nil
}

// Update — PATCH /me: имя, часовой пояс, личные уведомления §8.7 (частичный объект).
type Update struct {
	DisplayName          *string         `json:"display_name" binding:"omitempty,max=200"`
	Timezone             *string         `json:"timezone" binding:"omitempty,max=64" example:"Europe/Moscow"`
	NotificationSettings map[string]bool `json:"notification_settings"`
}

func (s *Service) Update(ctx context.Context, u *user.User, in Update) (*user.User, error) {
	updates := map[string]any{}
	if in.DisplayName != nil {
		name, ok := user.NormalizeDisplayName(*in.DisplayName)
		if !ok {
			return nil, fieldError("display_name", "от 1 до 80 символов")
		}
		updates["display_name"] = name
	}
	if in.Timezone != nil {
		if !model.ValidTimezone(*in.Timezone) {
			return nil, fieldError("timezone", "неизвестный часовой пояс IANA")
		}
		updates["timezone"] = *in.Timezone
	}
	var fresh *user.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if fresh, err = user.LockForUpdate(tx, u.ID); err != nil {
			return err
		}
		if fresh == nil {
			return auth.ErrNoToken
		}
		if in.NotificationSettings != nil {
			merged, err := user.MergeNotifications(fresh.NotificationSettings, in.NotificationSettings)
			if err != nil {
				return fieldError("notification_settings", err.Error())
			}
			updates["notification_settings"] = merged
		}
		if len(updates) == 0 {
			return nil
		}
		return user.Update(tx, fresh, updates)
	})
	if err != nil {
		return nil, err
	}
	return fresh, nil
}

// Notifications — колокольчик (§7.7): limit 0 — по умолчанию, иначе 1..200.
func (s *Service) Notifications(ctx context.Context, u *user.User, unreadOnly bool, limit int, cursor string) (*notification.Page, error) {
	page, err := notification.List(ctx, s.db, u.ID, unreadOnly, limit, cursor, s.now())
	switch {
	case errors.Is(err, notification.ErrBadLimit):
		return nil, fieldError("limit", "от 1 до 200")
	case errors.Is(err, notification.ErrBadCursor):
		return nil, fieldError("cursor", "некорректный курсор")
	}
	return page, err
}

// MarkRead — отметить прочитанными ids (до 200) или все (ids пуст); чужие id не трогаются.
func (s *Service) MarkRead(ctx context.Context, u *user.User, ids []uuid.UUID) (int64, error) {
	if len(ids) > notification.MaxLimit {
		return 0, fieldError("ids", "не больше 200")
	}
	return notification.MarkRead(ctx, s.db, u.ID, ids, s.now())
}

// Handlers — /api/v1/me.
type Handlers struct {
	svc         *Service
	accounts    *telegram.Accounts
	requireAuth gin.HandlerFunc
}

func NewHandlers(svc *Service, accounts *telegram.Accounts, requireAuth gin.HandlerFunc) *Handlers {
	return &Handlers{svc: svc, accounts: accounts, requireAuth: requireAuth}
}

func (h *Handlers) Register(api *gin.RouterGroup) {
	g := api.Group("/me", h.requireAuth)
	g.GET("", h.get)
	g.PATCH("", h.patch)
	g.GET("/notifications", h.notifications)
	g.POST("/notifications/read", h.read)
	// Без бота код привязать некуда: маршрута нет (404). Отвязка работает всегда.
	if h.svc.telegramBot {
		g.POST("/telegram-link-code", h.linkCode)
	}
	g.DELETE("/telegram", h.unlink)
}

// linkCode — код привязки Telegram.
//
// @Summary      Код привязки Telegram
// @Description  Одноразовый код из 8 символов (латиница и цифры) на 10 минут и ссылка https://t.me/<бот>?start=<код> (§8.1). Новый код гасит прежний неиспользованный. Маршрут есть, только если бот подключён (telegram_bot_enabled в GET /me), иначе 404.
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      201  {object}  telegram.LinkCode
// @Failure      401  {object}  apierr.Response
// @Failure      404  {object}  apierr.Response  "бот не подключён"
// @Router       /api/v1/me/telegram-link-code [post]
func (h *Handlers) linkCode(c *gin.Context) {
	code, err := h.accounts.IssueLinkCode(c.Request.Context(), auth.CurrentUser(c).ID)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusCreated, code)
}

// unlink — отвязать Telegram.
//
// @Summary      Отвязать Telegram
// @Description  Снимает привязку Telegram (§8.1); после этого бот отвечает пользователю только на /start и /help. Без привязки — тоже 204.
// @Tags         me
// @Security     BearerAuth
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Router       /api/v1/me/telegram [delete]
func (h *Handlers) unlink(c *gin.Context) {
	if err := h.accounts.Unlink(c.Request.Context(), auth.CurrentUser(c).ID); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// get — текущий пользователь.
//
// @Summary      Профиль
// @Description  Текущий пользователь, его личные уведомления §8.7 (все ключи, с умолчаниями), проекты и роли, запущенный таймер (пока всегда null — появится вместе с таймером).
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  Profile
// @Failure      401  {object}  apierr.Response
// @Router       /api/v1/me [get]
func (h *Handlers) get(c *gin.Context) {
	p, err := h.svc.Profile(c.Request.Context(), auth.CurrentUser(c))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

// patch — изменение профиля.
//
// @Summary      Изменить профиль
// @Description  Меняются только переданные поля. notification_settings — частичный объект личных уведомлений §8.7: assigned, rework, accepted_cancelled, comment, review_requested, due, timer_auto_stopped, worklog_trimmed, task_deleted, membership.
// @Tags         me
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      Update   true  "поля профиля"
// @Success      200   {object}  Profile
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Router       /api/v1/me [patch]
func (h *Handlers) patch(c *gin.Context) {
	var req Update
	if !httpserver.Bind(c, &req) {
		return
	}
	u, err := h.svc.Update(c.Request.Context(), auth.CurrentUser(c), req)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	p, err := h.svc.Profile(c.Request.Context(), u)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, p)
}

// notifications — колокольчик.
//
// @Summary      Уведомления
// @Description  Уведомления колокольчика за 30 дней, новые первыми. unread_count — только на первой странице (без cursor). Следующая страница — cursor из next_cursor.
// @Tags         me
// @Produce      json
// @Security     BearerAuth
// @Param        unread  query     bool    false  "только непрочитанные"
// @Param        limit   query     int     false  "до 200, по умолчанию 50"
// @Param        cursor  query     string  false  "курсор следующей страницы"
// @Success      200     {object}  notification.Page
// @Failure      400     {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401     {object}  apierr.Response
// @Router       /api/v1/me/notifications [get]
func (h *Handlers) notifications(c *gin.Context) {
	unread, limit := false, 0
	var err error
	if v := c.Query("unread"); v != "" {
		if unread, err = strconv.ParseBool(v); err != nil {
			httpserver.Abort(c, fieldError("unread", "ожидается true или false"))
			return
		}
	}
	if v := c.Query("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit == 0 {
			httpserver.Abort(c, fieldError("limit", "от 1 до 200"))
			return
		}
	}
	page, err := h.svc.Notifications(c.Request.Context(), auth.CurrentUser(c), unread, limit, c.Query("cursor"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

type readRequest struct {
	IDs []uuid.UUID `json:"ids"`
}

type readResponse struct {
	Marked int64 `json:"marked"`
}

// read — отметить прочитанными.
//
// @Summary      Отметить уведомления прочитанными
// @Description  ids — какие отметить (до 200); без ids или пустой список — все. Чужие id игнорируются.
// @Tags         me
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      readRequest   false  "id уведомлений"
// @Success      200   {object}  readResponse
// @Failure      400   {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401   {object}  apierr.Response
// @Router       /api/v1/me/notifications/read [post]
func (h *Handlers) read(c *gin.Context) {
	var req readRequest
	// Пустое тело (в том числе chunked, где длина неизвестна) — «отметить все».
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		httpserver.Abort(c, apierr.FromBinding(err))
		return
	}
	n, err := h.svc.MarkRead(c.Request.Context(), auth.CurrentUser(c), req.IDs)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, readResponse{Marked: n})
}
