package telegram

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
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
	cards    *Cards // карточка задачи для /task (§8.3)
}

func NewTaskCommands(db *gorm.DB, tasks *task.Service, projects *project.Service, groups *Groups, cards *Cards) *TaskCommands {
	return &TaskCommands{db: db, tasks: tasks, projects: projects, groups: groups, cards: cards}
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

// taskRef — задача, на которую указывает аргумент команды: ключ, проект и (в группе) проекты
// этой группы.
type taskRef struct {
	Key        string
	ProjectKey string
	chat       []project.Project
	inGroup    bool
}

// inGroup — команда или кнопка в группе. Нажатие без сообщения (чат неизвестен) — как в личном.
func inGroup(r *Request) bool {
	return r.Chat.Type == models.ChatTypeGroup || r.Chat.Type == models.ChatTypeSupergroup
}

// newRef — ключ задачи из проекта и номера; «007» — это задача 7.
func newRef(projectKey, number string) *taskRef {
	n, _ := strconv.Atoi(number)
	pk := strings.ToUpper(projectKey)
	return &taskRef{Key: pk + "-" + strconv.Itoa(n), ProjectKey: pk}
}

// resolveKey разбирает ключ задачи из аргумента (§8.5): «TM-42» в любом регистре; в группе,
// к которой привязан ровно один проект, — и просто «42». nil — ответ пользователю уже
// отправлен (формат, подсказка со списком проектов группы). usage — «Формат: …» команды.
func (tc *TaskCommands) resolveKey(ctx context.Context, r *Request, arg, usage string) (*taskRef, error) {
	arg = strings.TrimSpace(arg)
	var ref *taskRef
	if m := fullKeyRe.FindStringSubmatch(arg); m != nil {
		ref = newRef(m[1], m[2])
	} else if !numberRe.MatchString(arg) || !inGroup(r) {
		return nil, r.Answer(ctx, Text("cmd_usage", usage), true)
	}
	if !inGroup(r) {
		return ref, nil
	}
	ps, err := tc.groups.ChatProjects(ctx, r.Chat.ID)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		switch len(ps) {
		case 0:
			return nil, r.Answer(ctx, Text("group_no_projects", nil), true)
		case 1:
			ref = newRef(ps[0].Key, arg)
		default:
			return nil, r.Answer(ctx, Text("cmd_choose_project", map[string]any{"Projects": ps, "Cmd": "/" + r.Command, "Num": arg}), true)
		}
	}
	ref.chat, ref.inGroup = ps, true
	return ref, nil
}

// inChat — §8.4, шаг 2: в группе задача должна быть из проекта этой группы.
func (ref *taskRef) inChat() bool {
	if !ref.inGroup {
		return true
	}
	for _, p := range ref.chat {
		if p.Key == ref.ProjectKey {
			return true
		}
	}
	return false
}

// refuse — отказ по шагу 2 §8.4 или ошибке сервиса: всплывающим окном у кнопки, ответом,
// который в группе удаляется через 30 секунд, у команды. В группе «не найдена» уточняется:
// если пользователь не участник проекта — «Вы не участник проекта TM». Права проверяет сервис
// под блокировкой; здесь — только выбор текста отказа.
//
// statusText — отказ перехода (409 INVALID_TRANSITION) показать статусом задачи («в статусе
// Готово, команда недоступна»), а не текстом сервиса: для команд-переходов §8.5.
func (tc *TaskCommands) refuse(ctx context.Context, r *Request, ref *taskRef, err error, statusText ...bool) error {
	if ref.inGroup && (err == nil || errors.Is(err, task.ErrNotFound)) {
		if _, aerr := tc.projects.Access(ctx, r.User, ref.ProjectKey); errors.Is(aerr, project.ErrNotFound) {
			return r.Answer(ctx, Text("not_project_member", ref), true)
		} else if aerr != nil {
			return aerr
		}
		if err == nil { // участник, но проект не этой группы
			return r.Answer(ctx, Text("not_project_member", ref), true)
		}
	}
	if text, ok := commandErrorText(ref, err); ok && len(statusText) > 0 && statusText[0] {
		return r.Answer(ctx, text, true)
	}
	text, ferr := taskErrorText(ref, err)
	if ferr != nil {
		return ferr
	}
	return r.Answer(ctx, text, true)
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
	key, err := tc.tasks.KeyByID(ctx, id)
	if errors.Is(err, task.ErrNotFound) {
		return r.Answer(ctx, Text("p_task_not_found", nil), true)
	}
	if err != nil {
		return err
	}
	pk, number, _ := strings.Cut(key, "-")
	ref := newRef(pk, number)
	if inGroup(r) {
		if ref.chat, err = tc.groups.ChatProjects(ctx, r.Chat.ID); err != nil {
			return err
		}
		ref.inGroup = true
	}
	return tc.take(ctx, r, ref)
}

// take — §8.4, шаги 2–5: переход todo → in_progress сервисом в одной транзакции с блокировкой
// строки задачи (из двух одновременных нажатий задачу получает ровно один). Успех в группе
// обычно виден сразу: карточка правится, а «🛠 … взята в работу — …» публикуется ответом на
// неё — по событию задачи, как при взятии из веба (§8.6). Если такого сообщения не будет
// (событие выключено в проекте или задача уже была в работе без исполнителя), бот отвечает сам
// временным ответом; у кнопки и в личном чате — ответом всегда.
func (tc *TaskCommands) take(ctx context.Context, r *Request, ref *taskRef) error {
	if !ref.inChat() {
		return tc.refuse(ctx, r, ref, nil)
	}
	before, err := tc.status(ctx, ref.Key)
	if err != nil {
		return err
	}
	t, err := tc.tasks.Take(ctx, r.User, ref.Key)
	if err != nil {
		return tc.refuse(ctx, r, ref, err)
	}
	if !ref.inGroup || r.IsCallback() {
		return r.Answer(ctx, Text("taken_reply", t), false)
	}
	announced, err := tc.announced(ctx, ref.ProjectKey, project.NotifyTaskTaken)
	if err != nil {
		return err
	}
	if before == task.StatusTodo && announced {
		return nil
	}
	return r.ReplyTemp(ctx, Text("taken_reply", t))
}

// status — статус задачи до действия (выбор ответа, не проверка прав).
func (tc *TaskCommands) status(ctx context.Context, key string) (string, error) {
	var st []string
	if err := tc.db.WithContext(ctx).Raw(`SELECT status FROM tasks WHERE key = ?`, key).Scan(&st).Error; err != nil {
		return "", fmt.Errorf("telegram: task status: %w", err)
	}
	if len(st) == 0 {
		return "", nil
	}
	return st[0], nil
}

// announced — включено ли событие setting (§8.6) в проекте: тогда об успехе в группе напишет
// сообщение о событии.
func (tc *TaskCommands) announced(ctx context.Context, projectKey, setting string) (bool, error) {
	var raw []events.JSON
	if err := tc.db.WithContext(ctx).Raw(`SELECT notification_settings FROM projects WHERE key = ?`, projectKey).Scan(&raw).Error; err != nil {
		return false, fmt.Errorf("telegram: project settings: %w", err)
	}
	return len(raw) == 1 && project.EffectiveNotifications(raw[0])[setting], nil
}

// taskErrorText — ответ на отказ сервиса задач (§8.4, §8.5, §9.3); ошибка сервера — err.
func taskErrorText(ref *taskRef, err error) (string, error) {
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Status >= 500 {
		return "", err
	}
	switch ae.Code {
	case apierr.CodeNotFound:
		return Text("cmd_task_not_found", ref), nil
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
