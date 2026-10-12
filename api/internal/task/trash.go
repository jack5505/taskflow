package task

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/user"
)

// UserRef — пользователь в ответе: id и имя.
type UserRef struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
}

// TrashItem — удалённая задача в корзине (§7.9): поля задачи (deleted_at заполнено) и кто
// удалил; deleted_by — null, если удаление записано без пользователя.
type TrashItem struct {
	Task
	DeletedBy *UserRef `json:"deleted_by" extensions:"x-nullable"`
}

type trashRow struct {
	Task
	DeletedByID   *uuid.UUID
	DeletedByName *string
}

// TrashPage — страница корзины; next_cursor пуст на последней.
type TrashPage struct {
	Items      []TrashItem `json:"items"`
	NextCursor string      `json:"next_cursor"`
}

// Trash — корзина проекта (§2, §7.9: только администратор системы): удалённые задачи, новые
// удаления сверху, с автором последнего удаления из истории (§3.7, поле deleted_at). Курсор —
// (deleted_at, id). Администратору доступна и корзина удалённого проекта, как и журнал; не
// участнику — 404, участнику без прав администратора — 403.
func (s *Service) Trash(ctx context.Context, u *user.User, key string, limit int, cursor string) (*TrashPage, error) {
	var projectID uuid.UUID
	if u.IsSystemAdmin {
		p, err := s.projects.FindWithDeleted(ctx, key)
		if err != nil {
			return nil, err
		}
		projectID = p.ID
	} else {
		if _, err := s.projects.Access(ctx, u, key); err != nil {
			return nil, err
		}
		return nil, errDeletedAdminOnly // то же правило §2, что у include_deleted
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > MaxLimit {
		return nil, fieldError("limit", "целое число от 1 до 200")
	}
	where := []string{"t.project_id = ?", "t.deleted_at IS NOT NULL"}
	args := []any{projectID}
	if cursor != "" {
		at, id, err := decodeTrashCursor(cursor)
		if err != nil {
			return nil, err
		}
		where, args = append(where, "(t.deleted_at, t.id) < (?, ?)"), append(args, at, id)
	}
	var rows []trashRow
	// Кто удалил — последняя запись истории deleted_at со значением (не восстановление).
	db := s.db.WithContext(ctx)
	err := db.Raw(`SELECT t.*, d.user_id AS deleted_by_id
		FROM tasks t
		LEFT JOIN LATERAL (
			SELECT h.user_id FROM task_history h
			WHERE h.task_id = t.id AND h.field = 'deleted_at' AND h.new_value IS NOT NULL AND h.new_value <> 'null'::jsonb
			ORDER BY h.created_at DESC, h.id DESC LIMIT 1
		) d ON true
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY t.deleted_at DESC, t.id DESC LIMIT ?`, append(args, limit+1)...).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("task: trash: %w", err)
	}
	if err := fillNames(db, rows[:min(len(rows), limit)], func(r *trashRow) *uuid.UUID { return r.DeletedByID },
		func(r *trashRow, name string) { r.DeletedByName = &name }); err != nil {
		return nil, err
	}
	page := &TrashPage{Items: make([]TrashItem, 0, len(rows))}
	for _, r := range rows {
		it := TrashItem{Task: r.Task}
		if r.DeletedByID != nil && r.DeletedByName != nil { // как LEFT JOIN users: нет пользователя — нет и «кто удалил»
			it.DeletedBy = &UserRef{ID: *r.DeletedByID, DisplayName: *r.DeletedByName}
		}
		page.Items = append(page.Items, it)
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeTrashCursor(*last.DeletedAt, last.ID)
	}
	return page, nil
}

func encodeTrashCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeTrashCursor(c string) (time.Time, uuid.UUID, error) {
	bad := fieldError("cursor", "некорректный курсор")
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, bad
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, bad
	}
	at, err1 := time.Parse(time.RFC3339Nano, ts)
	id, err2 := uuid.Parse(idStr)
	if err1 != nil || err2 != nil {
		return time.Time{}, uuid.Nil, bad
	}
	return at, id, nil
}

// trash — корзина проекта.
//
// @Summary      Корзина проекта
// @Description  Удалённые задачи проекта (§7.9) — только администратор системы (§2), в том числе для удалённого проекта. Новые удаления первыми; deleted_by — кто удалил (последнее удаление по истории задачи), null, если неизвестно. limit до 200 (по умолчанию 50), следующая страница — cursor из next_cursor. Восстановление — POST /tasks/{key}/restore.
// @Tags         tasks
// @Produce      json
// @Security     BearerAuth
// @Param        key     path      string  true   "ключ проекта"
// @Param        limit   query     int     false  "до 200, по умолчанию 50"
// @Param        cursor  query     string  false  "курсор следующей страницы"
// @Success      200     {object}  TrashPage
// @Failure      400     {object}  apierr.Response  "VALIDATION_FAILED"
// @Failure      401     {object}  apierr.Response
// @Failure      403     {object}  apierr.Response  "FORBIDDEN: участник, но не администратор системы"
// @Failure      404     {object}  apierr.Response  "NOT_FOUND: нет проекта или пользователь в нём не участвует"
// @Router       /api/v1/projects/{key}/trash [get]
func (h *Handlers) trash(c *gin.Context) {
	limit := 0
	if v := c.Query("limit"); v != "" {
		var err error
		if limit, err = strconv.Atoi(v); err != nil {
			limit = -1 // 400 по limit — в сервисе, после проверки прав: чужой проект не раскрывается
		}
	}
	page, err := h.svc.Trash(c.Request.Context(), auth.CurrentUser(c), c.Param("key"), limit, c.Query("cursor"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}
