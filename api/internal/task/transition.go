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
	"github.com/jack5505/taskflow/api/internal/user"
)

// statusNames — названия статусов для сообщений (как в интерфейсе и боте).
var statusNames = map[string]string{
	StatusTodo: "К выполнению", StatusInProgress: "В работе", StatusReview: "На проверке",
	StatusDone: "Готово", StatusCancelled: "Отменена",
}

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
}

// Transition — POST /tasks/{key}/transition (§4). Строка задачи блокируется FOR UPDATE.
// Нового исполнителя (для переоткрытия) блокируем заранее; проверяет его reopen.
func (s *Service) Transition(ctx context.Context, u *user.User, key string, in TransitionInput) (*Task, error) {
	var pre preLock
	if in.AssigneeID != nil {
		pre.users = []uuid.UUID{*in.AssigneeID}
	}
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
func openTask(tx *gorm.DB, u *user.User, key string, pre preLock, lock string) (*opened, error) {
	var me []struct {
		IsSystemAdmin bool
		IsActive      bool
	}
	if err := tx.Raw(`SELECT is_system_admin, is_active FROM users WHERE id = ? FOR SHARE`, u.ID).Scan(&me).Error; err != nil {
		return nil, fmt.Errorf("task: actor: %w", err)
	}
	if len(me) == 0 || !me[0].IsActive {
		return nil, errUserBlocked
	}
	if err := lockShare(tx, "users", pre.users); err != nil {
		return nil, err
	}
	if err := lockShare(tx, "sprints", pre.sprints); err != nil {
		return nil, err
	}
	if pre.reorder {
		// Проект берём из ключа задачи: блокировка нужна раньше строки задачи.
		projectKey := keyRe.FindStringSubmatch(key)[1]
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('task-order:' || ?))`, projectKey).Error; err != nil {
			return nil, fmt.Errorf("task: order lock: %w", err)
		}
	}
	var o opened
	cond := " AND deleted_at IS NULL"
	if pre.withDeleted {
		cond = ""
	}
	res := tx.Raw(`SELECT * FROM tasks WHERE key = ?`+cond+` `+lock, key).Scan(&o.task)
	if res.Error != nil {
		return nil, fmt.Errorf("task: lock: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	pr := tx.Raw(`SELECT * FROM projects WHERE id = ? AND deleted_at IS NULL FOR SHARE`, o.task.ProjectID).Scan(&o.project)
	if pr.Error != nil {
		return nil, fmt.Errorf("task: project: %w", pr.Error)
	}
	if pr.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	role, err := project.RoleIn(tx, o.project.ID, u.ID)
	if err != nil {
		return nil, err
	}
	admin := me[0].IsSystemAdmin
	if role == "" && !admin {
		return nil, ErrNotFound
	}
	if o.task.DeletedAt != nil && !admin {
		return nil, ErrNotFound // корзину видит только администратор (§2) — раньше любых других ответов
	}
	_, source := audit.From(tx.Statement.Context)
	o.actor = actor{user: u, member: role != "", admin: admin, manager: admin || role == project.RoleManager, source: source}
	return &o, nil
}

// change — общая часть изменений задачи. Порядок блокировок тот же, что у деактивации и закрытия
// спринта: FOR SHARE строк действующего пользователя и pre, затем FOR UPDATE строки задачи,
// затем FOR SHARE проекта. Права берутся из строк под блокировкой, а не из снимка при входе.
// ifMatch — версии из If-Match: nil — без сверки, иначе версия задачи должна быть в списке.
// После fn — остановка таймеров, которым больше нельзя идти на задаче (§6.4), version+1 и сохранение; fn, вернувший errNoChange, ничего не меняет — тогда
// возвращается задача, какой она была до fn.
func (s *Service) change(ctx context.Context, u *user.User, key string, ifMatch []int, pre preLock,
	fn func(*gorm.DB, *Task, actor) error) (*Task, error) {
	if keyRe.FindStringSubmatch(key) == nil {
		return nil, ErrNotFound
	}
	var t, before Task
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, err := openTask(tx, u, key, pre, "FOR UPDATE")
		if err != nil {
			return err
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
	if errors.Is(err, errNoChange) {
		return &before, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// applyTransition — правила и побочные эффекты одного перехода §4.
func applyTransition(tx *gorm.DB, t *Task, a actor, in TransitionInput) error {
	from, to := t.Status, in.To
	if _, ok := statusNames[to]; !ok {
		return fieldError("to", "todo, in_progress, review, done или cancelled")
	}
	if !transitions[from][to] {
		return ErrInvalidTransition(from, to)
	}
	uid := a.user.ID
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
		if err := reopen(tx, t, in.AssigneeID, uid); err != nil {
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

	if comment != "" && (to == StatusCancelled || (from == StatusReview && to == StatusInProgress)) {
		c := Comment{TaskID: t.ID, AuthorID: uid, Body: comment, Source: commentSource(a.source)}
		if err := tx.Create(&c).Error; err != nil {
			return fmt.Errorf("task: reason comment: %w", err)
		}
	}
	t.Status = to
	if err := writeHistory(tx, t.ID, "status", from, to); err != nil {
		return err
	}
	payload := map[string]any{
		"task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "from": from, "to": to,
		"by": uid, "assignee_id": t.AssigneeID, "reporter_id": t.ReporterID,
	}
	if comment != "" {
		payload["comment"] = comment
	}
	return events.Publish(tx, events.TaskTransitioned, payload)
}

// reopen — §4 done → in_progress: исполнитель сохраняется; деактивированного менеджер обязан
// заменить активным участником (его строка уже под FOR SHARE — см. Transition). Из закрытого спринта задача
// переходит в активный, нет активного — в бэклог. Спринт читается без блокировки: закрытие спринта
// блокирует спринт, затем его задачи — встречная блокировка отсюда дала бы deadlock; в худшем случае
// задача попадёт в только что закрытый спринт и уйдёт при следующем переносе.
// Запись «добавлено после старта» в журнал спринта делает moveTo.
func reopen(tx *gorm.DB, t *Task, newAssignee *uuid.UUID, by uuid.UUID) error {
	if newAssignee != nil {
		if err := checkAssignee(tx, t.ProjectID, *newAssignee); err != nil {
			return err
		}
		if err := setAssignee(tx, t, newAssignee, by); err != nil {
			return err
		}
	} else if t.AssigneeID != nil {
		var active bool
		if err := tx.Raw(`SELECT is_active FROM users WHERE id = ?`, *t.AssigneeID).Scan(&active).Error; err != nil {
			return fmt.Errorf("task: assignee: %w", err)
		}
		if !active {
			return fieldError("assignee_id", "исполнитель деактивирован — выберите активного")
		}
	}
	if t.SprintID == nil {
		return nil
	}
	var state string
	if err := tx.Raw(`SELECT state FROM sprints WHERE id = ?`, *t.SprintID).Scan(&state).Error; err != nil {
		return fmt.Errorf("task: sprint: %w", err)
	}
	if state != "closed" {
		return nil
	}
	var ids []string
	if err := tx.Raw(`SELECT id::text FROM sprints WHERE project_id = ? AND state = 'active' LIMIT 1`, t.ProjectID).Scan(&ids).Error; err != nil {
		return fmt.Errorf("task: active sprint: %w", err)
	}
	if len(ids) == 0 {
		return moveTo(tx, t, nil, by)
	}
	active := uuid.MustParse(ids[0])
	return moveTo(tx, t, &active, by)
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
	if id != nil && *id != by {
		return events.Publish(tx, events.TaskAssigned, map[string]any{
			"task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "assignee_id": *id, "by": by,
		})
	}
	return nil
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
		var state string
		if err := tx.Raw(`SELECT state FROM sprints WHERE id = ?`, *step.sprint).Scan(&state).Error; err != nil {
			return fmt.Errorf("task: sprint state: %w", err)
		}
		if state != "active" {
			continue
		}
		if err := tx.Exec(`INSERT INTO sprint_log (sprint_id, task_id, user_id, action) VALUES (?, ?, ?, ?)`,
			*step.sprint, t.ID, actor, step.action).Error; err != nil {
			return fmt.Errorf("task: sprint log: %w", err)
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
		if err := tx.Raw(`SELECT display_name FROM users WHERE id = ?`, *t.AssigneeID).Scan(&name).Error; err != nil {
			return fmt.Errorf("task: assignee name: %w", err)
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
	open := `FROM tasks WHERE assignee_id = ? AND deleted_at IS NULL AND status NOT IN ('done', 'cancelled')`
	if err := tx.Exec(`INSERT INTO task_history (task_id, user_id, field, old_value, new_value, source, note)
		SELECT id, ?, 'assignee_id', to_jsonb(assignee_id::text), NULL, ?, ? `+open+` ORDER BY id`,
		actor, src, note, userID).Error; err != nil {
		return nil, fmt.Errorf("task: release history: %w", err)
	}
	if err := tx.Exec(`INSERT INTO task_history (task_id, user_id, field, old_value, new_value, source, note)
		SELECT id, ?, 'status', '"in_progress"'::jsonb, '"todo"'::jsonb, ?, ? `+open+` AND status = 'in_progress' ORDER BY id`,
		actor, src, note, userID).Error; err != nil {
		return nil, fmt.Errorf("task: release history: %w", err)
	}
	var keys []string
	if err := tx.Raw(`UPDATE tasks SET assignee_id = NULL, version = version + 1, updated_at = now(),
			status = CASE WHEN status = 'in_progress' THEN 'todo' ELSE status END
		WHERE assignee_id = ? AND deleted_at IS NULL AND status NOT IN ('done', 'cancelled')
		RETURNING key`, userID).Scan(&keys).Error; err != nil {
		return nil, fmt.Errorf("task: release: %w", err)
	}
	sort.Strings(keys)
	return keys, nil
}
