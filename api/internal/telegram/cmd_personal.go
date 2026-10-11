package telegram

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/task"
)

// MaxMyTasks — /my показывает не больше стольких задач (§8.5).
const MaxMyTasks = 30

// myTitleRunes — длина названия в строке /my: 30 строк с названиями до 200 символов (§3.3) не
// влезли бы в одно сообщение Telegram (4096 символов).
const myTitleRunes = 60

// RegisterPrivateCommands подключает личные команды §8.5: /timer, /stop, /my. Они работают только
// в личном чате; права и правила таймера (§6.4) проверяет task.Service, как и для веба.
func RegisterPrivateCommands(rt *Router, tasks *task.Service) {
	rt.Command("timer", privateOnly(timerCommand(tasks)))
	rt.Command("stop", privateOnly(stopCommand(tasks)))
	rt.Command("my", privateOnly(myCommand(tasks)))
}

// privateOnly — команда только для личного чата; в группе — подсказка временным ответом.
func privateOnly(h HandlerFunc) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		if !r.Private() {
			return r.Answer(ctx, Text("private_only", nil), true)
		}
		return h(ctx, r)
	}
}

// timerCommand — /timer <ключ>: запустить таймер (§6.4). Таймер на другой задаче
// останавливается сервисом с записью времени — об этом первая строка ответа.
func timerCommand(tasks *task.Service) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		// Ключ проекта в личном чате обязателен (§8.5), регистр не важен.
		key := strings.ToUpper(strings.TrimSpace(r.Args))
		if !task.ValidKey(key) {
			return r.Reply(ctx, Text("cmd_timer_usage", nil))
		}
		res, err := tasks.StartTimer(ctx, r.User, key)
		if err != nil {
			return replyServiceError(ctx, r, key, err)
		}
		text := Text("cmd_timer_started", res.Timer)
		if res.Stopped != nil {
			text = Text("cmd_timer_stopped", res.Stopped) + "\n" + text
		}
		return r.Reply(ctx, text)
	}
}

// stopCommand — /stop: остановить свой таймер с записью времени (§6.4).
func stopCommand(tasks *task.Service) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		res, err := tasks.StopTimer(ctx, r.User)
		if errors.Is(err, task.ErrNoTimer) {
			return r.Reply(ctx, Text("cmd_no_timer", nil))
		}
		if err != nil {
			return replyServiceError(ctx, r, "", err)
		}
		return r.Reply(ctx, Text("cmd_timer_stopped", res))
	}
}

// myItem — строка списка /my.
type myItem struct {
	task.MyTask
	Title    string // название, укороченное до myTitleRunes
	Status   string
	Spent    string
	Estimate string
}

// myCommand — /my: открытые задачи пользователя по всем проектам (§8.5), не больше MaxMyTasks;
// порядок — как в блоке «Мои задачи» веба: по сроку, просроченные сверху.
func myCommand(tasks *task.Service) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		list, err := tasks.Mine(ctx, r.User)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return r.Reply(ctx, Text("cmd_my_empty", nil))
		}
		data := struct {
			Items []myItem
			Total string
			More  bool
		}{Total: strconv.Itoa(len(list)), More: len(list) > MaxMyTasks}
		if len(list) >= task.MaxMine {
			data.Total += "+" // Mine отдаёт не больше MaxMine задач
		}
		for _, t := range list[:min(len(list), MaxMyTasks)] {
			it := myItem{MyTask: t, Title: shorten(t.Title, myTitleRunes), Status: Text("cmd_status_"+t.Status, nil), Spent: duration.Format(t.SpentMinutes)}
			if t.EstimateMinutes != nil {
				it.Estimate = duration.Format(*t.EstimateMinutes)
			}
			data.Items = append(data.Items, it)
		}
		return r.Reply(ctx, Text("cmd_my", data))
	}
}

// shorten обрезает s до n символов с многоточием.
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// replyServiceError переводит ошибку сервиса (§9.3) в ответ бота. key — задача команды (пусто,
// если команда без задачи). Ошибки 4xx без своего текста показываются сообщением сервиса — оно
// написано для пользователя; остальное (500) уходит в лог, пользователь получает internal_error.
func replyServiceError(ctx context.Context, r *Request, key string, err error) error {
	var e *apierr.Error
	if !errors.As(err, &e) || e.Status >= http.StatusInternalServerError {
		return err
	}
	data := map[string]string{"Key": key, "Message": e.Message}
	name := "cmd_error"
	switch {
	case e.Code == apierr.CodeProjectArchived:
		// «Проект TM в архиве» (§8.5): ключ проекта — часть ключа задачи до дефиса.
		name = "cmd_project_archived"
		data["Project"], _, _ = strings.Cut(key, "-")
	case e.Status == http.StatusNotFound && key != "":
		name = "cmd_task_not_found"
	case e.Status == http.StatusForbidden && r.Command == "timer":
		name = "cmd_timer_not_assignee"
	case e.Code == apierr.CodeInvalidTransition && r.Command == "timer":
		name = "cmd_timer_not_in_progress"
	}
	return r.Reply(ctx, Text(name, data))
}
