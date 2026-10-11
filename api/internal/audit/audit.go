// Package audit — журнал действий менеджеров и администраторов (ТЗ §3.8). Запись делается в той
// же транзакции, что и само изменение; кто и откуда действует, берётся из контекста запроса.
package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Источники действия (колонка source).
const (
	SourceWeb    = "web"
	SourceBot    = "bot"
	SourceCLI    = "cli"
	SourceSystem = "system"
)

// Действия журнала. Новое действие из списка §3.8 добавляется сюда.
const (
	ProjectCreated    = "project.created"
	ProjectUpdated    = "project.updated"
	ProjectArchived   = "project.archived"
	ProjectUnarchived = "project.unarchived"
	ProjectDeleted    = "project.deleted"
	ProjectRestored   = "project.restored"

	MemberAdded       = "member.added"
	MemberRoleChanged = "member.role_changed"
	MemberRemoved     = "member.removed"

	UserCreated     = "user.created"
	UserUpdated     = "user.updated"
	UserDeactivated = "user.deactivated"
	UserActivated   = "user.activated"
)

// Entry — строка таблицы audit_logs.
type Entry struct {
	ID         uuid.UUID    `gorm:"type:uuid;primaryKey" json:"id"`
	ActorID    *uuid.UUID   `gorm:"type:uuid" json:"actor_id"`
	ProjectID  *uuid.UUID   `gorm:"type:uuid" json:"project_id"`
	Action     string       `json:"action"`
	EntityType string       `json:"entity_type"`
	EntityID   string       `json:"entity_id"`
	OldValue   *events.JSON `gorm:"type:jsonb" json:"old_value" swaggertype:"object"`
	NewValue   *events.JSON `gorm:"type:jsonb" json:"new_value" swaggertype:"object"`
	Source     string       `json:"source" enums:"web,bot,cli,system"`
	CreatedAt  time.Time    `json:"created_at"`
}

func (Entry) TableName() string { return "audit_logs" }

type ctxKey int

const (
	actorKey ctxKey = iota
	sourceKey
)

// WithActor — кто действует (RequireAuth кладёт текущего пользователя).
func WithActor(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, actorKey, userID)
}

// WithSource — откуда действие. Без него: web, если в контексте есть актор (запрос API),
// иначе system (воркер, плановые задания).
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey, source)
}

// From — кто и откуда действует по контексту запроса: актор (nil — система) и источник.
func From(ctx context.Context) (*uuid.UUID, string) {
	if ctx == nil {
		return nil, SourceSystem
	}
	var actor *uuid.UUID
	source := SourceSystem
	if id, ok := ctx.Value(actorKey).(uuid.UUID); ok {
		actor, source = &id, SourceWeb
	}
	if s, ok := ctx.Value(sourceKey).(string); ok {
		source = s
	}
	return actor, source
}

// Record — что записать. ProjectID nil — действие вне проекта.
type Record struct {
	ProjectID  *uuid.UUID
	Action     string
	EntityType string
	EntityID   string
	Old, New   any
}

// Write пишет запись в транзакции tx; контекст берётся из tx (db.WithContext(ctx).Transaction).
func Write(tx *gorm.DB, r Record) error {
	actor, source := From(tx.Statement.Context)
	e := Entry{
		ID: uuid.New(), ActorID: actor, ProjectID: r.ProjectID, Action: r.Action,
		EntityType: r.EntityType, EntityID: r.EntityID, Source: source,
	}
	var err error
	if e.OldValue, err = toJSON(r.Old); err != nil {
		return err
	}
	if e.NewValue, err = toJSON(r.New); err != nil {
		return err
	}
	if err := tx.Omit("CreatedAt").Create(&e).Error; err != nil {
		return fmt.Errorf("audit: %s: %w", r.Action, err)
	}
	return nil
}

func toJSON(v any) (*events.JSON, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("audit: value: %w", err)
	}
	j := events.JSON(b)
	return &j, nil
}

// Diff — прежние и новые значения полей из updates, которые реально меняются (сравнение ==
// годится для строк, чисел и bool; прочее сравнивает вызывающий сам).
func Diff(current, updates map[string]any) (old, nw map[string]any) {
	old, nw = map[string]any{}, map[string]any{}
	for k, cur := range current {
		if v, ok := updates[k]; ok && v != cur {
			old[k], nw[k] = cur, v
		}
	}
	return old, nw
}

// Filter — фильтры журнала (§9.2): период [From, To), пользователь, тип действия.
type Filter struct {
	From, To *time.Time
	ActorID  *uuid.UUID
	Action   string
	Limit    int
	Cursor   string
}

// MaxLimit — §9.2: страница не больше 200 записей.
const MaxLimit = 200

var ErrBadCursor = errors.New("audit: некорректный курсор")

// Page — страница журнала, новые записи первыми; NextCursor пуст на последней странице.
type Page struct {
	Items      []Entry `json:"items"`
	NextCursor string  `json:"next_cursor"`
}

// List — журнал проекта с курсорной пагинацией по (created_at, id).
func List(ctx context.Context, db *gorm.DB, projectID uuid.UUID, f Filter) (*Page, error) {
	limit := f.Limit
	if limit <= 0 || limit > MaxLimit {
		limit = 50
	}
	where := []string{"project_id = ?"}
	args := []any{projectID}
	if f.From != nil {
		where, args = append(where, "created_at >= ?"), append(args, *f.From)
	}
	if f.To != nil {
		where, args = append(where, "created_at < ?"), append(args, *f.To)
	}
	if f.ActorID != nil {
		where, args = append(where, "actor_id = ?"), append(args, *f.ActorID)
	}
	if f.Action != "" {
		where, args = append(where, "action = ?"), append(args, f.Action)
	}
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, err
		}
		where, args = append(where, "(created_at, id) < (?, ?)"), append(args, at, id)
	}
	items := []Entry{}
	err := db.WithContext(ctx).Raw(`SELECT * FROM audit_logs WHERE `+strings.Join(where, " AND ")+
		` ORDER BY created_at DESC, id DESC LIMIT ?`, append(args, limit+1)...).Scan(&items).Error
	if err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	page := &Page{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	ts, idStr, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrBadCursor
	}
	return at, id, nil
}
