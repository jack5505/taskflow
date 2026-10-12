package sprint

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Report — отчёт по спринту (§5.6).
type Report struct {
	Sprint View `json:"sprint"`
	// Start — объём на старте (§5.2), не меняется.
	Start Scope `json:"start"`
	// Current — задачи и сумма оценок сейчас; у завершённого — на момент закрытия.
	Current Totals `json:"current"`
	// Added, Removed — задачи, добавленные и убранные после старта (журнал спринта §5.3).
	Added   []Change `json:"added"`
	Removed []Change `json:"removed"`
	// Spent — списанное время по задачам спринта с разбивкой по исполнителям, SpentMinutes — итог.
	Spent        []Spent `json:"spent"`
	SpentMinutes int     `json:"spent_minutes"`
	// Burndown — по точке на каждый день спринта, от start_date до end_date включительно.
	Burndown []Point `json:"burndown"`
}

// Totals — число задач и сумма их оценок в минутах (без оценки — 0).
type Totals struct {
	Tasks           int `json:"tasks"`
	EstimateMinutes int `json:"estimate_minutes"`
}

// Change — запись журнала спринта: задача, кто и когда.
type Change struct {
	TaskID uuid.UUID  `json:"task_id"`
	Key    string     `json:"key"`
	Title  string     `json:"title"`
	By     *uuid.UUID `json:"by" extensions:"x-nullable"` // null — система
	At     time.Time  `json:"at"`
	Action string     `json:"-"`
}

// Spent — сумма записей времени одного пользователя по задачам спринта.
type Spent struct {
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Minutes     int       `json:"minutes"`
	Hours       float64   `json:"hours" example:"2.5"`
}

// Point — точка берндауна на конец дня date по поясу проекта. IdealHours — идеальная прямая от
// объёма на старте до нуля в конце последнего дня. Остальные поля — из снимка за этот день (§3.8);
// null — снимка нет (день ещё не закончился или спринт закрыт раньше).
type Point struct {
	Date                task.Date `json:"date" swaggertype:"string" example:"2026-10-13"`
	IdealHours          float64   `json:"ideal_hours" example:"36"`
	RemainingHours      *float64  `json:"remaining_hours" example:"30.5" extensions:"x-nullable"`
	OpenTasks           *int      `json:"open_tasks" extensions:"x-nullable"`
	OpenEstimateMinutes *int      `json:"open_estimate_minutes" extensions:"x-nullable"`
	DoneTasks           *int      `json:"done_tasks" extensions:"x-nullable"`
}

// Report — отчёт по спринту (§5.6) для участника проекта (не участник — 404, как у карточки спринта).
// Доступен для active и closed; у запланированного — 409 INVALID_TRANSITION (details.reason = planned).
func (s *Service) Report(ctx context.Context, u *user.User, id uuid.UUID) (*Report, error) {
	v, _, err := s.Get(ctx, u, id)
	if err != nil {
		return nil, err
	}
	if v.State == StatePlanned {
		return nil, conflict("planned", "Отчёт доступен для активного и завершённого спринта")
	}
	db := s.db.WithContext(ctx)
	r := Report{Sprint: *v, Added: []Change{}, Removed: []Change{}, Spent: []Spent{}}

	if err := db.Raw(`SELECT count(*) AS tasks, COALESCE(SUM(estimate_minutes), 0) AS estimate_minutes,
			count(*) FILTER (WHERE estimate_minutes = 0) AS without_estimate
		FROM sprint_start_scope WHERE sprint_id = ?`, id).Scan(&r.Start).Error; err != nil {
		return nil, fmt.Errorf("sprint: report start: %w", err)
	}
	// Задачи спринта сейчас; у завершённого к ним прибавляются незакрытые из снимка закрытия —
	// при закрытии они ушли в бэклог или следующий спринт (§5.4).
	r.Current = Totals{Tasks: v.TasksCount, EstimateMinutes: v.EstimateMinutes}
	if v.State == StateClosed {
		var sn []Totals
		if err := db.Raw(`SELECT open_tasks AS tasks, open_estimate_minutes AS estimate_minutes FROM sprint_daily_snapshots
			WHERE sprint_id = ? ORDER BY date DESC LIMIT 1`, id).Scan(&sn).Error; err != nil {
			return nil, fmt.Errorf("sprint: report current: %w", err)
		}
		if len(sn) > 0 {
			r.Current.Tasks += sn[0].Tasks
			r.Current.EstimateMinutes += sn[0].EstimateMinutes
		}
	}

	var changes []Change
	if err := db.Raw(`SELECT task_id, user_id AS "by", created_at AS "at", action
		FROM sprint_log WHERE sprint_id = ? ORDER BY created_at, id`, id).Scan(&changes).Error; err != nil {
		return nil, fmt.Errorf("sprint: report log: %w", err)
	}
	ids := make([]uuid.UUID, len(changes))
	for i, c := range changes {
		ids[i] = c.TaskID
	}
	titles, err := task.LiveTitles(db, ids)
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		t, ok := titles[c.TaskID]
		if !ok {
			continue // задача удалена
		}
		c.Key, c.Title = t.Key, t.Title
		if c.Action == "added" {
			r.Added = append(r.Added, c)
		} else {
			r.Removed = append(r.Removed, c)
		}
	}

	if err := s.spent(ctx, &r); err != nil {
		return nil, err
	}
	if err := s.burndown(ctx, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// spent — списанное время по задачам спринта (§5.6) с разбивкой по тем, кто списывал. Задачи
// спринта — все, что были в нём после старта: сейчас, на старте, добавленные или убранные (так в
// отчёт попадают и незакрытые, перенесённые при закрытии). Записи — за дни спринта: от start_date
// до даты закрытия включительно по поясу проекта (у активного — без верхней границы). Так работа
// над задачей в бэклоге и в соседнем спринте сюда не относится. Удалённые задачи не считаются.
func (s *Service) spent(ctx context.Context, r *Report) error {
	sp := r.Sprint
	if sp.StartDate == nil {
		return nil // без даты начала записи не отбираются (как раньше: сравнение с NULL)
	}
	db := s.db.WithContext(ctx)
	var until *time.Time
	if sp.ClosedAt != nil {
		zones, err := project.Timezones(db, []uuid.UUID{sp.ProjectID}, true)
		if err != nil {
			return err
		}
		d, err := dateIn(*sp.ClosedAt, zones[sp.ProjectID])
		if err != nil {
			return err
		}
		until = &d
	}
	ids, err := task.IDsInSprint(db, sp.ID)
	if err != nil {
		return err
	}
	var logged []uuid.UUID
	if err := db.Raw(`SELECT task_id FROM sprint_start_scope WHERE sprint_id = @s
		UNION SELECT task_id FROM sprint_log WHERE sprint_id = @s`, map[string]any{"s": sp.ID}).Scan(&logged).Error; err != nil {
		return fmt.Errorf("sprint: report spent: %w", err)
	}
	for _, id := range logged {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	byUser, err := task.SpentByUser(db, ids, sp.StartDate.Time, until)
	if err != nil {
		return err
	}
	userIDs := make([]uuid.UUID, len(byUser))
	for i, m := range byUser {
		userIDs[i] = m.UserID
	}
	names, err := user.Names(db, userIDs...)
	if err != nil {
		return err
	}
	for _, m := range byUser {
		n, ok := names[m.UserID]
		if !ok {
			continue // как JOIN users: записи без пользователя не бывает
		}
		r.Spent = append(r.Spent, Spent{UserID: m.UserID, DisplayName: n.DisplayName, Minutes: m.Minutes})
	}
	// Как ORDER BY minutes DESC, display_name, user_id.
	slices.SortFunc(r.Spent, func(a, b Spent) int {
		return cmp.Or(cmp.Compare(b.Minutes, a.Minutes), cmp.Compare(a.DisplayName, b.DisplayName),
			cmp.Compare(a.UserID.String(), b.UserID.String()))
	})
	for i := range r.Spent {
		r.Spent[i].Hours = hours(r.Spent[i].Minutes)
		r.SpentMinutes += r.Spent[i].Minutes
	}
	return nil
}

// burndown — точки по дням спринта (§5.6) из ежедневных снимков (§3.8).
func (s *Service) burndown(ctx context.Context, r *Report) error {
	r.Burndown = []Point{}
	if r.Sprint.StartDate == nil || r.Sprint.EndDate == nil {
		return nil
	}
	var snaps []struct {
		Date                time.Time
		OpenTasks           int
		OpenEstimateMinutes int
		DoneTasks           int
	}
	if err := s.db.WithContext(ctx).Raw(`SELECT date, open_tasks, open_estimate_minutes, done_tasks
		FROM sprint_daily_snapshots WHERE sprint_id = ? AND date BETWEEN ? AND ?`,
		r.Sprint.ID, *r.Sprint.StartDate, *r.Sprint.EndDate).Scan(&snaps).Error; err != nil {
		return fmt.Errorf("sprint: report snapshots: %w", err)
	}
	byDate := make(map[string]int, len(snaps))
	for i, sn := range snaps {
		byDate[sn.Date.Format(time.DateOnly)] = i
	}
	start, end := r.Sprint.StartDate.Time, r.Sprint.EndDate.Time
	n := int(end.Sub(start).Hours()/24) + 1
	for i := range n {
		d := start.AddDate(0, 0, i)
		p := Point{Date: task.Date{Time: d}, IdealHours: roundHours(float64(r.Start.EstimateMinutes*(n-i-1)) / float64(n) / 60)}
		if j, ok := byDate[d.Format(time.DateOnly)]; ok {
			sn := snaps[j]
			h := hours(sn.OpenEstimateMinutes)
			p.RemainingHours, p.OpenTasks, p.OpenEstimateMinutes, p.DoneTasks = &h, &sn.OpenTasks, &sn.OpenEstimateMinutes, &sn.DoneTasks
		}
		r.Burndown = append(r.Burndown, p)
	}
	return nil
}

// hours — минуты в десятичных часах с двумя знаками (§6.1).
func hours(minutes int) float64 {
	return roundHours(float64(minutes) / 60)
}

func roundHours(h float64) float64 {
	return math.Round(h*100) / 100
}
