// Package me — профиль текущего пользователя и колокольчик уведомлений (ТЗ §7.7, §7.8, §9.2).
package me

import (
	"context"
	"errors"
	"fmt"
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
// запущенный таймер (появится вместе с таймером §6.4; пока всегда null).
type Profile struct {
	*user.User
	NotificationSettings map[string]bool `json:"notification_settings"`
	Projects             []MyProject     `json:"projects"`
	Timer                any             `json:"timer" swaggertype:"object"`
}

// Service — профиль и уведомления текущего пользователя.
type Service struct {
	db  *gorm.DB
	now func() time.Time
}

func NewService(db *gorm.DB) *Service { return &Service{db: db, now: time.Now} }

// Profile — данные для GET /me.
func (s *Service) Profile(ctx context.Context, u *user.User) (*Profile, error) {
	projects := []MyProject{}
	err := s.db.WithContext(ctx).Raw(`SELECT p.id, p.key, p.name, m.role, p.is_archived
		FROM project_members m JOIN projects p ON p.id = m.project_id
		WHERE m.user_id = ? AND p.deleted_at IS NULL ORDER BY p.key`, u.ID).Scan(&projects).Error
	if err != nil {
		return nil, fmt.Errorf("me: projects: %w", err)
	}
	return &Profile{User: u, NotificationSettings: user.EffectiveNotifications(u.NotificationSettings), Projects: projects}, nil
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
	var fresh user.User
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Raw(`SELECT * FROM users WHERE id = ? FOR UPDATE`, u.ID).Scan(&fresh)
		if res.Error != nil {
			return fmt.Errorf("me: lock: %w", res.Error)
		}
		if res.RowsAffected == 0 {
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
		if err := tx.Model(&fresh).Updates(updates).Error; err != nil {
			return fmt.Errorf("me: update: %w", err)
		}
		return tx.First(&fresh, "id = ?", u.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &fresh, nil
}

// Handlers — /api/v1/me.
type Handlers struct {
	svc         *Service
	requireAuth gin.HandlerFunc
}

func NewHandlers(svc *Service, requireAuth gin.HandlerFunc) *Handlers {
	return &Handlers{svc: svc, requireAuth: requireAuth}
}

func (h *Handlers) Register(api *gin.RouterGroup) {
	g := api.Group("/me", h.requireAuth)
	g.GET("", h.get)
	g.PATCH("", h.patch)
	g.GET("/notifications", h.notifications)
	g.POST("/notifications/read", h.read)
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
// @Description  Уведомления колокольчика за 30 дней, новые первыми, и число непрочитанных. Следующая страница — cursor из next_cursor.
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
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > notification.MaxLimit {
			httpserver.Abort(c, fieldError("limit", "от 1 до 200"))
			return
		}
	}
	page, err := notification.List(c.Request.Context(), h.svc.db, auth.CurrentUser(c).ID, unread, limit, c.Query("cursor"))
	if errors.Is(err, notification.ErrBadCursor) {
		err = fieldError("cursor", "некорректный курсор")
	}
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

type readRequest struct {
	IDs []uuid.UUID `json:"ids" binding:"max=200"`
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
	if c.Request.ContentLength != 0 && !httpserver.Bind(c, &req) {
		return
	}
	n, err := notification.MarkRead(c.Request.Context(), h.svc.db, auth.CurrentUser(c).ID, req.IDs, h.svc.now())
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, readResponse{Marked: n})
}
