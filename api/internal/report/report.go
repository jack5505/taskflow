// Package report — отчёты по времени (ТЗ §6.6, §9.2 GET /reports/time): по пользователям
// (строка на пользователя, столбец на день периода) и по задачам; выгрузка в CSV для русского
// Excel. Суммы считает PostgreSQL (GROUP BY), в Go — только раскладка по дням.
package report

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Представления отчёта (§6.6).
const (
	GroupByUser = "user"
	GroupByTask = "task"
)

// maxPeriodDays — предел длины периода: столбец на день, больше года в отчёте не читается.
const maxPeriodDays = 366

var (
	errSprintNotFound = apierr.NotFound("Спринт не найден")
	// §2, §6.6: исполнитель видит отчёты только по себе.
	errOthersForbidden = apierr.Forbidden("Отчёт по другим пользователям доступен менеджеру проекта")
)

func fieldError(field, reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: field, Reason: reason}})
}

// Query — фильтры §6.6 и §9.2. Пустые From/To — текущая неделя (пн–вс) по поясу пользователя.
type Query struct {
	From, To string // ГГГГ-ММ-ДД, обе границы включительно
	Project  string // ключ проекта
	SprintID *uuid.UUID
	UserID   *uuid.UUID
	GroupBy  string // user (по умолчанию) | task
}

// Day — столбец представления по пользователям; выходные выделяются серым (§6.6).
type Day struct {
	Date    string `json:"date" example:"2026-10-05"`
	Weekend bool   `json:"weekend"`
}

// UserRow — строка представления по пользователям: минуты по дням периода (как days) и итог.
type UserRow struct {
	UserID       uuid.UUID `json:"user_id"`
	UserName     string    `json:"user_name"`
	Minutes      []int     `json:"minutes"`
	TotalMinutes int       `json:"total_minutes" example:"150"`
	Hours        string    `json:"hours" example:"2.50"`
}

// TaskRow — строка представления по задачам. total_minutes — всё время по задаче за всё время
// в пределах тех же фильтров по пользователю и прав (без фильтра — как spent_minutes задачи).
type TaskRow struct {
	TaskID          uuid.UUID  `json:"task_id"`
	Key             string     `json:"key" example:"TM-42"`
	Title           string     `json:"title"`
	AssigneeID      *uuid.UUID `json:"assignee_id"`
	AssigneeName    *string    `json:"assignee_name"`
	Status          string     `json:"status" enums:"todo,in_progress,review,done,cancelled"`
	EstimateMinutes *int       `json:"estimate_minutes"`
	PeriodMinutes   int        `json:"period_minutes"`
	TotalMinutes    int        `json:"total_minutes"`
	Deleted         bool       `json:"deleted"` // §4: «удалённая задача»
	PeriodHours     string     `json:"period_hours" example:"2.50"`
	TotalHours      string     `json:"total_hours" example:"10.00"`
}

// Report — ответ GET /reports/time. Для group_by=user заполнены days, users, day_totals;
// для task — tasks; поля другого представления — пустые массивы.
type Report struct {
	From         string    `json:"from" example:"2026-10-05"`
	To           string    `json:"to" example:"2026-10-11"`
	GroupBy      string    `json:"group_by" enums:"user,task"`
	Days         []Day     `json:"days"`
	Users        []UserRow `json:"users"`
	DayTotals    []int     `json:"day_totals"`
	Tasks        []TaskRow `json:"tasks"`
	TotalMinutes int       `json:"total_minutes"`
	Total        string    `json:"total" example:"2h 30m"`
	Hours        string    `json:"hours" example:"2.50"`
}

// Service — отчёты по времени. Бот и HTTP вызывают один сервис (§9.1).
type Service struct {
	db       *gorm.DB
	projects *project.Service
	now      func() time.Time
}

func NewService(db *gorm.DB, projects *project.Service) *Service {
	return &Service{db: db, projects: projects, now: time.Now}
}

// WithClock подменяет часы (тесты периода по умолчанию).
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// filter — условия WHERE над worklogs w, tasks t, projects p, кроме периода.
type filter struct {
	cond []string
	args []any
	// restricted — видны не все записи задач (фильтр по пользователю или «только свои»):
	// «списано всего» тогда считается по видимым записям, а не из tasks.spent_minutes.
	restricted bool
}

func (f *filter) add(cond string, args ...any) {
	f.cond = append(f.cond, cond)
	f.args = append(f.args, args...)
}

func (f *filter) where() string { return strings.Join(f.cond, " AND ") }

// Time — отчёт по времени для u. Порядок ответов: 400 (период, group_by) → 404 (проект, спринт)
// → 403 (чужой пользователь у исполнителя). Удалённые задачи остаются с пометкой (§4), удалённые
// проекты в отчёт не входят (§3.2), архивные — входят.
func (s *Service) Time(ctx context.Context, u *user.User, q Query) (*Report, error) {
	from, to, err := s.period(u, q.From, q.To)
	if err != nil {
		return nil, err
	}
	switch q.GroupBy {
	case "":
		q.GroupBy = GroupByUser
	case GroupByUser, GroupByTask:
	default:
		return nil, fieldError("group_by", "user или task")
	}
	f, err := s.scope(ctx, u, q)
	if err != nil {
		return nil, err
	}
	r := &Report{From: from.Format(time.DateOnly), To: to.Format(time.DateOnly), GroupBy: q.GroupBy,
		Days: []Day{}, Users: []UserRow{}, DayTotals: []int{}, Tasks: []TaskRow{}}
	if q.GroupBy == GroupByUser {
		err = s.byUser(ctx, f, from, to, r)
	} else {
		err = s.byTask(ctx, f, from, to, r)
	}
	if err != nil {
		return nil, err
	}
	r.Total, r.Hours = duration.Format(r.TotalMinutes), duration.Hours(r.TotalMinutes)
	return r, nil
}

// period — границы отчёта (даты в UTC-полночь, как work_date из базы). По умолчанию — текущая
// неделя понедельник–воскресенье по поясу пользователя (§6.6, §11 п. 7: неделя с понедельника).
func (s *Service) period(u *user.User, fromS, toS string) (from, to time.Time, err error) {
	loc, lerr := time.LoadLocation(u.Timezone)
	if lerr != nil {
		loc = time.UTC
	}
	n := s.now().In(loc)
	today := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	from, to = monday, monday.AddDate(0, 0, 6)
	if fromS != "" {
		if from, err = time.Parse(time.DateOnly, fromS); err != nil {
			return from, to, fieldError("from", "дата ГГГГ-ММ-ДД")
		}
	}
	if toS != "" {
		if to, err = time.Parse(time.DateOnly, toS); err != nil {
			return from, to, fieldError("to", "дата ГГГГ-ММ-ДД")
		}
	}
	if to.Before(from) {
		return from, to, fieldError("to", "не раньше from")
	}
	if days(from, to) > maxPeriodDays {
		return from, to, fieldError("to", fmt.Sprintf("период не длиннее %d дней", maxPeriodDays))
	}
	return from, to, nil
}

// days — число дней периода включительно.
func days(from, to time.Time) int { return int(to.Sub(from).Hours()/24) + 1 }

// scope — права §2 и фильтры в условия запроса. Администратор видит всё; в проекте, где
// пользователь менеджер, — все записи; где исполнитель — только свои; чужие проекты не видны.
func (s *Service) scope(ctx context.Context, u *user.User, q Query) (*filter, error) {
	f := &filter{}
	f.add("p.deleted_at IS NULL")
	if q.SprintID != nil {
		// Спринт определяет проект; права проверяются как у фильтра по проекту.
		var keys []string
		if err := s.db.WithContext(ctx).Raw(`SELECT p.key FROM sprints s JOIN projects p ON p.id = s.project_id
			WHERE s.id = ? AND p.deleted_at IS NULL`, *q.SprintID).Scan(&keys).Error; err != nil {
			return nil, fmt.Errorf("report: sprint: %w", err)
		}
		if len(keys) == 0 || (q.Project != "" && q.Project != keys[0]) {
			return nil, errSprintNotFound
		}
		q.Project = keys[0]
		f.add("t.sprint_id = ?", *q.SprintID)
	}
	others := q.UserID != nil && *q.UserID != u.ID
	switch {
	case q.Project != "":
		a, err := s.projects.Access(ctx, u, q.Project)
		if err != nil {
			if errors.Is(err, project.ErrNotFound) && q.SprintID != nil {
				return nil, errSprintNotFound
			}
			return nil, err
		}
		f.add("t.project_id = ?", a.Project.ID)
		if !a.CanManage() {
			if others {
				return nil, errOthersForbidden
			}
			f.add("w.user_id = ?", u.ID)
			f.restricted = true
		}
	case !u.IsSystemAdmin:
		// Проекты пользователя (их не больше 50, §10.1) раскрываются в списки: так условие
		// «только свои» превращается в w.user_id = ? и идёт по индексу пользователя.
		var ms []struct {
			ProjectID uuid.UUID
			Role      string
		}
		if err := s.db.WithContext(ctx).Raw(`SELECT m.project_id, m.role FROM project_members m
			JOIN projects p ON p.id = m.project_id WHERE m.user_id = ? AND p.deleted_at IS NULL`, u.ID).
			Scan(&ms).Error; err != nil {
			return nil, fmt.Errorf("report: memberships: %w", err)
		}
		var managed, own []uuid.UUID
		for _, m := range ms {
			if m.Role == project.RoleManager {
				managed = append(managed, m.ProjectID)
			} else {
				own = append(own, m.ProjectID)
			}
		}
		if others && len(managed) == 0 {
			return nil, errOthersForbidden
		}
		switch {
		case len(managed) > 0 && len(own) > 0:
			f.add("(t.project_id IN ? OR (w.user_id = ? AND t.project_id IN ?))", managed, u.ID, own)
		case len(managed) > 0:
			f.add("t.project_id IN ?", managed)
		case len(own) > 0:
			f.add("w.user_id = ? AND t.project_id IN ?", u.ID, own)
		default:
			f.add("false")
		}
		// Только управляемые проекты — видны все записи их задач, «всего» = spent_minutes.
		f.restricted = len(own) > 0
	}
	if q.UserID != nil {
		f.add("w.user_id = ?", *q.UserID)
		f.restricted = true
	}
	return f, nil
}

// base — записи периода с задачей и проектом; к нему дописываются условия filter.
const base = `FROM worklogs w JOIN tasks t ON t.id = w.task_id JOIN projects p ON p.id = t.project_id
	WHERE w.work_date BETWEEN ? AND ? AND `

func (s *Service) byUser(ctx context.Context, f *filter, from, to time.Time, r *Report) error {
	var cells []struct {
		UserID   uuid.UUID
		UserName string
		WorkDate time.Time
		Minutes  int
	}
	args := append([]any{from, to}, f.args...)
	if err := s.db.WithContext(ctx).Raw(`SELECT a.user_id, u.display_name AS user_name, a.work_date, a.minutes
		FROM (SELECT w.user_id, w.work_date, SUM(w.minutes) AS minutes `+base+f.where()+`
			GROUP BY w.user_id, w.work_date) a
		JOIN users u ON u.id = a.user_id`, args...).Scan(&cells).Error; err != nil {
		return fmt.Errorf("report: by user: %w", err)
	}
	n := days(from, to)
	for i := range n {
		d := from.AddDate(0, 0, i)
		wd := d.Weekday()
		r.Days = append(r.Days, Day{Date: d.Format(time.DateOnly), Weekend: wd == time.Saturday || wd == time.Sunday})
	}
	r.DayTotals = make([]int, n)
	rows := map[uuid.UUID]*UserRow{}
	for _, c := range cells {
		row := rows[c.UserID]
		if row == nil {
			row = &UserRow{UserID: c.UserID, UserName: c.UserName, Minutes: make([]int, n)}
			rows[c.UserID] = row
		}
		i := int(c.WorkDate.UTC().Sub(from).Hours() / 24)
		row.Minutes[i] += c.Minutes
		row.TotalMinutes += c.Minutes
		r.DayTotals[i] += c.Minutes
		r.TotalMinutes += c.Minutes
	}
	for _, row := range rows {
		row.Hours = duration.Hours(row.TotalMinutes)
		r.Users = append(r.Users, *row)
	}
	sort.Slice(r.Users, func(i, j int) bool {
		a, b := r.Users[i], r.Users[j]
		if a.UserName != b.UserName {
			return a.UserName < b.UserName
		}
		return a.UserID.String() < b.UserID.String()
	})
	return nil
}

func (s *Service) byTask(ctx context.Context, f *filter, from, to time.Time, r *Report) error {
	total := "t.spent_minutes"
	args := append([]any{from, to}, f.args...)
	if f.restricted {
		// Те же условия без периода; w внутри подзапроса — записи этой задачи.
		total = `(SELECT COALESCE(SUM(w.minutes), 0) FROM worklogs w WHERE w.task_id = t.id AND ` + f.where() + `)`
		args = append(args, f.args...)
	}
	if err := s.db.WithContext(ctx).Raw(`WITH period AS (
			SELECT w.task_id, SUM(w.minutes) AS minutes `+base+f.where()+` GROUP BY w.task_id)
		SELECT t.id AS task_id, t.key, t.title, t.assignee_id, au.display_name AS assignee_name, t.status,
			t.estimate_minutes, pr.minutes AS period_minutes, `+total+` AS total_minutes,
			t.deleted_at IS NOT NULL AS deleted
		FROM period pr JOIN tasks t ON t.id = pr.task_id JOIN projects p ON p.id = t.project_id
		LEFT JOIN users au ON au.id = t.assignee_id
		ORDER BY p.key, t.number`, args...).Scan(&r.Tasks).Error; err != nil {
		return fmt.Errorf("report: by task: %w", err)
	}
	for i := range r.Tasks {
		t := &r.Tasks[i]
		t.PeriodHours, t.TotalHours = duration.Hours(t.PeriodMinutes), duration.Hours(t.TotalMinutes)
		r.TotalMinutes += t.PeriodMinutes
	}
	return nil
}
