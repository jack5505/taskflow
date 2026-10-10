// Package mail — письма (ТЗ §10.2): API кладёт письмо в очередь asynq, воркер отправляет
// его через внешний SMTP; при недоступном SMTP asynq повторяет задание.
package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	netmail "net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/queue"
)

// TaskType — тип задания asynq для письма.
const TaskType = "email:send"

// MaxRetry — сколько раз asynq повторит письмо при недоступном SMTP (с растущей паузой, это часы).
const MaxRetry = 10

// Message — готовое письмо: адресат, тема и текст.
type Message struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

// Sender отправляет письмо. В API это очередь (Queue), в воркере — SMTP.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Queue ставит письма в очередь asynq; отправит их воркер.
type Queue struct{ client *asynq.Client }

func NewQueue(client *asynq.Client) *Queue { return &Queue{client: client} }

func (q *Queue) Send(ctx context.Context, m Message) error {
	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("mail: payload: %w", err)
	}
	_, err = q.client.EnqueueContext(ctx, asynq.NewTask(TaskType, payload),
		asynq.Queue(queue.QueueDefault), asynq.MaxRetry(MaxRetry), asynq.Timeout(time.Minute))
	if err != nil {
		return fmt.Errorf("mail: enqueue: %w", err)
	}
	return nil
}

// Handler — обработчик задания email:send в воркере. Ошибка отправки возвращается asynq,
// и он повторит задание. s == nil — SMTP не настроен: письмо пишется в лог без текста
// (в тексте одноразовые ссылки) и не повторяется.
func Handler(s Sender) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var m Message
		if err := json.Unmarshal(t.Payload(), &m); err != nil {
			return fmt.Errorf("mail: payload: %w: %w", err, asynq.SkipRetry)
		}
		if s == nil {
			slog.Warn("mail: SMTP не настроен, письмо не отправлено", "to", m.To, "subject", m.Subject)
			return nil
		}
		if err := s.Send(ctx, m); err != nil {
			return err
		}
		slog.Info("mail: sent", "to", m.To, "subject", m.Subject)
		return nil
	}
}

// SMTP отправляет письма через внешний SMTP-сервер. Порт 465 — TLS сразу,
// остальные — STARTTLS, если сервер его предлагает.
type SMTP struct {
	host, port string
	user, pass string
	from       *netmail.Address
}

// NewSMTP возвращает отправителя по настройкам SMTP_* или nil, если SMTP_HOST не задан.
func NewSMTP(cfg *config.Config) (Sender, error) {
	if cfg.SMTPHost == "" {
		return nil, nil
	}
	from, err := netmail.ParseAddress(cfg.SMTPFrom)
	if err != nil {
		return nil, fmt.Errorf("SMTP_FROM: %w", err)
	}
	return &SMTP{host: cfg.SMTPHost, port: cfg.SMTPPort, user: cfg.SMTPUser, pass: cfg.SMTPPassword, from: from}, nil
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	body, err := build(s.from, m, time.Now())
	if err != nil {
		return err
	}
	conn, err := s.dial(ctx)
	if err != nil {
		return fmt.Errorf("mail: smtp connect: %w", err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Minute)
	}
	_ = conn.SetDeadline(deadline)

	c, err := smtp.NewClient(conn, s.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: smtp greeting: %w", err)
	}
	defer func() { _ = c.Close() }()

	if ok, _ := c.Extension("STARTTLS"); ok && s.port != "465" {
		if err := c.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if s.user != "" {
		// PlainAuth сам откажется слать пароль по незашифрованному соединению (кроме localhost).
		if err := c.Auth(smtp.PlainAuth("", s.user, s.pass, s.host)); err != nil {
			return fmt.Errorf("mail: smtp auth: %w", err)
		}
	}
	if err := c.Mail(s.from.Address); err != nil {
		return fmt.Errorf("mail: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("mail: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: DATA end: %w", err)
	}
	return c.Quit()
}

func (s *SMTP) dial(ctx context.Context) (net.Conn, error) {
	addr := net.JoinHostPort(s.host, s.port)
	d := &net.Dialer{Timeout: 15 * time.Second}
	if s.port == "465" {
		td := &tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}}
		return td.DialContext(ctx, "tcp", addr)
	}
	return d.DialContext(ctx, "tcp", addr)
}

var errHeaderInjection = errors.New("mail: перевод строки в адресе или теме")

// build собирает письмо: text/plain в UTF-8, тело в base64, тема в RFC 2047.
func build(from *netmail.Address, m Message, now time.Time) ([]byte, error) {
	if strings.ContainsAny(m.To+m.Subject, "\r\n") {
		return nil, errHeaderInjection
	}
	to, err := netmail.ParseAddress(m.To)
	if err != nil {
		return nil, fmt.Errorf("mail: адрес %q: %w", m.To, err)
	}
	domain := from.Address[strings.LastIndex(from.Address, "@")+1:]

	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\n", from.String())
	fmt.Fprintf(&b, "To: %s\r\n", to.String())
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.BEncoding.Encode("UTF-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: <%s@%s>\r\n", uuid.NewString(), domain)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")

	enc := base64.StdEncoding.EncodeToString([]byte(m.Text))
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return b.Bytes(), nil
}
