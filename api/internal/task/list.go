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
	"github.com/jack5505/taskflow/api/internal/model"
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

	var after *listCursor
	if f.Cursor != "" {
		section, pos, num, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, err
		}
		after = &listCursor{section, pos, num}
	}
	items, err := listTasks(s.db.WithContext(ctx), a.Project.ID, f, after, limit+1)
	if err != nil {
		return nil, err
	}
	today, err := todayIn(a.Project.Timezone, s.now())
	if err != nil {
		return nil, err
	}
	for i := range items {
		t := &items[i]
		t.Overdue = t.DueDate != nil && t.Status != StatusDone && t.Status != StatusCancelled && t.DueDate.Before(today)
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
	db := s.db.WithContext(ctx)
	// Проекты, где пользователь участник, неудалённые и неархивные, — по ключу (ProjectsOf).
	mine, err := project.ProjectsOf(db, u.ID)
	if err != nil {
		return nil, err
	}
	var open []project.UserProject
	for _, p := range mine {
		if !p.IsArchived {
			open = append(open, p)
		}
	}
	if len(open) == 0 {
		return list, nil
	}
	ids := make([]uuid.UUID, len(open))
	for i, p := range open {
		ids[i] = p.ID
	}
	// Пояса и заодно перепроверка: проект мог стать удалённым или архивным после ProjectsOf.
	briefs, err := project.LiveBriefs(db, ids, true)
	if err != nil {
		return nil, err
	}
	now := s.now()
	today := make(map[uuid.UUID]time.Time, len(briefs))
	for id, b := range briefs {
		if today[id], err = todayIn(b.Timezone, now); err != nil {
			return nil, err
		}
	}
	// Порядок проектов по ключу — как ORDER BY p.key: ids уже в порядке ключей (ProjectsOf).
	rows, err := assignedOpen(db, u.ID, ids, MaxMine)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]project.UserProject, len(open))
	for _, p := range open {
		byID[p.ID] = p
	}
	for _, r := range rows {
		d, live := today[r.ProjectID]
		if !live {
			continue
		}
		t := r.MyTask
		p := byID[r.ProjectID]
		t.ProjectKey, t.ProjectName = p.Key, p.Name
		t.Overdue = t.DueDate != nil && t.DueDate.Before(d)
		list = append(list, t)
	}
	return list, nil
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

// todayIn — сегодняшняя дата по поясу tz (полночь UTC, как даты из базы).
func todayIn(tz string, now time.Time) (time.Time, error) {
	d, err := model.DateIn(now, tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("task: %w", err)
	}
	return d, nil
}
