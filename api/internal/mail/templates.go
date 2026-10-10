package mail

import (
	"bytes"
	"fmt"
	"text/template"
)

// Все тексты писем — здесь, в одном файле (ТЗ §10.2, как у бота §8.8). Тексты не зависят
// от рода получателя: обращение по имени без согласования.
var templates = template.Must(template.New("mail").Parse(`
{{define "password_set.subject"}}TaskFlow: приглашение и установка пароля{{end}}
{{define "password_set.text"}}Здравствуйте, {{.Name}}!

Для вас создана учётная запись в TaskFlow, логин — {{.Email}}.
Чтобы войти, задайте пароль по ссылке:

{{.Link}}

Ссылка действует 24 часа и срабатывает один раз. Если она истекла, воспользуйтесь
восстановлением пароля на странице входа.
{{end}}
{{define "password_reset.subject"}}TaskFlow: восстановление пароля{{end}}
{{define "password_reset.text"}}Здравствуйте, {{.Name}}!

Для вашей учётной записи в TaskFlow запрошено восстановление пароля.
Задать новый пароль можно по ссылке:

{{.Link}}

Ссылка действует 24 часа и срабатывает один раз.
Если вы не запрашивали восстановление, просто удалите это письмо — пароль останется прежним.
{{end}}
`))

// PasswordSet — приглашение новому пользователю: ссылка установки пароля (ТЗ §7.1).
func PasswordSet(to, name, link string) (Message, error) {
	return render(to, "password_set", map[string]string{"Name": name, "Email": to, "Link": link})
}

// PasswordReset — письмо со ссылкой восстановления пароля (ТЗ §7.1).
func PasswordReset(to, name, link string) (Message, error) {
	return render(to, "password_reset", map[string]string{"Name": name, "Link": link})
}

func render(to, name string, data any) (Message, error) {
	var subj, text bytes.Buffer
	if err := templates.ExecuteTemplate(&subj, name+".subject", data); err != nil {
		return Message{}, fmt.Errorf("mail: шаблон %s: %w", name, err)
	}
	if err := templates.ExecuteTemplate(&text, name+".text", data); err != nil {
		return Message{}, fmt.Errorf("mail: шаблон %s: %w", name, err)
	}
	return Message{To: to, Subject: subj.String(), Text: text.String()}, nil
}
