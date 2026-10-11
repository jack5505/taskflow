package sprint

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

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
	if err := db.Raw(`SELECT c.tasks + COALESCE(sn.open_tasks, 0) AS tasks,
			c.estimate_minutes + COALESCE(sn.open_estimate_minutes, 0) AS estimate_minutes
		FROM (SELECT count(*) AS tasks, COALESCE(SUM(estimate_minutes), 0) AS estimate_minutes
			FROM tasks WHERE sprint_id = @s AND deleted_at IS NULL) c
		LEFT JOIN LATERAL (SELECT open_tasks, open_estimate_minutes FROM sprint_daily_snapshots
			WHERE sprint_id = @s AND @closed ORDER BY date DESC LIMIT 1) sn ON true`,
		map[string]any{"s": id, "closed": v.State == StateClosed}).Scan(&r.Current).Error; err != nil {
		return nil, fmt.Errorf("sprint: report current: %w", err)
	}

	var changes []Change
	if err := db.Raw(`SELECT l.task_id, t.key, t.title, l.user_id AS "by", l.created_at AS "at", l.action
		FROM sprint_log l JOIN tasks t ON t.id = l.task_id
		WHERE l.sprint_id = ? ORDER BY l.created_at, l.id`, id).Scan(&changes).Error; err != nil {
		return nil, fmt.Errorf("sprint: report log: %w", err)
	}
	for _, c := range changes {
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
// спринта — те, что в нём сейчас, плюс бывшие в нём на старте или добавленные после, если их
// не убрали после старта (так в отчёт попадают незакрытые, перенесённые при закрытии). У
// завершённого спринта записи берутся по дату закрытия включительно (по поясу проекта): работа
// над перенесёнными задачами в следующем спринте сюда не относится. Удалённые задачи не считаются.
func (s *Service) spent(ctx context.Context, r *Report) error {
	var until *string
	if r.Sprint.ClosedAt != nil {
		if err := s.db.WithContext(ctx).Raw(`SELECT ((?::timestamptz AT TIME ZONE timezone)::date)::text FROM projects WHERE id = ?`,
			*r.Sprint.ClosedAt, r.Sprint.ProjectID).Scan(&until).Error; err != nil {
			return fmt.Errorf("sprint: report close date: %w", err)
		}
	}
	if err := s.db.WithContext(ctx).Raw(`WITH last AS (
			SELECT DISTINCT ON (task_id) task_id, action FROM sprint_log WHERE sprint_id = @s
			ORDER BY task_id, created_at DESC, id DESC),
		st AS (
			SELECT id AS task_id FROM tasks WHERE sprint_id = @s
			UNION
			SELECT x.task_id FROM (SELECT task_id FROM sprint_start_scope WHERE sprint_id = @s
				UNION SELECT task_id FROM sprint_log WHERE sprint_id = @s) x
			LEFT JOIN last USING (task_id) WHERE last.action IS DISTINCT FROM 'removed')
		SELECT w.user_id, u.display_name, SUM(w.minutes) AS minutes
		FROM worklogs w JOIN st ON st.task_id = w.task_id
		JOIN tasks t ON t.id = w.task_id AND t.deleted_at IS NULL
		JOIN users u ON u.id = w.user_id
		WHERE CAST(@until AS date) IS NULL OR w.work_date <= CAST(@until AS date)
		GROUP BY w.user_id, u.display_name ORDER BY minutes DESC, u.display_name, w.user_id`,
		map[string]any{"s": r.Sprint.ID, "until": until}).Scan(&r.Spent).Error; err != nil {
		return fmt.Errorf("sprint: report spent: %w", err)
	}
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
