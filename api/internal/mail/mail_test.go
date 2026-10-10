package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	netmail "net/mail"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/testutil"
)

func TestPasswordResetTemplate(t *testing.T) {
	m, err := PasswordReset("ivan@example.com", "Иван Петров", "https://tasks.example/password-set?token=abc")
	require.NoError(t, err)
	require.Equal(t, "ivan@example.com", m.To)
	require.Equal(t, "TaskFlow: восстановление пароля", m.Subject)
	require.Contains(t, m.Text, "Иван Петров")
	require.Contains(t, m.Text, "https://tasks.example/password-set?token=abc")
	require.Contains(t, m.Text, "24 часа")
}

func TestBuildEncodesHeadersAndBody(t *testing.T) {
	from := &netmail.Address{Name: "TaskFlow", Address: "noreply@tasks.example"}
	m := Message{To: "ivan@example.com", Subject: "Тема письма", Text: strings.Repeat("Привет, мир! ", 20)}
	raw, err := build(from, m, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	msg, err := netmail.ReadMessage(strings.NewReader(string(raw)))
	require.NoError(t, err)
	subj, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	require.NoError(t, err)
	require.Equal(t, "Тема письма", subj)
	require.Equal(t, "<ivan@example.com>", msg.Header.Get("To"))
	require.Contains(t, msg.Header.Get("Message-ID"), "@tasks.example>")
	require.Equal(t, "text/plain; charset=UTF-8", msg.Header.Get("Content-Type"))

	rawBody, err := io.ReadAll(msg.Body)
	require.NoError(t, err)
	var body strings.Builder
	for _, line := range strings.Split(strings.TrimSpace(string(rawBody)), "\r\n") {
		require.LessOrEqual(t, len(line), 76, "строки base64 не длиннее 76 символов")
		body.WriteString(line)
	}
	text, err := base64.StdEncoding.DecodeString(body.String())
	require.NoError(t, err)
	require.Equal(t, m.Text, string(text))
}

func TestBuildRejectsHeaderInjectionAndBadAddress(t *testing.T) {
	from := &netmail.Address{Address: "noreply@tasks.example"}
	_, err := build(from, Message{To: "a@example.com", Subject: "x\r\nBcc: evil@example.com"}, time.Now())
	require.ErrorIs(t, err, errHeaderInjection)
	_, err = build(from, Message{To: "not an address", Subject: "x"}, time.Now())
	require.Error(t, err)
}

func TestNewSMTPConfig(t *testing.T) {
	s, err := NewSMTP(&config.Config{})
	require.NoError(t, err)
	require.Nil(t, s, "без SMTP_HOST отправителя нет")
	_, err = NewSMTP(&config.Config{SMTPHost: "smtp.example", SMTPFrom: "не адрес"})
	require.Error(t, err)
}

type fakeSender struct{ err error }

func (f fakeSender) Send(context.Context, Message) error { return f.err }

func task(t *testing.T, m Message) *asynq.Task {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return asynq.NewTask(TaskType, b)
}

func TestHandler(t *testing.T) {
	m := Message{To: "a@example.com", Subject: "s", Text: "t"}
	require.NoError(t, Handler(nil)(context.Background(), task(t, m)), "без SMTP письмо пропускается без повтора")
	require.NoError(t, Handler(fakeSender{})(context.Background(), task(t, m)))

	failure := errors.New("smtp down")
	require.ErrorIs(t, Handler(fakeSender{err: failure})(context.Background(), task(t, m)), failure, "ошибка отправки уходит в asynq — он повторит")

	err := Handler(fakeSender{})(context.Background(), asynq.NewTask(TaskType, []byte("{")))
	require.ErrorIs(t, err, asynq.SkipRetry, "битое задание не повторяется")
}

// Критерий #10: письмо уходит через очередь с повторами.
func TestQueueEnqueuesWithRetries(t *testing.T) {
	addr := testutil.SharedRedisAddr(t)
	client := queue.NewClient(addr)
	t.Cleanup(func() { _ = client.Close() })

	m := Message{To: "a@example.com", Subject: "s", Text: "t"}
	require.NoError(t, NewQueue(client).Send(context.Background(), m))

	insp := asynq.NewInspector(queue.Opts(addr))
	t.Cleanup(func() { _ = insp.Close() })
	tasks, err := insp.ListPendingTasks(queue.QueueDefault)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, TaskType, tasks[0].Type)
	require.Equal(t, MaxRetry, tasks[0].MaxRetry)
	var got Message
	require.NoError(t, json.Unmarshal(tasks[0].Payload, &got))
	require.Equal(t, m, got)
}

// Критерий #10: при недоступном SMTP отправка возвращает ошибку (и asynq повторит задание).
func TestSMTPUnavailable(t *testing.T) {
	s, err := NewSMTP(&config.Config{SMTPHost: "127.0.0.1", SMTPPort: "1", SMTPFrom: "noreply@tasks.example"})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.Error(t, s.Send(ctx, Message{To: "a@example.com", Subject: "s", Text: "t"}))
}

// Настоящая отправка: SMTP-сервер mailpit в контейнере принимает письмо, его API отдаёт его обратно.
func TestSMTPSendsToRealServer(t *testing.T) {
	if testing.Short() {
		t.Skip("нужен docker")
	}
	ctx := context.Background()
	c, err := testcontainers.Run(ctx, "axllent/mailpit:v1.21.0",
		testcontainers.WithExposedPorts("1025/tcp", "8025/tcp"),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/livez").WithPort("8025/tcp").WithStartupTimeout(time.Minute)),
	)
	testcontainers.CleanupContainer(t, c)
	require.NoError(t, err)
	host, err := c.Host(ctx)
	require.NoError(t, err)
	smtpPort, err := c.MappedPort(ctx, "1025/tcp")
	require.NoError(t, err)
	apiPort, err := c.MappedPort(ctx, "8025/tcp")
	require.NoError(t, err)

	s, err := NewSMTP(&config.Config{SMTPHost: host, SMTPPort: smtpPort.Port(), SMTPFrom: "TaskFlow <noreply@tasks.example>"})
	require.NoError(t, err)
	m, err := PasswordReset("ivan@example.com", "Иван Петров", "https://tasks.example/password-set?token=abc")
	require.NoError(t, err)
	require.NoError(t, s.Send(ctx, m))

	api := fmt.Sprintf("http://%s:%s/api/v1", host, apiPort.Port())
	var list struct {
		Messages []struct {
			ID      string
			Subject string
			To      []struct{ Address string }
		}
	}
	getJSON(t, api+"/messages", &list)
	require.Len(t, list.Messages, 1)
	require.Equal(t, m.Subject, list.Messages[0].Subject)
	require.Equal(t, "ivan@example.com", list.Messages[0].To[0].Address)

	var full struct{ Text string }
	getJSON(t, api+"/message/"+list.Messages[0].ID, &full)
	require.Contains(t, full.Text, "https://tasks.example/password-set?token=abc")
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec,noctx // адрес тестового контейнера
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(v))
}
