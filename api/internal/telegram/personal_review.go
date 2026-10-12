package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Действия кнопок уведомления «отправлена на проверку» (§8.7, callback_data <действие>:<task_id>).
const (
	ActionAccept = "accept" // review → done
	ActionRework = "rework" // review → in_progress, причина — следующим сообщением
)

// ReasonTTL — сколько бот ждёт причину возврата после нажатия «Вернуть». Потом обычный текст
// снова игнорируется, а кнопку можно нажать ещё раз.
const ReasonTTL = 10 * time.Minute

// minReason — §4: причина возврата на доработку не короче 5 символов.
const minReason = 5

// awaitReason — что бот ждёт от пользователя после «Вернуть»: короткоживущее состояние в Redis
// по telegram_user_id.
type awaitReason struct {
	UserID    uuid.UUID `json:"user_id"`
	Key       string    `json:"key"`
	ChatID    int64     `json:"chat_id"`
	MessageID int       `json:"message_id"` // уведомление с кнопками: убрать их после возврата
	Text      string    `json:"text"`       // его текст (editMessageText требует текст целиком)
}

func awaitKey(tgID int64) string { return "tg:await-reason:" + strconv.FormatInt(tgID, 10) }

// reviewTask — задача из callback_data глазами нажавшего.
type reviewTask struct {
	Key, Status string
	CanReturn   bool // автор задачи, менеджер проекта или администратор (§4: review → in_progress)
}

// findTask — неудалённая задача по id из callback_data, если u — участник её проекта или
// администратор; иначе nil, как 404 у сервиса: ключ чужой задачи не раскрывается.
func (p *Personal) findTask(ctx context.Context, u *user.User, data string) (*reviewTask, error) {
	id, err := uuid.Parse(data)
	if err != nil {
		return nil, nil
	}
	var out *reviewTask
	err = readSnapshot(p.db.WithContext(ctx), func(tx *gorm.DB) error {
		t, err := task.SnapshotWithDeleted(tx, id)
		if err != nil || t == nil || t.Deleted {
			return err
		}
		live, role, err := project.LiveRole(tx, t.ProjectID, u.ID)
		if err != nil || !live || (role == "" && !u.IsSystemAdmin) {
			return err
		}
		out = &reviewTask{Key: t.Key, Status: t.Status,
			CanReturn: t.ReporterID == u.ID || role == project.RoleManager || u.IsSystemAdmin}
		return nil
	})
	return out, err
}

// acceptButton — «Принять»: переход review → done от имени нажавшего по правилам §4 (права
// проверяются сервисом при каждом нажатии, §8.8). Повторное нажатие — «уже не на проверке».
func (p *Personal) acceptButton(ctx context.Context, r *Request) error {
	t, err := p.findTask(ctx, r.User, r.Data)
	if err != nil {
		return err
	}
	if t == nil {
		return r.Answer(ctx, Text("p_task_not_found", nil), true)
	}
	in := task.TransitionInput{From: task.StatusReview, To: task.StatusDone}
	if _, err := p.tasks.Transition(ctx, r.User, t.Key, in); err != nil {
		text, ferr := transitionError(t.Key, err)
		if ferr != nil {
			return ferr
		}
		return r.Answer(ctx, text, true)
	}
	p.forgetReason(ctx, r.From.ID, t.Key)
	if m := r.Update.CallbackQuery.Message.Message; m != nil {
		p.markDone(ctx, r.Chat.ID, m.ID, m.Text, t.Key, "p_mark_accepted")
	}
	return r.Answer(ctx, Text("p_accept_done", map[string]string{"Key": t.Key}), false)
}

// forgetReason — после «Принять» ожидание причины возврата той же задачи больше не нужно:
// иначе следующее обычное сообщение пошло бы причиной. Ошибка — только в лог.
func (p *Personal) forgetReason(ctx context.Context, tgID int64, key string) {
	raw, err := p.rdb.Get(ctx, awaitKey(tgID)).Bytes()
	if err != nil {
		return
	}
	var st awaitReason
	if json.Unmarshal(raw, &st) == nil && st.Key != key {
		return
	}
	if err := p.rdb.Del(ctx, awaitKey(tgID)).Err(); err != nil {
		slog.Warn("telegram: await state", "err", err)
	}
}

// reworkButton — «Вернуть»: запоминает задачу и просит причину следующим сообщением (§8.7).
// Права и статус проверяются при нажатии, чтобы не просить причину зря; сам переход — в
// reworkReason, где сервис проверяет их ещё раз.
func (p *Personal) reworkButton(ctx context.Context, r *Request) error {
	t, err := p.findTask(ctx, r.User, r.Data)
	if err != nil {
		return err
	}
	if t == nil {
		return r.Answer(ctx, Text("p_task_not_found", nil), true)
	}
	if t.Status != task.StatusReview {
		return r.Answer(ctx, Text("p_not_in_review", map[string]string{"Key": t.Key, "Status": task.StatusName(t.Status)}), true)
	}
	if !t.CanReturn {
		return r.Answer(ctx, Text("p_rework_forbidden", nil), true)
	}
	st := awaitReason{UserID: r.User.ID, Key: t.Key, ChatID: r.Chat.ID}
	if m := r.Update.CallbackQuery.Message.Message; m != nil {
		st.MessageID, st.Text = m.ID, m.Text
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("telegram: await state: %w", err)
	}
	if err := p.rdb.Set(ctx, awaitKey(r.From.ID), raw, ReasonTTL).Err(); err != nil {
		return fmt.Errorf("telegram: await state: %w", err)
	}
	return r.Reply(ctx, Text("p_rework_prompt", map[string]string{"Key": t.Key}))
}

// reworkReason — обычный текст в личном чате. Если бот ждёт причину возврата — переход
// review → in_progress с этим текстом комментарием; иначе текст игнорируется.
func (p *Personal) reworkReason(ctx context.Context, r *Request) error {
	raw, err := p.rdb.Get(ctx, awaitKey(r.From.ID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("telegram: await state: %w", err)
	}
	var st awaitReason
	if err := json.Unmarshal(raw, &st); err != nil || st.UserID != r.User.ID {
		// Битое состояние или Telegram с тех пор привязан к другому аккаунту — забываем.
		return p.rdb.Del(ctx, awaitKey(r.From.ID)).Err()
	}
	if utf8.RuneCountInString(r.Args) < minReason {
		return r.Reply(ctx, Text("p_rework_short", nil)) // ждём дальше
	}
	_, err = p.tasks.Transition(ctx, r.User, st.Key, task.TransitionInput{From: task.StatusReview, To: task.StatusInProgress, Comment: r.Args})
	var ae *apierr.Error
	if err != nil && errors.As(err, &ae) && ae.Code == apierr.CodeValidation {
		// Причина не подошла (например, длиннее 5000 символов) — ждём другую.
		text, _ := transitionError(st.Key, err)
		return r.Reply(ctx, text)
	}
	if derr := p.rdb.Del(ctx, awaitKey(r.From.ID)).Err(); derr != nil {
		return fmt.Errorf("telegram: await state: %w", derr)
	}
	if err != nil {
		text, ferr := transitionError(st.Key, err)
		if ferr != nil {
			return ferr
		}
		return r.Reply(ctx, text)
	}
	if st.MessageID != 0 {
		p.markDone(ctx, st.ChatID, st.MessageID, st.Text, st.Key, "p_mark_reworked")
	}
	return r.Reply(ctx, Text("p_rework_done", map[string]string{"Key": st.Key}))
}

// markDone убирает кнопки «Принять» и «Вернуть» из уведомления и дописывает итог: повторно
// нажимать нечего. Ошибка постановки в очередь только в лог — переход уже сделан.
func (p *Personal) markDone(ctx context.Context, chatID int64, messageID int, text, key, mark string) {
	if text == "" {
		return
	}
	m := Message{Op: OpEdit, ChatID: chatID, MessageID: messageID, Text: text + "\n\n" + Text(mark, nil),
		Keyboard: keyboard(p.openButton(key))}
	if err := p.out.Send(ctx, m, 0); err != nil {
		slog.Error("telegram: edit not queued", "chat_id", chatID, "err", err)
	}
}

// transitionError — ответ пользователю на отказ сервиса задач. Внутренняя ошибка
// возвращается как есть: обработчик отдаст её процессору (лог и общий ответ).
func transitionError(key string, err error) (string, error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Status >= 500 {
		return "", err
	}
	projectKey, _, _ := strings.Cut(key, "-")
	switch ae.Code {
	case apierr.CodeNotFound:
		return Text("p_task_not_found", nil), nil
	case apierr.CodeProjectArchived:
		return Text("p_archived", map[string]string{"Key": projectKey}), nil
	case apierr.CodeInvalidTransition:
		from, _ := ae.Details["from"].(string)
		return Text("p_not_in_review", map[string]string{"Key": key, "Status": task.StatusName(from)}), nil
	case apierr.CodeValidation:
		if fields, ok := ae.Details["fields"].([]apierr.FieldError); ok && len(fields) > 0 {
			return fmt.Sprintf("%s: %s", ae.Message, fields[0].Reason), nil
		}
	}
	return ae.Message, nil
}
