package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// MaxFreeTasks — /tasks показывает не больше стольких задач на все проекты группы (§8.5).
const MaxFreeTasks = 20

// freeTitle — длина названия задачи в списке /tasks: 20 строк должны влезть в одно сообщение.
const freeTitle = 100

// RegisterGroupLists подключает команды группы /tasks и /sprint (§8.5): по всем проектам группы
// с разбивкой по проектам или по одному — /tasks <ключ проекта>, /sprint <ключ проекта>.
func RegisterGroupLists(rt *Router, db *gorm.DB, groups *Groups) {
	rt.Command("tasks", tasksCommand(db, groups))
	rt.Command("sprint", sprintCommand(db, groups))
}

// sprintSummary — активный спринт для /sprint и ежедневной сводки (шаблон sprint_block).
type sprintSummary struct {
	Name, Dates     string
	Statuses        string // «К выполнению: 5 · В работе: 3 · …»
	Spent, Estimate string
}

// activeSprint — сводка активного спринта проекта (§8.5 /sprint): название, даты, задачи по
// статусам, списано из оценки (оценка без отменённых задач). Нет активного спринта — nil.
func activeSprint(db *gorm.DB, projectID uuid.UUID) (*sprintSummary, error) {
	var sp []struct {
		ID                 uuid.UUID
		Name               string
		StartDate, EndDate *time.Time
	}
	if err := db.Raw(`SELECT id, name, start_date, end_date FROM sprints WHERE project_id = ? AND state = 'active' LIMIT 1`,
		projectID).Scan(&sp).Error; err != nil {
		return nil, fmt.Errorf("telegram: active sprint: %w", err)
	}
	if len(sp) == 0 {
		return nil, nil
	}
	var st []struct {
		Status   string
		N        int
		Spent    int
		Estimate int
	}
	if err := db.Raw(`SELECT status, count(*) AS n, COALESCE(SUM(spent_minutes), 0) AS spent,
			COALESCE(SUM(estimate_minutes), 0) AS estimate
		FROM tasks WHERE sprint_id = ? AND deleted_at IS NULL GROUP BY status`, sp[0].ID).Scan(&st).Error; err != nil {
		return nil, fmt.Errorf("telegram: sprint statuses: %w", err)
	}
	counts := map[string]int{}
	spent, estimate := 0, 0
	for _, s := range st {
		counts[s.Status] = s.N
		spent += s.Spent
		if s.Status != task.StatusCancelled {
			estimate += s.Estimate
		}
	}
	var parts []string
	for _, s := range []string{task.StatusTodo, task.StatusInProgress, task.StatusReview, task.StatusDone, task.StatusCancelled} {
		if counts[s] > 0 || s != task.StatusCancelled {
			parts = append(parts, fmt.Sprintf("%s: %d", task.StatusName(s), counts[s]))
		}
	}
	s := &sprintSummary{Name: sp[0].Name, Statuses: strings.Join(parts, " · "),
		Spent: duration.Format(spent), Estimate: duration.Format(estimate)}
	if start, end := sp[0].StartDate, sp[0].EndDate; start != nil && end != nil {
		layout := "02.01"
		if start.Year() != end.Year() {
			layout = "02.01.2006" // спринт через Новый год
		}
		s.Dates = start.Format(layout) + "–" + end.Format("02.01.2006")
	}
	return s, nil
}

// chatProjects — проекты группы для /tasks и /sprint: все или один по ключу из аргумента.
// Ответ уже отправлен (личный чат, группа без проектов, чужой ключ) — nil.
func chatProjects(ctx context.Context, r *Request, groups *Groups) ([]project.Project, error) {
	if r.Private() {
		return nil, r.Reply(ctx, Text("group_only", nil))
	}
	ps, err := groups.ChatProjects(ctx, r.Chat.ID)
	if err != nil {
		return nil, err
	}
	if len(ps) == 0 {
		return nil, r.Reply(ctx, Text("group_no_projects", nil))
	}
	key := strings.ToUpper(strings.TrimSpace(r.Args))
	if key == "" {
		return ps, nil
	}
	for _, p := range ps {
		if p.Key == key {
			return []project.Project{p}, nil
		}
	}
	return nil, r.Reply(ctx, Text("unbind_not_here", key))
}

// sprintCommand — /sprint [ключ проекта]: сводка активного спринта по каждому проекту группы
// одним сообщением (§8.5).
func sprintCommand(db *gorm.DB, groups *Groups) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		ps, err := chatProjects(ctx, r, groups)
		if err != nil || ps == nil {
			return err
		}
		type item struct {
			Key, Name string
			Sprint    *sprintSummary
		}
		items := make([]item, len(ps))
		for i, p := range ps {
			s, err := activeSprint(db.WithContext(ctx), p.ID)
			if err != nil {
				return err
			}
			items[i] = item{Key: p.Key, Name: p.Name, Sprint: s}
		}
		return r.Reply(ctx, clip(Text("cmd_sprint", items), maxMessage))
	}
}

// freeTask — строка /tasks.
type freeTask struct {
	ID         uuid.UUID
	ProjectID  uuid.UUID
	Key, Title string
	Priority   string
	Total      int // свободных задач всего (до LIMIT)
}

// tasksCommand — /tasks [ключ проекта]: свободные задачи активных спринтов (todo, без
// исполнителя) с кнопкой «Взять» у каждой (§8.5). Не больше MaxFreeTasks на все проекты, самые
// приоритетные; в сообщении — с разбивкой по проектам, внутри проекта по приоритету. Задачи
// архивного проекта не предлагаются: взять их нельзя (§3.2).
func tasksCommand(db *gorm.DB, groups *Groups) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		ps, err := chatProjects(ctx, r, groups)
		if err != nil || ps == nil {
			return err
		}
		ids := make([]uuid.UUID, len(ps))
		for i, p := range ps {
			ids[i] = p.ID
		}
		var withSprint []uuid.UUID
		if err := db.WithContext(ctx).Raw(`SELECT project_id FROM sprints WHERE project_id IN ? AND state = 'active'`, ids).
			Scan(&withSprint).Error; err != nil {
			return fmt.Errorf("telegram: active sprints: %w", err)
		}
		var free []freeTask
		if err := db.WithContext(ctx).Raw(`SELECT t.id, t.project_id, t.key, t.title, t.priority, count(*) OVER () AS total
			FROM tasks t JOIN sprints s ON s.id = t.sprint_id AND s.state = 'active'
			JOIN projects p ON p.id = t.project_id AND NOT p.is_archived
			WHERE t.project_id IN ? AND t.status = 'todo' AND t.assignee_id IS NULL AND t.deleted_at IS NULL
			ORDER BY CASE t.priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, t.position, t.number
			LIMIT ?`, ids, MaxFreeTasks).Scan(&free).Error; err != nil {
			return fmt.Errorf("telegram: free tasks: %w", err)
		}

		type line struct{ Key, Title, Priority string }
		type group struct {
			Key, Name        string
			Archived, Sprint bool
			Tasks            []line
		}
		data := struct {
			Projects []group
			More     int
		}{}
		var kb [][]models.InlineKeyboardButton
		for _, p := range ps {
			g := group{Key: p.Key, Name: p.Name, Archived: p.IsArchived}
			for _, id := range withSprint {
				g.Sprint = g.Sprint || id == p.ID
			}
			for _, t := range free {
				if t.ProjectID != p.ID {
					continue
				}
				g.Tasks = append(g.Tasks, line{Key: t.Key, Title: clip(t.Title, freeTitle), Priority: priorityLabels[t.Priority]})
				kb = append(kb, []models.InlineKeyboardButton{{Text: Text("button_take_key", t.Key), CallbackData: ActionTake + ":" + t.ID.String()}})
			}
			data.Projects = append(data.Projects, g)
		}
		if len(free) > 0 {
			data.More = free[0].Total - len(free)
		}
		m := r.reply(clip(Text("cmd_tasks", data), maxMessage))
		if len(kb) > 0 {
			m.Keyboard = &models.InlineKeyboardMarkup{InlineKeyboard: kb}
		}
		return r.out.Send(ctx, m, 0)
	}
}
