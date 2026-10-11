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
