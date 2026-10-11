package task

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/user"
)

// MaxLimit — §9.2: страница списка задач не больше 200.
const MaxLimit = 200

// IDFilter — фильтр по исполнителю или спринту: конкретные id и/или «пусто» (null).
type IDFilter struct {
	IDs  []uuid.UUID
	Null bool
}

// ListFilter — фильтры списка задач проекта (§9.2, фильтры доски §7.3).
type ListFilter struct {
	Status         []string
	Type           []string
	Priority       []string
	Assignee       IDFilter
	Sprint         IDFilter
	Label          string
	Q              string
	IncludeDeleted bool
	Limit          int
	Cursor         string
}

// ListItem — задача в списке: поле overdue — срок раньше сегодняшнего дня по поясу проекта,
// а задача не закрыта (§3.3).
type ListItem struct {
	Task
	Overdue bool `json:"overdue"`
}

// Page — страница задач; next_cursor пуст на последней.
type Page struct {
	Items      []ListItem `json:"items"`
	NextCursor string     `json:"next_cursor"`
}

var errDeletedAdminOnly = apierr.Forbidden("Удалённые задачи видит только администратор системы")

// sectionExpr — секция задачи для порядка списка: спринт (по id), бэклог — последним.
const sectionExpr = `COALESCE(t.sprint_id::text, '~')`

// List — задачи проекта по фильтрам с курсорной пагинацией. Порядок — по секциям (спринты, затем
// бэклог), внутри секции — ручной порядок (position), при равенстве — номер. Курсор — ключ
// (секция, position, number): уже выданные задачи не повторяются и не теряются при вставке новых
// (новая задача встаёт в конец своей секции; если её секция уже пройдена — она появится при
// следующем запросе первой страницы). Перенос задачи во время листания может показать её ещё раз.
func (s *Service) List(ctx context.Context, a *project.Access, f ListFilter) (*Page, error) {
	if f.IncludeDeleted && !a.Admin {
		return nil, errDeletedAdminOnly
	}
	limit := f.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > MaxLimit {
		return nil, fieldError("limit", "от 1 до 200")
	}
	for _, check := range []struct {
		field  string
		values []string
		allow  []string
	}{
		{"status", f.Status, []string{StatusTodo, StatusInProgress, StatusReview, StatusDone, StatusCancelled}},
		{"type", f.Type, []string{TypeTask, TypeBug, TypeFeature}},
		{"priority", f.Priority, []string{PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical}},
	} {
		for _, v := range check.values {
			if !slices.Contains(check.allow, v) {
				return nil, fieldError(check.field, "допустимые: "+strings.Join(check.allow, ", "))
			}
		}
	}

	where := []string{"t.project_id = ?"}
	args := []any{a.Project.ID}
	if !f.IncludeDeleted {
		where = append(where, "t.deleted_at IS NULL")
	}
	add := func(cond string, vals ...any) { where, args = append(where, cond), append(args, vals...) }
	if len(f.Status) > 0 {
		add("t.status IN ?", f.Status)
	}
	if len(f.Type) > 0 {
		add("t.type IN ?", f.Type)
	}
	if len(f.Priority) > 0 {
		add("t.priority IN ?", f.Priority)
	}
	if c, v := idCondition("t.assignee_id", f.Assignee); c != "" {
		add(c, v...)
	}
	if c, v := idCondition("t.sprint_id", f.Sprint); c != "" {
		add(c, v...)
	}
	if f.Label != "" {
		add("t.labels @> jsonb_build_array(?::text)", f.Label)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		like := escapeLike(q)
		add(`(t.key ILIKE ? OR t.title ILIKE ?)`, like+"%", "%"+like+"%")
	}
	if f.Cursor != "" {
		section, pos, num, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, err
		}
		add("("+sectionExpr+", t.position, t.number) > (?, ?, ?)", section, pos, num)
	}
	var items []ListItem
	err := s.db.WithContext(ctx).Raw(`SELECT t.*,
			(t.due_date IS NOT NULL AND t.status NOT IN ('done', 'cancelled')
			 AND t.due_date < (?::timestamptz AT TIME ZONE p.timezone)::date) AS overdue
		FROM tasks t JOIN projects p ON p.id = t.project_id
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY `+sectionExpr+`, t.position, t.number LIMIT ?`,
		append(append([]any{s.now()}, args...), limit+1)...).Scan(&items).Error
	if err != nil {
		return nil, fmt.Errorf("task: list: %w", err)
	}
	page := &Page{Items: items}
	if page.Items == nil {
		page.Items = []ListItem{}
	}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(last.SprintID, last.Position, last.Number)
	}
	return page, nil
}

// MyTask — открытая задача пользователя в блоке «Мои задачи» (§7.2).
type MyTask struct {
	Key             string     `json:"key"`
	Title           string     `json:"title"`
	ProjectKey      string     `json:"project_key"`
	ProjectName     string     `json:"project_name"`
	Status          string     `json:"status"`
	DueDate         *Date      `json:"due_date" swaggertype:"string" example:"2026-10-31"`
	SpentMinutes    int        `json:"spent_minutes"`
	EstimateMinutes *int       `json:"estimate_minutes"`
	Overdue         bool       `json:"overdue"`
	UpdatedAt       time.Time  `json:"updated_at"`
	SprintID        *uuid.UUID `json:"sprint_id"`
}

// MaxMine — «Мои задачи» отдаются одним списком, не больше этого числа (самые срочные).
const MaxMine = 200

// Mine — GET /me/tasks: открытые задачи пользователя по всем его проектам, кроме архивных и
// удалённых (§7.2), по сроку (без срока — в конце), просроченные оказываются сверху; не больше MaxMine.
func (s *Service) Mine(ctx context.Context, u *user.User) ([]MyTask, error) {
	list := []MyTask{}
	err := s.db.WithContext(ctx).Raw(`SELECT t.key, t.title, p.key AS project_key, p.name AS project_name, t.status,
			t.due_date, t.spent_minutes, t.estimate_minutes, t.updated_at, t.sprint_id,
			(t.due_date IS NOT NULL AND t.due_date < (?::timestamptz AT TIME ZONE p.timezone)::date) AS overdue
		FROM tasks t
		JOIN projects p ON p.id = t.project_id AND p.deleted_at IS NULL AND NOT p.is_archived
		JOIN project_members m ON m.project_id = p.id AND m.user_id = t.assignee_id
		WHERE t.assignee_id = ? AND t.deleted_at IS NULL AND t.status NOT IN ('done', 'cancelled')
		ORDER BY t.due_date NULLS LAST, p.key, t.number LIMIT ?`, s.now(), u.ID, MaxMine).Scan(&list).Error
	if err != nil {
		return nil, fmt.Errorf("task: my tasks: %w", err)
	}
	return list, nil
}

func idCondition(column string, f IDFilter) (string, []any) {
	switch {
	case f.Null && len(f.IDs) > 0:
		return "(" + column + " IS NULL OR " + column + " IN ?)", []any{f.IDs}
	case f.Null:
		return column + " IS NULL", nil
	case len(f.IDs) > 0:
		return column + " IN ?", []any{f.IDs}
	}
	return "", nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func encodeCursor(sprint *uuid.UUID, pos float64, num int) string {
	section := "~"
	if sprint != nil {
		section = sprint.String()
	}
	return base64.RawURLEncoding.EncodeToString([]byte(section + "|" + strconv.FormatFloat(pos, 'g', -1, 64) + "|" + strconv.Itoa(num)))
}

func decodeCursor(c string) (string, float64, int, error) {
	bad := fieldError("cursor", "некорректный курсор")
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", 0, 0, bad
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", 0, 0, bad
	}
	if parts[0] != "~" {
		if _, err := uuid.Parse(parts[0]); err != nil {
			return "", 0, 0, bad
		}
	}
	pos, err1 := strconv.ParseFloat(parts[1], 64)
	num, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil {
		return "", 0, 0, bad
	}
	return parts[0], pos, num, nil
}
