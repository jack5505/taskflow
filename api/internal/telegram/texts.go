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
