package telegram

import (
	"context"
	"errors"
	"strings"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// RegisterTaskCommands подключает команды по задачам §8.5: /drop, /review, /done, /log, /comment,
// /task — в группе и личном чате. cards — карточка задачи для /task (§8.3).
func RegisterTaskCommands(rt *Router, tc *TaskCommands, cards *Cards) {
	tc.cards = cards
	rt.Command("drop", tc.transitionCommand(transitionCmd{from: task.StatusInProgress, to: task.StatusTodo,
		usage: "/drop TM-42", setting: project.NotifyTaskReturned, reply: "cmd_dropped"}))
	rt.Command("review", tc.transitionCommand(transitionCmd{from: task.StatusInProgress, to: task.StatusReview,
		usage: "/review TM-42", setting: project.NotifyTaskReview, reply: "cmd_reviewed"}))
	rt.Command("done", tc.transitionCommand(transitionCmd{from: task.StatusReview, to: task.StatusDone,
		usage: "/done TM-42", setting: project.NotifyTaskAccepted, reply: "cmd_accepted"}))
	rt.Command("log", tc.logCommand)
	rt.Command("comment", tc.commentCommand)
	rt.Command("task", tc.taskCommand)
}

// transitionCmd — команда-переход §4: из какого статуса в какой, формат, событие §8.6, о котором
// в группе напишет сообщение, и ответ, если его не будет.
type transitionCmd struct {
	from, to, usage, setting, reply string
}

// transitionCommand — /drop, /review, /done <ключ>: переход по правилам §4 сервисом задач
// (права — там же: /done исполнителем-автором отклоняется). В группе об успехе пишет сообщение
// о событии ответом на карточку (§8.6); если событие выключено — временный ответ. В личном
// чате — ответ всегда.
func (tc *TaskCommands) transitionCommand(c transitionCmd) HandlerFunc {
	return func(ctx context.Context, r *Request) error {
		ref, err := tc.resolveKey(ctx, r, r.Args, c.usage)
		if ref == nil || err != nil {
			return err
		}
		if !ref.inChat() {
			return tc.refuse(ctx, r, ref, nil)
		}
		t, err := tc.tasks.Transition(ctx, r.User, ref.Key, task.TransitionInput{From: c.from, To: c.to})
		if err != nil {
			return tc.refuse(ctx, r, ref, err)
		}
		text := Text(c.reply, map[string]any{"Key": t.Key, "Title": t.Title, "Spent": spentText(t.SpentMinutes, t.EstimateMinutes)})
		if !ref.inGroup {
			return r.Reply(ctx, text)
		}
		announced, err := tc.announced(ctx, ref.ProjectKey, c.setting)
		if err != nil || announced {
			return err
		}
		return r.ReplyTemp(ctx, text)
	}
}

// logCommand — /log <ключ> <время> [комментарий]: запись времени на сегодня с source = telegram
// (§8.5, формат времени §6.1). Ответ «Списано … на TM-42. Всего … из …»; в группе он удаляется
// через 30 секунд.
func (tc *TaskCommands) logCommand(ctx context.Context, r *Request) error {
	const usage = "/log TM-42 1h 30m [комментарий]"
	keyArg, rest, _ := strings.Cut(strings.TrimSpace(r.Args), " ")
	dur, comment, ok := splitDuration(rest)
	if !ok {
		return r.Answer(ctx, Text("cmd_usage", usage), true)
	}
	ref, err := tc.resolveKey(ctx, r, keyArg, usage)
	if ref == nil || err != nil {
		return err
	}
	if !ref.inChat() {
		return tc.refuse(ctx, r, ref, nil)
	}
	in := task.WorklogInput{Duration: dur}
	if comment != "" {
		in.Comment = &comment
	}
	w, err := tc.tasks.AddWorklog(ctx, r.User, ref.Key, in)
	if err != nil {
		return tc.refuse(ctx, r, ref, err)
	}
	t, _, err := tc.tasks.Get(ctx, r.User, ref.Key)
	if err != nil {
		return err
	}
	return r.ReplyTemp(ctx, Text("cmd_logged", map[string]any{"Key": t.Key, "Logged": duration.Format(w.Minutes),
		"Total": spentText(t.SpentMinutes, t.EstimateMinutes)}))
}

// splitDuration делит «1h 30m починил экспорт» на длительность и комментарий: длительность — самое
// длинное начало из слов, которое разбирает парсер §6.1. ok=false — длительности нет.
func splitDuration(s string) (dur, comment string, ok bool) {
	words := strings.Fields(s)
	for n := len(words); n > 0; n-- {
		head := strings.Join(words[:n], " ")
		if _, err := duration.Parse(head); err == nil {
			return head, strings.Join(words[n:], " "), true
		}
	}
	return "", "", false
}

// commentCommand — /comment <ключ> <текст>: комментарий с source = telegram (§8.5).
func (tc *TaskCommands) commentCommand(ctx context.Context, r *Request) error {
	const usage = "/comment TM-42 <текст>"
	keyArg, body, _ := strings.Cut(strings.TrimSpace(r.Args), " ")
	if strings.TrimSpace(body) == "" {
		return r.Answer(ctx, Text("cmd_usage", usage), true)
	}
	ref, err := tc.resolveKey(ctx, r, keyArg, usage)
	if ref == nil || err != nil {
		return err
	}
	if !ref.inChat() {
		return tc.refuse(ctx, r, ref, nil)
	}
	if _, err := tc.tasks.AddComment(ctx, r.User, ref.Key, strings.TrimSpace(body)); err != nil {
		return tc.refuse(ctx, r, ref, err)
	}
	return r.Reply(ctx, Text("cmd_commented", ref))
}

// taskCommand — /task <ключ>: карточка задачи в формате §8.3 с кнопками. В группе проекта это
// новая карточка задачи: она запоминается в telegram_message_id, дальнейшие изменения правят её.
// В личном чате — только показ.
func (tc *TaskCommands) taskCommand(ctx context.Context, r *Request) error {
	ref, err := tc.resolveKey(ctx, r, r.Args, "/task TM-42")
	if ref == nil || err != nil {
		return err
	}
	if !ref.inChat() {
		return tc.refuse(ctx, r, ref, nil)
	}
	t, _, err := tc.tasks.Get(ctx, r.User, ref.Key)
	if err == nil && t.DeletedAt != nil {
		err = task.ErrNotFound // администратор видит удалённые, но карточки у них нет (§4)
	}
	if err != nil {
		return tc.refuse(ctx, r, ref, err)
	}
	if ref.inGroup {
		return r.out.Send(ctx, Message{Op: OpSend, Kind: KindCard, Ref: t.ID.String(), Stamp: cardRepublish}, 0)
	}
	row, err := tc.cards.load(ctx, t.ID.String())
	if err != nil || row == nil {
		return err
	}
	m := r.reply("")
	m.Text, m.Keyboard = tc.cards.content(row)
	m.HTML = true
	return r.out.Send(ctx, m, 0)
}

// commandErrorText — отказ сервиса на команду-переход: вместо «Переход done → todo не разрешён» —
// статус задачи по-русски (§8.5).
func commandErrorText(ref *taskRef, err error) (string, bool) {
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != apierr.CodeInvalidTransition {
		return "", false
	}
	from, _ := ae.Details["from"].(string)
	if from == "" {
		return "", false
	}
	return Text("cmd_bad_status", map[string]string{"Key": ref.Key, "Status": task.StatusName(from)}), true
}
