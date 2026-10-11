package telegram

import (
	"bytes"
	"log/slog"
	"text/template"
)

// Все тексты бота — здесь, в одном файле (ТЗ §8.8). Тексты не зависят от рода пользователя:
// имя ставится после тире, глагол согласуется с задачей («TM-42 принята — Анна Смирнова»).
// Сообщения с разметкой HTML экранируют данные пользователя функцией html.
var texts = template.Must(template.New("bot").Parse(`
{{define "internal_error"}}Не получилось выполнить действие. Попробуйте ещё раз{{end}}
{{define "private_only"}}Эта команда работает в личном чате с ботом{{end}}

{{- /* Привязка аккаунта, §8.1 */}}
{{define "start"}}Здравствуйте! Это бот TaskFlow.
Чтобы привязать аккаунт, откройте профиль в веб-интерфейсе, нажмите «Привязать Telegram» и перейдите по ссылке или отправьте сюда /start <код>.{{end}}
{{define "linked"}}Аккаунт привязан: {{.DisplayName}} ({{.Email}}){{end}}
{{define "code_invalid"}}Код не найден или устарел. Сгенерируйте новый в профиле{{end}}
{{define "too_many_codes"}}Слишком много неверных кодов. Попробуйте через час{{end}}
{{define "linked_to_other"}}Этот Telegram уже привязан к другому аккаунту. Сначала отвяжите его командой /unlink{{end}}
{{define "unlinked"}}Аккаунт отвязан{{end}}
{{define "not_linked"}}Сначала привяжите аккаунт: профиль → Привязать Telegram{{end}}

{{- /* Личные команды /timer, /stop, /my, §8.5. Длительность — в формате §6.1 (duration.Format). */}}
{{define "cmd_timer_usage"}}Формат: /timer TM-42{{end}}
{{define "cmd_timer_started"}}Таймер запущен на {{.TaskKey}}{{end}}
{{define "cmd_timer_stopped"}}
{{- if .Minutes}}Таймер остановлен: {{.Duration}} списано на {{.TaskKey}}
{{- else}}Таймер на {{.TaskKey}} остановлен: меньше минуты, время не списано{{end}}
{{- if .AutoStopped}}. Таймер шёл больше 12 часов — списано 12 часов{{end}}
{{- if .Trimmed}}. Запись обрезана по дневному лимиту 24 часа{{end}}{{end}}
{{define "cmd_no_timer"}}Таймер не запущен{{end}}
{{define "cmd_timer_not_assignee"}}Таймер на {{.Key}} может запустить только исполнитель задачи{{end}}
{{define "cmd_timer_not_in_progress"}}Таймер запускается только на задаче в работе, а {{.Key}} не в работе{{end}}
{{define "cmd_task_not_found"}}Задача {{.Key}} не найдена{{end}}
{{define "cmd_error"}}{{.Message}}{{end}}
{{define "cmd_my_empty"}}Открытых задач нет{{end}}
{{define "cmd_my"}}Ваши открытые задачи{{if .More}} (первые {{len .Items}} из {{.Total}}){{end}}:{{range .Items}}
{{.Key}} «{{.Title}}» · {{.Status}} · {{.Spent}}{{if .Estimate}} из {{.Estimate}}{{end}}{{if .Overdue}} · просрочена{{end}}{{end}}{{end}}
{{define "cmd_status_todo"}}К выполнению{{end}}
{{define "cmd_status_in_progress"}}В работе{{end}}
{{define "cmd_status_review"}}На проверке{{end}}
{{define "cmd_status_done"}}Готово{{end}}
{{define "cmd_status_cancelled"}}Отменена{{end}}
`))

// Text — текст по имени шаблона. Ошибка шаблона — ошибка кода; она пишется в лог,
// а пользователь получает общий ответ.
func Text(name string, data any) string {
	var b bytes.Buffer
	if err := texts.ExecuteTemplate(&b, name, data); err != nil {
		slog.Error("telegram: шаблон", "name", name, "err", err)
		b.Reset()
		_ = texts.ExecuteTemplate(&b, "internal_error", nil) // статический текст, не падает
	}
	return b.String()
}
