package task

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/sprint/sprintdb"
	"github.com/jack5505/taskflow/api/internal/user"
)

// statusNames — названия статусов для сообщений (как в интерфейсе и боте).
var statusNames = map[string]string{
	StatusTodo: "К выполнению", StatusInProgress: "В работе", StatusReview: "На проверке",
	StatusDone: "Готово", StatusCancelled: "Отменена",
}

// StatusName — название статуса для сообщений бота: «В работе», «Готово».
func StatusName(status string) string { return statusNames[status] }

// transitions — допустимые переходы §4; всё остальное — 409 INVALID_TRANSITION.
var transitions = map[string]map[string]bool{
	StatusTodo:       {StatusInProgress: true, StatusCancelled: true},
	StatusInProgress: {StatusTodo: true, StatusReview: true, StatusCancelled: true},
	StatusReview:     {StatusInProgress: true, StatusDone: true, StatusCancelled: true},
	StatusDone:       {StatusInProgress: true},
	StatusCancelled:  {StatusTodo: true},
}

// minReworkComment — §4: причина возврата на доработку не короче 5 символов.
const minReworkComment = 5

var (
	errForbiddenTransition = apierr.Forbidden("Этот переход вам недоступен")
	errAcceptOwn           = apierr.Forbidden("Свою задачу, где вы и автор, и исполнитель, принимает менеджер")
)

// ErrInvalidTransition — 409 INVALID_TRANSITION с текстом «Переход done → todo не разрешён».
func ErrInvalidTransition(from, to string) error {
	return apierr.Conflict(apierr.CodeInvalidTransition, fmt.Sprintf("Переход %s → %s не разрешён", from, to)).
		With("from", from).With("to", to)
}

// TransitionInput — смена статуса: to, причина (для доработки и отмены) и исполнитель
// (для переоткрытия задачи, чей исполнитель деактивирован).
type TransitionInput struct {
	// From — ожидаемый текущий статус (кнопки бота §8.7: «Принять» и «Вернуть» — только из review);
	// задача в другом статусе — 409 INVALID_TRANSITION. Пусто — без проверки.
	From       string
	To         string
	Comment    string
	AssigneeID *uuid.UUID
}

// actor — кто меняет и его права в проекте задачи (под блокировкой).
type actor struct {
	user    *user.User
	member  bool // участник проекта (исполнителем может быть только участник)
	admin   bool // администратор системы
	manager bool // менеджер проекта или администратор системы (§2)
	source  string
	sprints []uuid.UUID // спринты, заблокированные FOR SHARE раньше задачи (openTask)
}

// Transition — POST /tasks/{key}/transition (§4). Строка задачи блокируется FOR UPDATE.
// Нового исполнителя (для переоткрытия) блокируем заранее; проверяет его reopen.
func (s *Service) Transition(ctx context.Context, u *user.User, key string, in TransitionInput) (*Task, error) {
	var pre preLock
	if in.AssigneeID != nil {
		pre.users = []uuid.UUID{*in.AssigneeID}
	}
	pre.sprintCheck = in.To == StatusInProgress // переоткрытие done → in_progress переносит из закрытого спринта
	return s.change(ctx, u, key, nil, pre, func(tx *gorm.DB, t *Task, a actor) error {
		return applyTransition(tx, t, a, in)
	})
}

// Take — POST /tasks/{key}/take: назначить себя и перевести в in_progress одной операцией
// (§9.2, §8.4). Назначенную на другого — 409 TASK_ALREADY_TAKEN с исполнителем в details,
// в том числе менеджеру (переназначить — PATCH). Задачу в работе без исполнителя (например,
// после деактивации исполнителя) берёт любой участник.
func (s *Service) Take(ctx context.Context, u *user.User, key string) (*Task, error) {
	return s.change(ctx, u, key, nil, preLock{}, func(tx *gorm.DB, t *Task, a actor) error {
		mine := t.AssigneeID != nil && *t.AssigneeID == u.ID
		switch {
		case mine && t.Status == StatusInProgress:
			return errNoChange // повторное нажатие той же кнопки — без изменений
		case t.Status == StatusReview || t.Status == StatusDone || t.Status == StatusCancelled:
			return apierr.Conflict(apierr.CodeInvalidTransition,
				fmt.Sprintf("Задача %s в статусе %s, взять нельзя", t.Key, statusNames[t.Status])).
				With("from", t.Status).With("to", StatusInProgress)
		case t.AssigneeID != nil && !mine:
			return alreadyTaken(tx, t)
		}
		if !a.member {
			return errNotMember
		}
		if err := setAssignee(tx, t, &u.ID, u.ID); err != nil {
			return err
		}
		if t.Status == StatusInProgress {
			// Уже в работе, но без исполнителя — только назначение.
			return events.Publish(tx, events.TaskUpdated, map[string]any{
				"task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "by": u.ID, "fields": []string{"assignee_id"},
			})
		}
		return applyTransition(tx, t, a, TransitionInput{To: StatusInProgress})
	})
}

var (
	errNoChange    = errors.New("task: без изменений")
	errNotMember   = apierr.Forbidden("Исполнителем может быть только участник проекта")
	errUserBlocked = apierr.Unauthorized("Пользователь деактивирован")
)

// preLock — строки, которые change() блокирует FOR SHARE раньше строки задачи: будущие
// исполнители (деактивация берёт FOR UPDATE пользователя, затем его задачи) и целевые спринты
// (закрытие спринта блокирует спринт, затем его задачи). Только блокировка — проверяет их fn
// после проверки прав, чтобы ошибки шли в порядке 404 → 403 → 400.
type preLock struct {
	users, sprints []uuid.UUID
	withDeleted    bool // разрешить удалённую задачу (восстановление)
	reorder        bool // перестановка: advisory-блокировка порядка задач проекта (см. Move)
	// sprintCheck — операция может сменить спринт задачи (перенос, переоткрытие): текущий спринт
	// задачи и активный спринт проекта тоже берутся FOR SHARE раньше задачи (#160).
	sprintCheck bool
}

// opened — задача под блокировкой, её проект и права действующего пользователя.
type opened struct {
	task    Task
	project project.Project
	actor   actor
}

// openTask — общее начало любой операции над задачей. Порядок блокировок тот же, что у деактивации
// и закрытия спринта: FOR SHARE строк действующего пользователя и pre, затем строка задачи (lock:
// FOR UPDATE для изменения, FOR SHARE — для действий, не меняющих её поля), затем FOR SHARE проекта.
// Права — из строк под блокировкой. Ошибки: деактивирован — 401; нет задачи, не участник или
// удалённая для не-администратора — 404. Архив проверяет вызывающий — в нужном месте порядка ответов.
func openTask(tx *gorm.DB, u *user.User, key string, pre preLock, lock rowLock) (*opened, error) {
	me, err := user.LockActor(tx, u.ID)
	if err != nil {
		return nil, err
	}
	if me == nil || !me.IsActive {
		return nil, errUserBlocked
	}
	if err := user.LockShare(tx, pre.users); err != nil {
		return nil, err
	}
	if err := sprintdb.LockShare(tx, pre.sprints); err != nil {
		return nil, err
	}
	if pre.reorder {
		// Проект берём из ключа задачи: блокировка нужна раньше строки задачи.
		projectKey := keyRe.FindStringSubmatch(key)[1]
		if err := lockTaskOrder(tx, projectKey); err != nil {
			return nil, err
		}
	}
	var o opened
	found, err := lockTaskByKey(tx, key, pre.withDeleted, lock, &o.task)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	p, err := project.LockShareLive(tx, o.task.ProjectID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNotFound
	}
	o.project = *p
	role, err := project.RoleIn(tx, o.project.ID, u.ID)
	if err != nil {
		return nil, err
	}
	admin := me.IsSystemAdmin
	if role == "" && !admin {
		return nil, ErrNotFound
	}
	if o.task.DeletedAt != nil && !admin {
		return nil, ErrNotFound // корзину видит только администратор (§2) — раньше любых других ответов
	}
	_, source := audit.From(tx.Statement.Context)
	o.actor = actor{user: u, member: role != "", admin: admin, manager: admin || role == project.RoleManager, source: source,
		sprints: pre.sprints}
	return &o, nil
}

// change — общая часть изменений задачи. Порядок блокировок тот же, что у деактивации и закрытия
// спринта: FOR SHARE строк действующего пользователя и pre (при pre.sprintCheck — ещё текущего
// спринта задачи и активного спринта проекта), затем FOR UPDATE строки задачи, затем FOR SHARE
// проекта. Спринты читаются до блокировок; если задачу перенесли или fn нужен ещё спринт
// (needSprintLock), транзакция повторяется — fn может выполниться несколько раз и не должен иметь
// побочных эффектов вне транзакции. Права берутся из строк под блокировкой, а не из снимка при входе.
// ifMatch — версии из If-Match: nil — без сверки, иначе версия задачи должна быть в списке.
// После fn — остановка таймеров, которым больше нельзя идти на задаче (§6.4), version+1 и сохранение; fn, вернувший errNoChange, ничего не меняет — тогда
// возвращается задача, какой она была до fn.
func (s *Service) change(ctx context.Context, u *user.User, key string, ifMatch []int, pre preLock,
	fn func(*gorm.DB, *Task, actor) error) (*Task, error) {
	if keyRe.FindStringSubmatch(key) == nil {
		return nil, ErrNotFound
	}
	var t, before Task
	var extra []uuid.UUID // спринты, которые понадобились в прошлой попытке (needSprintLock)
	var err error
	for range 3 {
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			p := pre
			p.sprints = append(slices.Clone(pre.sprints), extra...)
			var cur []uuid.UUID
			if pre.sprintCheck {
				// Текущий спринт задачи и активный спринт проекта — FOR SHARE раньше строки задачи
				// (#160): закрытие и старт спринта держат их FOR UPDATE и блокируют задачи после.
				var err error
				if cur, err = sprintOfKey(tx, key); err != nil {
					return err
				}
				active, err := activeSprintOfKey(tx, key)
				if err != nil {
					return err
				}
				p.sprints = append(append(p.sprints, cur...), active...)
			}
			o, err := openTask(tx, u, key, p, forUpdate)
			if err != nil {
				return err
			}
			if pre.sprintCheck && !samePtr(o.task.SprintID, firstID(cur)) {
				return errSprintMoved // перенесли, пока брались блокировки — повтор
			}
			t = o.task
			if err := project.EnsureWritable(&o.project); err != nil {
				return err
			}
			if ifMatch != nil && !slices.Contains(ifMatch, t.Version) {
				// §3.3: чужая правка — 409 с текущим состоянием, чтобы интерфейс показал её.
				return apierr.Conflict(apierr.CodeVersionConflict, "Задачу уже изменил другой пользователь").
					With("current", t).With("expected_version", ifMatch)
			}
			a := o.actor
			before = t
			if err := fn(tx, &t, a); err != nil {
				return err
			}
			if t.Status != before.Status || !samePtr(t.AssigneeID, before.AssigneeID) || (t.DeletedAt == nil) != (before.DeletedAt == nil) {
				if err := stopStaleTaskTimers(tx, &t, s.now()); err != nil {
					return err
				}
			}
			t.Version = before.Version + 1
			return tx.Model(&t).Select("*").Omit("CreatedAt").Updates(&t).Error
		})
		var need needSprintLock
		switch {
		case errors.As(err, &need):
			extra = append(extra, need.id)
			continue
		case errors.Is(err, errSprintMoved):
			continue
		}
		break
	}
	if errors.Is(err, errNoChange) {
		return &before, nil
	}
	var need needSprintLock
	if errors.As(err, &need) || errors.Is(err, errSprintMoved) {
		// Три попытки подряд спринты менялись под руками — как одновременная правка (§3.3).
		return nil, apierr.Conflict(apierr.CodeVersionConflict, "Задачу или её спринт одновременно меняют, повторите действие")
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// errSprintMoved — задачу перенесли в другой спринт, пока брались блокировки; change и worklogTx
// повторяют транзакцию.
var errSprintMoved = errors.New("task: sprint moved while locking")

// needSprintLock — операции нужен спринт id под FOR SHARE, а взять его можно только раньше строки
// задачи: change повторяет транзакцию, добавив его к блокировкам.
type needSprintLock struct{ id uuid.UUID }

func (e needSprintLock) Error() string {
	return "task: нужна блокировка спринта " + e.id.String()
}

// activeSprintOfKey — активный спринт проекта задачи key без блокировки (пусто — нет).
func activeSprintOfKey(tx *gorm.DB, key string) ([]uuid.UUID, error) {
	projectID, err := projectOfKey(tx, key)
	if err != nil || projectID == nil {
		return nil, err
	}
	active, err := sprintdb.ActiveID(tx, *projectID)
	if err != nil || active == nil {
		return nil, err
	}
	return []uuid.UUID{*active}, nil
}

// firstID — первый id или nil.
func firstID(ids []uuid.UUID) *uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	return &ids[0]
}

// applyTransition — правила и побочные эффекты одного перехода §4.
func applyTransition(tx *gorm.DB, t *Task, a actor, in TransitionInput) error {
	from, to := t.Status, in.To
	if _, ok := statusNames[to]; !ok {
		return fieldError("to", "todo, in_progress, review, done или cancelled")
	}
	if !transitions[from][to] || (in.From != "" && in.From != from) {
		return ErrInvalidTransition(from, to)
	}
	uid := a.user.ID
	prevAssignee := t.AssigneeID
	isAssignee := t.AssigneeID != nil && *t.AssigneeID == uid
	isReporter := t.ReporterID == uid
	now := time.Now()
	comment := strings.TrimSpace(in.Comment)
	if utf8.RuneCountInString(comment) > 5000 {
		return fieldError("comment", "не длиннее 5000 символов")
	}

	switch {
	case from == StatusTodo && to == StatusInProgress:
		switch {
		case t.AssigneeID == nil:
			if !a.member {
				return errNotMember
			}
			if err := setAssignee(tx, t, &uid, uid); err != nil {
				return err
			}
		case !isAssignee && !a.manager:
			return errForbiddenTransition
		}
		if t.StartedAt == nil {
			t.StartedAt = &now
		}
	case from == StatusInProgress && to == StatusTodo:
		if !isAssignee && !a.manager {
			return errForbiddenTransition
		}
		if err := setAssignee(tx, t, nil, uid); err != nil {
			return err
		}
	case from == StatusInProgress && to == StatusReview:
		if !isAssignee && !a.manager {
			return errForbiddenTransition
		}
	case from == StatusReview && to == StatusInProgress:
		if !isReporter && !a.manager {
			return errForbiddenTransition
		}
		if utf8.RuneCountInString(comment) < minReworkComment {
			return fieldError("comment", "причина возврата обязательна, не короче 5 символов")
		}
	case from == StatusReview && to == StatusDone:
		if !a.manager {
			if !isReporter {
				return errForbiddenTransition
			}
			if isAssignee {
				return errAcceptOwn
			}
		}
		t.CompletedAt = &now
	case from == StatusDone && to == StatusInProgress:
		if !a.manager {
			return errForbiddenTransition
		}
		if err := reopen(tx, t, in.AssigneeID, uid, a.sprints); err != nil {
			return err
		}
		t.CompletedAt = nil
	case to == StatusCancelled:
		if !a.manager {
			return errForbiddenTransition
		}
		if comment == "" {
			return fieldError("comment", "причина отмены обязательна")
		}
	case from == StatusCancelled && to == StatusTodo:
		if !a.manager {
			return errForbiddenTransition
		}
		if err := setAssignee(tx, t, nil, uid); err != nil {
			return err
		}
		if err := moveTo(tx, t, nil, uid); err != nil {
			return err
		}
	}

	// Причина от GitHub без связанного автора (§12.4) — примечание к истории: комментарий без автора невозможен.
	systemNote := uid == uuid.Nil && comment != ""
	if comment != "" && !systemNote && (to == StatusCancelled || (from == StatusReview && to == StatusInProgress)) {
		c := Comment{TaskID: t.ID, AuthorID: uid, Body: comment, Source: commentSource(a.source)}
		if err := tx.Create(&c).Error; err != nil {
			return fmt.Errorf("task: reason comment: %w", err)
		}
	}
	t.Status = to
	note := ""
	if systemNote {
		note = comment
	}
	if err := writeHistoryNote(tx, t.ID, "status", from, to, note); err != nil {
		return err
	}
	payload := map[string]any{
		"task_id": t.ID, "key": t.Key, "title": t.Title, "project_id": t.ProjectID, "from": from, "to": to,
		"by": uid, "assignee_id": t.AssigneeID, "reporter_id": t.ReporterID,
	}
	if !samePtr(prevAssignee, t.AssigneeID) {
		// Переход сменил исполнителя (снят при возврате в todo, назначен при переоткрытии):
		// прежнему — уведомление о смене статуса, новому хватает task.assigned.
		payload["prev_assignee_id"] = prevAssignee
	}
	if comment != "" {
		payload["comment"] = comment
	}
	return events.Publish(tx, events.TaskTransitioned, payload)
}

// reopen — §4 done → in_progress: исполнитель сохраняется; деактивированного менеджер обязан
// заменить активным участником (его строка уже под FOR SHARE — см. Transition). Из закрытого спринта задача
// переходит в активный, нет активного — в бэклог. Оба спринта должны быть под FOR SHARE, взятым
// раньше задачи (Transition ставит pre.sprintCheck, locked — заблокированные): иначе одновременное
// закрытие активного спринта не увидит задачу (#160). Активный спринт не из locked — needSprintLock,
// change повторит транзакцию, взяв его.
// Запись «добавлено после старта» в журнал спринта делает moveTo.
func reopen(tx *gorm.DB, t *Task, newAssignee *uuid.UUID, by uuid.UUID, locked []uuid.UUID) error {
	if newAssignee != nil {
		if err := checkAssignee(tx, t.ProjectID, *newAssignee); err != nil {
			return err
		}
		if err := setAssignee(tx, t, newAssignee, by); err != nil {
			return err
		}
	} else if t.AssigneeID != nil {
		active, err := user.IsActive(tx, *t.AssigneeID)
		if err != nil {
			return err
		}
		if !active {
			return fieldError("assignee_id", "исполнитель деактивирован — выберите активного")
		}
	}
	if t.SprintID == nil {
		return nil
	}
	state, err := sprintdb.State(tx, *t.SprintID)
	if err != nil {
		return err
	}
	if state != sprintdb.StateClosed {
		return nil
	}
	active, err := sprintdb.ActiveID(tx, t.ProjectID)
	if err != nil {
		return err
	}
	if active != nil && !slices.Contains(locked, *active) {
		// Активный спринт нужно держать FOR SHARE раньше задачи, иначе одновременное закрытие
		// его не увидит задачу (#160): change повторит транзакцию, взяв и его.
		return needSprintLock{*active}
	}
	return moveTo(tx, t, active, by)
}

// setAssignee меняет исполнителя с записью в историю. Когда исполнителем назначают не того,
// кто действует (by), публикуется task.assigned — уведомление назначенному (§8.7);
// взятие себе публикуется как переход.
func setAssignee(tx *gorm.DB, t *Task, id *uuid.UUID, by uuid.UUID) error {
	if samePtr(t.AssigneeID, id) {
		return nil
	}
	if err := writeHistory(tx, t.ID, "assignee_id", t.AssigneeID, id); err != nil {
		return err
	}
	t.AssigneeID = id
	return publishAssigned(tx, t, by)
}

// publishAssigned — task.assigned (§8.7, §7.7), когда исполнителем назначен не тот, кто действует.
func publishAssigned(tx *gorm.DB, t *Task, by uuid.UUID) error {
	if t.AssigneeID == nil || *t.AssigneeID == by {
		return nil
	}
	return events.Publish(tx, events.TaskAssigned, map[string]any{
		"task_id": t.ID, "key": t.Key, "title": t.Title, "project_id": t.ProjectID, "assignee_id": *t.AssigneeID, "by": by,
	})
}

// moveTo — перенос в секцию (спринт или бэклог) в её конец, с записью в историю.
func moveTo(tx *gorm.DB, t *Task, sprintID *uuid.UUID, by uuid.UUID) error {
	if samePtr(t.SprintID, sprintID) {
		return nil
	}
	if err := writeHistory(tx, t.ID, "sprint_id", t.SprintID, sprintID); err != nil {
		return err
	}
	pos, err := endOfSection(tx, t.ProjectID, sprintID, &t.ID)
	if err != nil {
		return err
	}
	old := t.SprintID
	t.SprintID, t.Position = sprintID, pos
	return sprintChanged(tx, t, old, sprintID, by)
}

// sprintChanged — §5.3: перенос в активный спринт и из него после старта пишется в журнал
// спринта («добавлено/убрано после старта») и публикуется sprint.task_added / sprint.task_removed.
// by — кто действует (явно, а не из контекста: бот вызывает сервис напрямую).
func sprintChanged(tx *gorm.DB, t *Task, from, to *uuid.UUID, by uuid.UUID) error {
	actor := &by
	for _, step := range []struct {
		sprint *uuid.UUID
		action string
		event  string
	}{{from, "removed", events.SprintTaskRemoved}, {to, "added", events.SprintTaskAdded}} {
		if step.sprint == nil {
			continue
		}
		state, err := sprintdb.State(tx, *step.sprint)
		if err != nil {
			return err
		}
		if state != sprintdb.StateActive {
			continue
		}
		if err := sprintdb.Log(tx, *step.sprint, t.ID, actor, step.action); err != nil {
			return err
		}
		if err := events.Publish(tx, step.event, map[string]any{
			"sprint_id": *step.sprint, "task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "by": actor,
		}); err != nil {
			return err
		}
	}
	return nil
}

// alreadyTaken — 409 TASK_ALREADY_TAKEN «Задача TM-42 уже в работе, исполнитель: Иван Петров» (§8.4).
func alreadyTaken(tx *gorm.DB, t *Task) error {
	name := ""
	if t.AssigneeID != nil {
		var err error
		if name, _, err = user.DisplayName(tx, *t.AssigneeID); err != nil {
			return err
		}
	}
	e := apierr.Conflict(apierr.CodeTaskAlreadyTaken, fmt.Sprintf("Задача %s уже в работе, исполнитель: %s", t.Key, name))
	if t.AssigneeID != nil {
		e = e.With("assignee", map[string]any{"id": *t.AssigneeID, "display_name": name})
	}
	return e
}

// ReleaseOnDeactivation — §3.1: открытые задачи деактивируемого пользователя освобождаются —
// исполнитель очищается, in_progress возвращается в todo, в историю «снят при деактивации».
// Вызывается в транзакции деактивации; три запроса на любое число задач. Возвращает их ключи.
func ReleaseOnDeactivation(tx *gorm.DB, userID uuid.UUID) ([]string, error) {
	const note = "снят при деактивации"
	actor, source := audit.From(tx.Statement.Context)
	src := historySource(source)
	keys, err := releaseAssigned(tx, userID, actor, src, note)
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}
