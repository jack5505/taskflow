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

{{- /* Создание задачи /new, §8.5 */}}
{{define "cmd_new_usage"}}Формат: /new [ключ проекта] <название>{{end}}
{{define "cmd_new_created"}}Создана задача {{.Key}} «{{.Title}}»{{end}}
{{define "cmd_new_not_member"}}Вы не участник проекта {{.}}{{end}}
{{define "cmd_new_no_projects"}}{{if .}}У вас нет активных проектов{{else}}Все проекты группы в архиве{{end}}{{end}}
{{define "cmd_new_title_invalid"}}Название задачи — от 1 до 200 символов{{end}}
{{define "cmd_new_choose"}}{{if .Private}}Укажите проект: /new {{index .Keys 0}} <название>. Ваши проекты: {{else}}К группе подключено несколько проектов, укажите ключ: /new {{index .Keys 0}} <название>. Проекты группы: {{end}}
{{- range $i, $k := .Keys}}{{if $i}}, {{end}}{{$k}}{{end}}{{end}}

{{- /* /help, меню бота, неизвестные команды, архивный проект, §8.5 */}}
{{define "cmd_help_private"}}Команды в личном чате:{{range .Commands}}
/{{.Name}}{{if .Args}} {{.Args}}{{end}} — {{.Description}}{{end}}
{{- if .Keyed}}
Ключ задачи пишется вместе с ключом проекта: TM-42.{{end}}{{end}}
{{define "cmd_help_group"}}Команды в группе:{{range .Commands}}
/{{.Name}}{{if .Args}} {{.Args}}{{end}} — {{.Description}}{{end}}
{{- if .Keyed}}
Если к группе подключён один проект, ключ проекта можно не писать: TM-42 или 42.{{end}}{{end}}
{{define "cmd_unknown"}}Неизвестная команда. /help{{end}}
{{define "cmd_project_archived"}}Проект {{with .Project}}{{.}} {{end}}в архиве, изменения недоступны{{end}}
{{define "cmd_menu_start"}}Привязать аккаунт по коду из профиля{{end}}
{{define "cmd_menu_unlink"}}Отвязать аккаунт{{end}}
{{define "cmd_menu_bind"}}Подключить группу к проекту{{end}}
{{define "cmd_menu_unbind"}}Отключить группу от проекта{{end}}
{{define "cmd_menu_take"}}Взять задачу в работу{{end}}
{{define "cmd_menu_drop"}}Вернуть свою задачу в очередь{{end}}
{{define "cmd_menu_review"}}Отправить свою задачу на проверку{{end}}
{{define "cmd_menu_done"}}Принять задачу{{end}}
{{define "cmd_menu_log"}}Списать время на сегодня{{end}}
{{define "cmd_menu_timer"}}Запустить таймер{{end}}
{{define "cmd_menu_stop"}}Остановить таймер{{end}}
{{define "cmd_menu_my"}}Мои открытые задачи{{end}}
{{define "cmd_menu_tasks"}}Свободные задачи активного спринта{{end}}
{{define "cmd_menu_sprint"}}Сводка активного спринта{{end}}
{{define "cmd_menu_task"}}Карточка задачи{{end}}
{{define "cmd_menu_new"}}Создать задачу{{end}}
{{define "cmd_menu_comment"}}Добавить комментарий к задаче{{end}}
{{define "cmd_menu_help"}}Список команд{{end}}

{{- /* Подключение группы к проекту, §8.2 */}}
{{define "group_only"}}Эта команда работает в группе проекта{{end}}
{{define "bind_usage"}}Формат: /bind <код>. Код выдаётся в настройках проекта → Telegram{{end}}
{{define "bind_code_invalid"}}Код не найден или устарел. Получите новый в настройках проекта → Telegram{{end}}
{{define "bind_not_manager"}}Подключать и отключать группу может только менеджер проекта{{end}}
{{define "bind_has_group"}}К проекту {{.Key}} уже подключена другая группа. Сначала отключите её: /unbind в той группе или кнопка в настройках проекта{{end}}
{{define "project_archived"}}Проект {{.Key}} в архиве, изменения недоступны{{end}}
{{define "group_bound"}}Группа подключена к проекту {{.Key}} «{{.Name}}». Команды: /help{{end}}
{{define "group_unbound"}}Группа отключена от проекта {{.Key}} «{{.Name}}»{{end}}
{{define "group_no_projects"}}К этой группе не подключён ни один проект{{end}}
{{define "unbind_choose"}}К группе подключено несколько проектов: {{range $i, $p := .}}{{if $i}}, {{end}}{{$p.Key}}{{end}}. Укажите ключ: /unbind {{(index . 0).Key}}{{end}}
{{define "unbind_not_here"}}Проект {{.}} не подключён к этой группе{{end}}
{{define "group_bot_removed"}}Бота удалили из группы{{with .Title}} «{{.}}»{{end}}, Telegram-группа отключена от {{if gt (len .Projects) 1}}проектов{{else}}проекта{{end}}:
{{range .Projects}}{{.Key}} «{{.Name}}»
{{end}}Подключить группу снова можно в настройках проекта → Telegram{{end}}

{{- /* Карточка задачи в группе, §8.3 (HTML Telegram: данные экранируются функцией html) */}}
{{define "card"}}🆕 Новая задача {{html .Key}} · {{.Type}} · Приоритет: {{.Priority}}
<b>{{html .Title}}</b>

Автор: {{html .Reporter}}
{{with .Details}}{{range $i, $d := .}}{{if $i}} · {{end}}{{html $d}}{{end}}
{{end}}Статус: {{.Status}} · Исполнитель: {{html .Assignee}}{{end}}
{{define "card_deleted"}}🗑 Задача удалена: {{html .Key}} «{{html .Title}}»{{end}}

{{- /* Сообщения в группу по событиям, §8.6 (без разметки) */}}
{{define "ev_taken"}}🛠 {{.Key}} «{{.Title}}» взята в работу — {{.Assignee}}{{end}}
{{define "ev_assigned"}}📌 {{.Key}} «{{.Title}}»: исполнитель {{.Assignee}} — назначение: {{.Actor}}{{end}}
{{define "ev_returned"}}↩️ {{.Key}} «{{.Title}}» возвращена в очередь — {{.Actor}}{{end}}
{{define "ev_review"}}🔍 {{.Key}} «{{.Title}}» отправлена на проверку — {{.Actor}}{{end}}
{{define "ev_rework"}}🔁 {{.Key}} «{{.Title}}» возвращена на доработку — {{.Actor}}{{with .Comment}}: {{.}}{{end}}{{end}}
{{define "ev_accepted"}}✅ {{.Key}} «{{.Title}}» принята — {{.Actor}}. Списано {{.Spent}}{{end}}
{{define "ev_cancelled"}}❌ {{.Key}} «{{.Title}}» отменена — {{.Actor}}{{with .Comment}}: {{.}}{{end}}{{end}}
{{define "ev_comment"}}💬 {{.Author}} к {{.Key}}: {{.Body}}{{end}}
{{define "ev_sprint_started"}}🚀 {{.Name}} запущен: {{.Dates}}, {{.Tasks}}, оценка {{.Estimate}}
{{- with .Free}}

Свободные задачи:{{range .}}
{{.Key}} «{{.Title}}»{{end}}{{end}}{{if .More}}
…и другие: /tasks{{end}}{{end}}
{{define "ev_sprint_closed"}}🏁 {{.Name}} завершён: готово {{.Done}} из {{.Total}}, перенесено {{.Moved}}, списано {{.Spent}}{{end}}
{{define "button_take_key"}}Взять {{.}}{{end}}
{{- /* Личные уведомления, §8.7. Обычный текст без разметки. */}}
{{define "p_assigned"}}📌 Вас назначили исполнителем {{.Key}} «{{.Title}}» — {{.By}}{{end}}
{{define "p_rework"}}🔁 {{.Key}} «{{.Title}}» возвращена на доработку — {{.By}}: {{.Comment}}{{end}}
{{define "p_accepted"}}✅ {{.Key}} «{{.Title}}» принята — {{.By}}{{end}}
{{define "p_cancelled"}}❌ {{.Key}} «{{.Title}}» отменена — {{.By}}: {{.Comment}}{{end}}
{{define "p_comment"}}💬 {{.By}} к {{.Key}} «{{.Title}}»: {{.Body}}{{end}}
{{define "p_review"}}🔍 {{.Key}} «{{.Title}}» отправлена на проверку — {{.By}}{{end}}
{{define "p_timer_stopped"}}⏹ Таймер на {{.Key}} «{{.Title}}» остановлен автоматически через 12 часов, списано {{.Spent}}{{end}}
{{define "p_worklog_trimmed"}}✂️ Запись таймера на {{.Key}} «{{.Title}}» за {{.Date}} обрезана по дневному лимиту 24 часа: списано {{.Spent}} из {{.Requested}}{{end}}
{{define "p_task_deleted"}}🗑 {{.Key}} «{{.Title}}» удалена — {{.By}}{{end}}
{{define "p_member_added"}}👥 Вас добавили в проект {{.Key}} «{{.Name}}», роль: {{.Role}} — {{.By}}{{end}}
{{define "p_member_removed"}}👥 Вас исключили из проекта {{.Key}} «{{.Name}}» — {{.By}}{{end}}
{{define "p_member_role"}}👥 Ваша роль в проекте {{.Key}} «{{.Name}}» изменена: {{.OldRole}} → {{.Role}} — {{.By}}{{end}}
{{define "role_manager"}}менеджер{{end}}
{{define "role_member"}}участник{{end}}
{{define "btn_take"}}Взять в работу{{end}}
{{define "btn_open"}}Открыть{{end}}
{{define "btn_accept"}}Принять{{end}}
{{define "btn_rework"}}Вернуть{{end}}
{{define "p_rework_prompt"}}Напишите следующим сообщением причину возврата {{.Key}} на доработку (не короче 5 символов){{end}}
{{define "p_rework_short"}}Причина слишком короткая: нужно не меньше 5 символов. Напишите её ещё раз{{end}}
{{define "p_accept_done"}}{{.Key}} принята{{end}}
{{define "p_rework_done"}}{{.Key}} возвращена на доработку{{end}}
{{define "p_mark_accepted"}}✅ Принята{{end}}
{{define "p_mark_reworked"}}🔁 Возвращена на доработку{{end}}
{{define "p_not_in_review"}}Задача {{.Key}} уже не на проверке, статус: {{.Status}}{{end}}
{{define "p_rework_forbidden"}}Вернуть на доработку может автор задачи или менеджер проекта{{end}}
{{define "p_task_not_found"}}Задача не найдена{{end}}
{{define "p_archived"}}Проект {{.Key}} в архиве, изменения недоступны{{end}}

{{- /* Сводки группы /tasks и /sprint, §8.5; блок спринта — общий с ежедневной сводкой */}}
{{define "sprint_block"}}{{with .}}🏃 {{.Name}}{{if .Dates}}: {{.Dates}}{{end}}
{{.Statuses}}
Списано {{.Spent}} из {{.Estimate}}{{else}}Нет активного спринта{{end}}{{end}}
{{define "cmd_sprint"}}{{range $i, $p := .}}{{if $i}}

{{end}}📊 {{$p.Key}} «{{$p.Name}}»
{{template "sprint_block" $p.Sprint}}{{end}}{{end}}
{{define "cmd_tasks"}}{{range $i, $p := .Projects}}{{if $i}}

{{end}}📋 Свободные задачи {{$p.Key}} «{{$p.Name}}»
{{- if $p.Archived}}: проект в архиве
{{- else if not $p.Sprint}}: нет активного спринта
{{- else if not $p.Tasks}}: нет
{{- else}}:{{range $p.Tasks}}
{{.Key}} «{{.Title}}» · {{.Priority}}{{end}}{{end}}{{end}}
{{- if .More}}

…и ещё {{.More}}{{end}}{{end}}

{{- /* По расписанию: напоминания о сроке (§8.7) и ежедневная сводка в группу (§8.6). */}}
{{define "p_due_tomorrow"}}⏰ Срок {{.Key}} «{{.Title}}» завтра, {{.Date}}{{end}}
{{define "p_overdue"}}⚠️ Срок {{.Key}} «{{.Title}}» просрочен: {{.Date}}{{end}}
{{define "daily_summary"}}📊 Сводка {{.Key}} «{{.Name}}» на {{.Date}}

{{template "sprint_block" .Sprint}}
{{if .Overdue}}
⚠️ Просрочено:
{{- range .Overdue}}
{{.Key}} «{{.Title}}» — {{.Assignee}}, срок {{.Due}}
{{- end}}
{{- if .More}}
и ещё {{.More}}
{{- end}}
{{- else}}
Просроченных задач нет
{{- end}}{{end}}
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

// Названия перечислений задачи — как в веб-интерфейсе (§3.3, §4).
var (
	typeLabels     = map[string]string{"task": "Задача", "bug": "Ошибка", "feature": "Новая функция"}
	priorityLabels = map[string]string{"low": "Низкий", "medium": "Средний", "high": "Высокий", "critical": "Критический"}
	statusLabels   = map[string]string{"todo": "К выполнению", "in_progress": "В работе", "review": "На проверке",
		"done": "Готово", "cancelled": "Отменена"}
)
