package telegram

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

// TaskCommands — команды бота по задачам (§8.4, §8.5). Действия идут через сервис задач с теми
// же правилами и правами, что и в вебе; бот своей логики задач не имеет.
type TaskCommands struct {
	db       *gorm.DB
	tasks    *task.Service
	projects *project.Service
	groups   *Groups
}

func NewTaskCommands(db *gorm.DB, tasks *task.Service, projects *project.Service, groups *Groups) *TaskCommands {
	return &TaskCommands{db: db, tasks: tasks, projects: projects, groups: groups}
}

// RegisterTake подключает взятие задачи в работу (§8.4): кнопку take:<task_id> (карточка в группе,
// сообщение о запуске спринта, личное уведомление о назначении) и команду /take <ключ>.
func RegisterTake(rt *Router, tc *TaskCommands) {
	rt.Callback(ActionTake, tc.takeButton)
	rt.Command("take", tc.takeCommand)
}

var (
	fullKeyRe = regexp.MustCompile(`^([A-Za-z]{2,10})-(\d+)$`)
	numberRe  = regexp.MustCompile(`^\d+$`)
)

// taskRef — задача, на которую указывает аргумент команды: ключ и проект.
type taskRef struct {
	Key        string
	ProjectKey string
}

// resolveKey разбирает ключ задачи из аргумента (§8.5): «TM-42» в любом регистре; в группе,
// к которой привязан ровно один проект, — и просто «42». Пустой ref — ответ пользователю уже
// отправлен (формат, подсказка со списком проектов группы). usage — «Формат: …» команды.
func (tc *TaskCommands) resolveKey(ctx context.Context, r *Request, arg, usage string) (*taskRef, error) {
	arg = strings.TrimSpace(arg)
	if m := fullKeyRe.FindStringSubmatch(arg); m != nil {
		pk := strings.ToUpper(m[1])
		return &taskRef{Key: pk + "-" + m[2], ProjectKey: pk}, nil
	}
	if !numberRe.MatchString(arg) || r.Private() {
		return nil, r.Answer(ctx, Text("cmd_usage", usage), true)
	}
	ps, err := tc.groups.ChatProjects(ctx, r.Chat.ID)
	if err != nil {
		return nil, err
	}
	switch len(ps) {
	case 0:
		return nil, r.Answer(ctx, Text("group_no_projects", nil), true)
	case 1:
		return &taskRef{Key: ps[0].Key + "-" + arg, ProjectKey: ps[0].Key}, nil
	}
	return nil, r.Answer(ctx, Text("cmd_choose_project", map[string]any{"Projects": ps, "Cmd": "/" + r.Command, "Num": arg}), true)
}

// checkAccess — §8.4, шаг 2: в группе задача должна быть из проекта этой группы, а пользователь —
// участником проекта («Вы не участник проекта TM»). В личном чате о чужом проекте не
// говорим — «не найдена», как ответил бы сервис. false — ответ уже отправлен.
func (tc *TaskCommands) checkAccess(ctx context.Context, r *Request, ref *taskRef) (bool, error) {
	if !r.Private() {
		ps, err := tc.groups.ChatProjects(ctx, r.Chat.ID)
		if err != nil {
			return false, err
		}
		inGroup := false
		for _, p := range ps {
			inGroup = inGroup || p.Key == ref.ProjectKey
		}
		if !inGroup {
			return false, r.Answer(ctx, Text("task_not_found", ref), true)
		}
	}
	_, err := tc.projects.Access(ctx, r.User, ref.ProjectKey)
	switch {
	case errors.Is(err, project.ErrNotFound) && !r.Private():
		return false, r.Answer(ctx, Text("not_project_member", ref), true)
	case errors.Is(err, project.ErrNotFound):
		return false, r.Answer(ctx, Text("task_not_found", ref), true)
	case err != nil:
		return false, err
	}
	return true, nil
}

// takeCommand — /take <ключ> в группе или личном чате (§8.4, §8.5).
func (tc *TaskCommands) takeCommand(ctx context.Context, r *Request) error {
	ref, err := tc.resolveKey(ctx, r, r.Args, "/take TM-42")
	if ref == nil || err != nil {
		return err
	}
	return tc.take(ctx, r, ref)
}

// takeButton — «Взять в работу» / «Взять TM-5»: callback_data take:<task_id>; права проверяются
// сервисом при каждом нажатии (§8.8).
func (tc *TaskCommands) takeButton(ctx context.Context, r *Request) error {
	id, err := uuid.Parse(r.Data)
	if err != nil {
		return r.Answer(ctx, Text("p_task_not_found", nil), true)
	}
	var keys []string
	if err := tc.db.WithContext(ctx).Raw(`SELECT key FROM tasks WHERE id = ? AND deleted_at IS NULL`, id).Scan(&keys).Error; err != nil {
		return fmt.Errorf("telegram: task by id: %w", err)
	}
	if len(keys) == 0 {
		return r.Answer(ctx, Text("p_task_not_found", nil), true)
	}
	pk, _, _ := strings.Cut(keys[0], "-")
	return tc.take(ctx, r, &taskRef{Key: keys[0], ProjectKey: pk})
}

// take — §8.4, шаги 2–5: переход todo → in_progress сервисом в одной транзакции с блокировкой
// строки задачи (из двух одновременных нажатий задачу получает ровно один). Отказ — всплывающим
// уведомлением у кнопки или ответом, который в группе удаляется через 30 секунд. Успех в группе
// виден сразу: карточка правится, а «🛠 … взята в работу — …» публикуется ответом на неё —
// по событиям задачи, как при взятии из веба (§8.6).
func (tc *TaskCommands) take(ctx context.Context, r *Request, ref *taskRef) error {
	ok, err := tc.checkAccess(ctx, r, ref)
	if !ok || err != nil {
		return err
	}
	t, err := tc.tasks.Take(ctx, r.User, ref.Key)
	if err != nil {
		text, ferr := taskErrorText(ref, err)
		if ferr != nil {
			return ferr
		}
		return r.Answer(ctx, text, true)
	}
	if r.Private() || r.IsCallback() {
		return r.Answer(ctx, Text("taken_reply", t), false)
	}
	return nil // в группе ответ — сообщение о событии под карточкой
}

// taskErrorText — ответ на отказ сервиса задач (§8.4, §8.5, §9.3); ошибка сервера — err.
func taskErrorText(ref *taskRef, err error) (string, error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Status >= 500 {
		return "", err
	}
	switch ae.Code {
	case apierr.CodeNotFound:
		return Text("task_not_found", ref), nil
	case apierr.CodeProjectArchived:
		return Text("project_archived", map[string]string{"Key": ref.ProjectKey}), nil
	case apierr.CodeValidation:
		if fields, ok := ae.Details["fields"].([]apierr.FieldError); ok && len(fields) > 0 {
			return fmt.Sprintf("%s: %s", ae.Message, fields[0].Reason), nil
		}
	}
	// TASK_ALREADY_TAKEN, INVALID_TRANSITION, FORBIDDEN и прочие — текст сервиса: он уже в
	// формате §8.4 («Задача TM-42 уже в работе, исполнитель: …»).
	return ae.Message, nil
}
