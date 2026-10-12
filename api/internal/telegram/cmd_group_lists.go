package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/sprint"
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
	sp, err := sprint.ActiveOf(db, projectID)
	if err != nil || sp == nil {
		return nil, err
	}
	st, err := task.SprintTotals(db, sp.ID)
	if err != nil {
		return nil, err
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
	s := &sprintSummary{Name: sp.Name, Statuses: strings.Join(parts, " · "),
		Spent: duration.Format(spent), Estimate: duration.Format(estimate)}
	if start, end := sp.StartDate, sp.EndDate; start != nil && end != nil {
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
	args := strings.Fields(r.Args)
	if len(args) == 0 {
		return ps, nil
	}
	key := strings.ToUpper(args[0]) // остальные слова не нужны
	for _, p := range ps {
		if p.Key == key {
			return []project.Project{p}, nil
		}
	}
	return nil, r.Reply(ctx, Text("unbind_not_here", clip(key, 20)))
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

// freeLine и freeGroup — данные шаблона cmd_tasks.
type freeLine struct{ Key, Title, Priority string }

type freeGroup struct {
	Key, Name        string
	Archived, Sprint bool
	Tasks            []freeLine
	More             int // свободных задач проекта, не попавших в список
}

// tasksCommand — /tasks [ключ проекта]: свободные задачи активных спринтов (todo, без
// исполнителя) с кнопкой «Взять» у каждой (§8.5). Не больше MaxFreeTasks на все проекты, самые
// приоритетные; в сообщении — с разбивкой по проектам, внутри проекта по приоритету, у проекта —
// сколько его задач не поместилось. Задачи архивного проекта не предлагаются: взять их нельзя (§3.2).
func tasksCommand(db *gorm.DB, groups *Groups) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		ps, err := chatProjects(ctx, r, groups)
		if err != nil || ps == nil {
			return err
		}
		qdb := db.WithContext(ctx)
		ids := make([]uuid.UUID, len(ps))
		archived := map[uuid.UUID]bool{}
		for i, p := range ps {
			ids[i], archived[p.ID] = p.ID, p.IsArchived
		}
		// Проекты с активным спринтом и число свободных задач в каждом; брать можно только задачи
		// неархивных проектов (§3.2).
		active, err := sprint.ActiveIn(qdb, ids)
		if err != nil {
			return err
		}
		freeCount := map[uuid.UUID]int{}
		var free []task.FreeTask
		if len(active) > 0 {
			var all, open []uuid.UUID
			for _, sp := range active {
				if all = append(all, sp.ID); !archived[sp.ProjectID] {
					open = append(open, sp.ID)
				}
			}
			bySprint, err := task.FreeCounts(qdb, all)
			if err != nil {
				return err
			}
			for _, sp := range active {
				freeCount[sp.ProjectID] += bySprint[sp.ID]
			}
			if len(open) > 0 {
				if free, err = task.FreeInSprints(qdb, open, MaxFreeTasks); err != nil {
					return err
				}
			}
		}

		data := struct{ Projects []freeGroup }{}
		for _, p := range ps {
			n, sprint := freeCount[p.ID]
			g := freeGroup{Key: p.Key, Name: p.Name, Archived: p.IsArchived, Sprint: sprint}
			if !p.IsArchived {
				g.More = n
			}
			data.Projects = append(data.Projects, g)
		}
		// Задачи добавляются, пока текст влезает в сообщение: кнопки — только у показанных.
		var kb [][]models.InlineKeyboardButton
		for gi := range data.Projects {
			g := &data.Projects[gi]
			for _, t := range free {
				if t.ProjectID != ps[gi].ID {
					continue
				}
				g.Tasks, g.More = append(g.Tasks, freeLine{Key: t.Key, Title: clip(t.Title, freeTitle), Priority: priorityLabels[t.Priority]}), g.More-1
				if len([]rune(Text("cmd_tasks", data))) > maxMessage {
					g.Tasks, g.More = g.Tasks[:len(g.Tasks)-1], g.More+1
					continue
				}
				kb = append(kb, []models.InlineKeyboardButton{{Text: Text("button_take_key", t.Key), CallbackData: ActionTake + ":" + t.ID.String()}})
			}
		}
		m := r.reply(Text("cmd_tasks", data))
		if len(kb) > 0 {
			m.Keyboard = &models.InlineKeyboardMarkup{InlineKeyboard: kb}
		}
		return r.out.Send(ctx, m, 0)
	}
}
