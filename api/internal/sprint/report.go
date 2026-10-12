package sprint

import (
	"cmp"
	"context"
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

	if r.Start, err = startScopeTotals(db, id); err != nil {
		return nil, err
	}
	// Задачи спринта сейчас; у завершённого к ним прибавляются незакрытые из снимка закрытия —
	// при закрытии они ушли в бэклог или следующий спринт (§5.4).
	r.Current = Totals{Tasks: v.TasksCount, EstimateMinutes: v.EstimateMinutes}
	if v.State == StateClosed {
		sn, err := lastSnapshotOpen(db, id)
		if err != nil {
			return nil, err
		}
		if sn != nil {
			r.Current.Tasks += sn.Tasks
			r.Current.EstimateMinutes += sn.EstimateMinutes
		}
	}

	changes, err := sprintLog(db, id)
	if err != nil {
		return nil, err
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
	// Задачи, бывшие в спринте после старта: на старте и в журнале (уникальность — UNION).
	logged, err := scopeAndLogTasks(db, sp.ID)
	if err != nil {
		return err
	}
	byUser, err := task.SpentByUser(db, append(ids, logged...), sp.StartDate.Time, until)
	if err != nil {
		return err
	}
	minutes := make(map[uuid.UUID]int, len(byUser))
	userIDs := make([]uuid.UUID, len(byUser))
	for i, m := range byUser {
		userIDs[i], minutes[m.UserID] = m.UserID, m.Minutes
	}
	// Имена — в порядке базы (display_name, id); стабильная сортировка по минутам сохраняет его
	// у равных: как ORDER BY minutes DESC, display_name, user_id.
	names, err := user.NamesOrdered(db, userIDs)
	if err != nil {
		return err
	}
	for _, n := range names {
		r.Spent = append(r.Spent, Spent{UserID: n.ID, DisplayName: n.DisplayName, Minutes: minutes[n.ID]})
	}
	slices.SortStableFunc(r.Spent, func(a, b Spent) int { return cmp.Compare(b.Minutes, a.Minutes) })
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
	snaps, err := snapshotsBetween(s.db.WithContext(ctx), r.Sprint.ID, *r.Sprint.StartDate, *r.Sprint.EndDate)
	if err != nil {
		return err
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
