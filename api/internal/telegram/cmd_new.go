package telegram

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/go-telegram/bot/models"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// projectKeyRe — слово, похожее на ключ проекта (§3.2), после перевода в верхний регистр.
var projectKeyRe = regexp.MustCompile(`^[A-Z]{2,10}$`)

// RegisterNewTask подключает /new [ключ проекта] <название> (§8.5): создание задачи из группы
// и из личного чата. Права и правила создания — task.Service.Create, как в вебе.
func RegisterNewTask(rt *Router, projects *project.Service, tasks *task.Service, groups *Groups, baseURL string) {
	rt.Command("new", newTaskCommand(projects, tasks, groups, strings.TrimRight(baseURL, "/")))
}

// newTaskCommand — /new: тип task, приоритет medium, в бэклоге, автор — отправитель (умолчания
// task.CreateInput). Успех в группе — карточка §8.3 (её публикует событие task.created);
// в личном чате — «Создана задача TM-43 «…»» с кнопкой «Открыть».
func newTaskCommand(projects *project.Service, tasks *task.Service, groups *Groups, baseURL string) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		key, title, err := newTaskProject(ctx, r, projects, groups)
		if err != nil || key == "" {
			return err // подсказка уже отправлена
		}
		if title == "" { // «/new TM» без названия
			return r.Reply(ctx, Text("cmd_new_usage", nil))
		}
		a, err := projects.Access(ctx, r.User, key)
		if errors.Is(err, project.ErrNotFound) {
			return r.Reply(ctx, Text("cmd_new_not_member", key))
		}
		if err != nil {
			return err
		}
		t, err := tasks.Create(ctx, a, r.User, task.CreateInput{Title: title})
		if errors.Is(err, project.ErrArchived) {
			return r.Reply(ctx, Text("project_archived", a.Project))
		}
		if err != nil {
			return replyServiceError(ctx, r, "", err)
		}
		// В группе карточку публикует событие «Создана задача», если оно включено (§8.6);
		// выключено — отвечаем как в личном чате, чтобы команда не осталась без ответа.
		if !r.Private() && project.EffectiveNotifications(a.Project.NotificationSettings)[project.NotifyTaskCreated] {
			return nil
		}
		m := r.reply(Text("cmd_new_created", t))
		m.Keyboard = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: Text("btn_open", nil), URL: baseURL + "/tasks/" + t.Key},
		}}}
		return r.out.Send(ctx, m, 0)
	}
}

// newTaskProject выбирает проект /new и отделяет название (§8.5). Первое слово — ключ проекта,
// только если совпадает (без учёта регистра) с проектом, доступным в этом чате: в группе —
// подключённым к ней, в личном чате — проектом пользователя. Без ключа: в группе с одним
// проектом — он, в личном чате — единственный активный проект пользователя; иначе бот отвечает
// подсказкой со списком проектов и key пустой.
func newTaskProject(ctx context.Context, r *Request, projects *project.Service, groups *Groups) (key, title string, err error) {
	args := strings.TrimSpace(r.Args)
	first, rest := args, ""
	if i := strings.IndexFunc(args, unicode.IsSpace); i >= 0 {
		first, rest = args[:i], strings.TrimSpace(args[i:])
	}
	first = strings.ToUpper(first)

	var keys []string
	if r.Private() {
		// Ключ проверяется доступом: проект пользователя, в том числе архивный (ответ об архиве).
		if projectKeyRe.MatchString(first) {
			if _, err := projects.Access(ctx, r.User, first); err == nil {
				return first, rest, nil // пустое название — подсказка о формате
			} else if !errors.Is(err, project.ErrNotFound) {
				return "", "", err
			}
		}
		list, err := projects.List(ctx, r.User, project.ListFilter{})
		if err != nil {
			return "", "", err
		}
		for _, p := range list {
			keys = append(keys, p.Key)
		}
	} else {
		ps, err := groups.ChatProjects(ctx, r.Chat.ID)
		if err != nil {
			return "", "", err
		}
		if len(ps) == 0 {
			return "", "", r.Reply(ctx, Text("group_no_projects", nil))
		}
		for _, p := range ps {
			if p.Key == first {
				return first, rest, nil
			}
			keys = append(keys, p.Key)
		}
	}
	switch {
	case args == "":
		return "", "", r.Reply(ctx, Text("cmd_new_usage", nil))
	case len(keys) == 1:
		return keys[0], args, nil
	case len(keys) == 0:
		return "", "", r.Reply(ctx, Text("cmd_new_no_projects", nil))
	}
	return "", "", r.Reply(ctx, Text("cmd_new_choose", map[string]any{"Private": r.Private(), "Keys": keys}))
}
