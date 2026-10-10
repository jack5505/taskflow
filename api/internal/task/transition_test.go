package task_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
	"github.com/jack5505/taskflow/api/internal/user"
	"github.com/jack5505/taskflow/api/internal/users"
)

// state — задача в нужном состоянии напрямую в базе (подготовка к проверке перехода).
func (f *fixture) state(t *testing.T, reporter *user.User, status string, assignee *user.User) string {
	t.Helper()
	token := f.member
	if reporter == f.managerU {
		token = f.manager
	}
	key := f.create(t, token, `{"title":"Задача"}`)["key"].(string)
	updates := map[string]any{"status": status}
	if assignee != nil {
		updates["assignee_id"] = assignee.ID
	}
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", key).Updates(updates).Error)
	return key
}

func (f *fixture) get(t *testing.T, key string) task.Task {
	t.Helper()
	var tk task.Task
	require.NoError(t, f.db.First(&tk, "key = ?", key).Error)
	return tk
}

func (f *fixture) move(t *testing.T, token, key, body string) (*int, map[string]any) {
	t.Helper()
	rec, b := f.do(t, "POST", "/api/v1/tasks/"+key+"/transition", token, body)
	return &rec.Code, b
}

// Критерий #20: все переходы §4 — права и побочные эффекты.
func TestAllTransitions(t *testing.T) {
	f := newFixture(t)
	other, otherToken := f.user(t, false)
	f.join(t, other, project.RoleMember)
	M, U := f.memberU, f.managerU

	type tc struct {
		name     string
		reporter *user.User
		from     string
		assignee *user.User
		token    string
		body     string
		want     int
		check    func(t *testing.T, tk task.Task)
	}
	cases := []tc{
		// todo → in_progress
		{"взять свободную — любой участник, становится исполнителем", M, "todo", nil, otherToken, `{"to":"in_progress"}`, 200, func(t *testing.T, tk task.Task) {
			require.Equal(t, other.ID, *tk.AssigneeID)
			require.NotNil(t, tk.StartedAt)
		}},
		{"взять чужую — нельзя участнику", M, "todo", M, otherToken, `{"to":"in_progress"}`, 403, nil},
		{"взять назначенную себе", M, "todo", M, f.member, `{"to":"in_progress"}`, 200, nil},
		{"взять чужую — менеджер может, исполнитель сохраняется", M, "todo", other, f.manager, `{"to":"in_progress"}`, 200, func(t *testing.T, tk task.Task) {
			require.Equal(t, other.ID, *tk.AssigneeID)
		}},
		// in_progress → todo
		{"отказаться — исполнитель, исполнитель очищается", M, "in_progress", M, f.member, `{"to":"todo"}`, 200, func(t *testing.T, tk task.Task) {
			require.Nil(t, tk.AssigneeID)
		}},
		{"отказаться за другого — нельзя", M, "in_progress", M, otherToken, `{"to":"todo"}`, 403, nil},
		{"отказаться — менеджер", M, "in_progress", M, f.manager, `{"to":"todo"}`, 200, nil},
		// in_progress → review
		{"на проверку — исполнитель", M, "in_progress", other, otherToken, `{"to":"review"}`, 200, nil},
		{"на проверку — не исполнитель нельзя", M, "in_progress", other, f.member, `{"to":"review"}`, 403, nil},
		// review → in_progress
		{"вернуть — автор с причиной", M, "review", other, f.member, `{"to":"in_progress","comment":"Не работает экспорт"}`, 200, nil},
		{"вернуть — без причины 400", M, "review", other, f.member, `{"to":"in_progress","comment":" abc "}`, 400, nil},
		{"вернуть — не автор нельзя", M, "review", other, otherToken, `{"to":"in_progress","comment":"Своё возвращаю"}`, 403, nil},
		{"вернуть свою — автор-исполнитель может", M, "review", M, f.member, `{"to":"in_progress","comment":"Доделаю тесты"}`, 200, nil},
		// review → done
		{"принять — автор, не исполнитель", M, "review", other, f.member, `{"to":"done"}`, 200, func(t *testing.T, tk task.Task) {
			require.NotNil(t, tk.CompletedAt)
		}},
		{"принять свою — автор-исполнитель нельзя", M, "review", M, f.member, `{"to":"done"}`, 403, nil},
		{"принять — менеджер", M, "review", M, f.manager, `{"to":"done"}`, 200, nil},
		{"принять — посторонний участник нельзя", M, "review", M, otherToken, `{"to":"done"}`, 403, nil},
		// done → in_progress
		{"переоткрыть — менеджер, completed_at очищается", M, "done", M, f.manager, `{"to":"in_progress"}`, 200, func(t *testing.T, tk task.Task) {
			require.Nil(t, tk.CompletedAt)
			require.Equal(t, M.ID, *tk.AssigneeID)
		}},
		{"переоткрыть — исполнитель нельзя", M, "done", M, f.member, `{"to":"in_progress"}`, 403, nil},
		// → cancelled
		{"отменить из todo — менеджер с причиной", M, "todo", other, f.manager, `{"to":"cancelled","comment":"Не актуально"}`, 200, func(t *testing.T, tk task.Task) {
			require.Equal(t, other.ID, *tk.AssigneeID, "исполнитель сохраняется")
		}},
		{"отменить из in_progress", M, "in_progress", other, f.manager, `{"to":"cancelled","comment":"Дубль"}`, 200, nil},
		{"отменить из review", M, "review", other, f.manager, `{"to":"cancelled","comment":"Дубль"}`, 200, nil},
		{"отменить без причины — 400", M, "todo", nil, f.manager, `{"to":"cancelled"}`, 400, nil},
		{"отменить — участник нельзя", M, "todo", nil, f.member, `{"to":"cancelled","comment":"Сам отменю"}`, 403, nil},
		// cancelled → todo
		{"вернуть в бэклог — менеджер, исполнитель очищается", M, "cancelled", other, f.manager, `{"to":"todo"}`, 200, func(t *testing.T, tk task.Task) {
			require.Nil(t, tk.AssigneeID)
			require.Nil(t, tk.SprintID)
		}},
		{"вернуть в бэклог — участник нельзя", M, "cancelled", nil, f.member, `{"to":"todo"}`, 403, nil},
		// недопустимые
		{"done → todo — 409", M, "done", M, f.manager, `{"to":"todo"}`, 409, nil},
		{"todo → review — 409", M, "todo", M, f.manager, `{"to":"review"}`, 409, nil},
		{"cancelled → in_progress — 409", M, "cancelled", nil, f.admin, `{"to":"in_progress"}`, 409, nil},
		{"администратор — как менеджер", U, "review", U, f.admin, `{"to":"done"}`, 200, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key := f.state(t, c.reporter, c.from, c.assignee)
			before := f.get(t, key)
			code, body := f.move(t, c.token, key, c.body)
			require.Equal(t, c.want, *code, "%v", body)
			after := f.get(t, key)
			if c.want != http.StatusOK {
				require.Equal(t, c.from, after.Status, "отказ ничего не меняет")
				require.Equal(t, before.Version, after.Version)
				if c.want == http.StatusConflict {
					require.Equal(t, apierr.CodeInvalidTransition, errCode(body))
				}
				return
			}
			require.NotEqual(t, c.from, after.Status)
			require.Equal(t, before.Version+1, after.Version, "version растёт")
			if c.check != nil {
				c.check(t, after)
			}
			var n int64
			require.NoError(t, f.db.Raw(`SELECT count(*) FROM task_history WHERE task_id = ? AND field = 'status'
				AND old_value = to_jsonb(?::text) AND new_value = to_jsonb(?::text)`, after.ID, c.from, after.Status).Scan(&n).Error)
			require.EqualValues(t, 1, n, "переход записан в историю")
		})
	}
}

// Причина возврата и отмены сохраняется комментарием.
func TestReasonBecomesComment(t *testing.T) {
	f := newFixture(t)
	key := f.state(t, f.memberU, "review", f.managerU)
	code, _ := f.move(t, f.member, key, `{"to":"in_progress","comment":"  Падает экспорт CSV  "}`)
	require.Equal(t, 200, *code)
	var body, source string
	require.NoError(t, f.db.Raw(`SELECT c.body, c.source FROM comments c JOIN tasks t ON t.id = c.task_id WHERE t.key = ?`, key).Row().Scan(&body, &source))
	require.Equal(t, "Падает экспорт CSV", body)
	require.Equal(t, "web", source)
}

// §4: переоткрытие из закрытого спринта — в активный, нет активного — в бэклог;
// деактивированного исполнителя менеджер обязан заменить.
func TestReopen(t *testing.T) {
	f := newFixture(t)
	closed := f.sprint(t, f.p.ID, "closed")
	key := f.state(t, f.memberU, "done", f.memberU)
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", key).Update("sprint_id", closed).Error)

	code, _ := f.move(t, f.manager, key, `{"to":"in_progress"}`)
	require.Equal(t, 200, *code)
	require.Nil(t, f.get(t, key).SprintID, "активного нет — в бэклог")

	active := f.sprint(t, f.p.ID, "active")
	key2 := f.state(t, f.memberU, "done", f.memberU)
	require.NoError(t, f.db.Model(&task.Task{}).Where("key = ?", key2).Update("sprint_id", closed).Error)
	code, _ = f.move(t, f.manager, key2, `{"to":"in_progress"}`)
	require.Equal(t, 200, *code)
	require.Equal(t, active, *f.get(t, key2).SprintID, "в активный спринт")

	gone, _ := f.user(t, false)
	f.join(t, gone, project.RoleMember)
	key3 := f.state(t, f.memberU, "done", gone)
	require.NoError(t, f.db.Model(gone).Update("is_active", false).Error)
	code, body := f.move(t, f.manager, key3, `{"to":"in_progress"}`)
	require.Equal(t, 400, *code)
	require.Equal(t, "assignee_id", field(t, body))
	code, _ = f.move(t, f.manager, key3, `{"to":"in_progress","assignee_id":"`+f.memberU.ID.String()+`"}`)
	require.Equal(t, 200, *code)
	require.Equal(t, f.memberU.ID, *f.get(t, key3).AssigneeID)
	var assigned int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'task.assigned' AND payload->>'key' = ?`, key3).Scan(&assigned).Error)
	require.EqualValues(t, 1, assigned, "новому исполнителю уйдёт уведомление")
}

func TestTake(t *testing.T) {
	f := newFixture(t)
	other, otherToken := f.user(t, false)
	f.join(t, other, project.RoleMember)
	take := func(token, key string) (int, map[string]any) {
		rec, b := f.do(t, "POST", "/api/v1/tasks/"+key+"/take", token, "")
		return rec.Code, b
	}

	key := f.state(t, f.memberU, "todo", nil)
	code, b := take(f.member, key)
	require.Equal(t, 200, code, "%v", b)
	require.Equal(t, "in_progress", b["status"])
	require.Equal(t, f.memberU.ID.String(), b["assignee_id"])
	code, _ = take(f.member, key)
	require.Equal(t, 200, code, "повторное нажатие тем же — без изменений")
	require.EqualValues(t, 2, f.get(t, key).Version)

	code, b = take(otherToken, key)
	require.Equal(t, 409, code)
	require.Equal(t, apierr.CodeTaskAlreadyTaken, errCode(b))
	require.Contains(t, b["error"].(map[string]any)["message"], "уже в работе, исполнитель: Пользователь")

	assigned := f.state(t, f.memberU, "todo", f.memberU)
	code, b = take(otherToken, assigned)
	require.Equal(t, 409, code, "назначенную на другого не взять")
	require.Equal(t, apierr.CodeTaskAlreadyTaken, errCode(b))
	code, b = take(f.manager, assigned)
	require.Equal(t, 409, code, "и менеджеру — переназначают через PATCH")
	require.Equal(t, apierr.CodeTaskAlreadyTaken, errCode(b))

	review := f.state(t, f.memberU, "review", f.memberU)
	code, b = take(f.member, review)
	require.Equal(t, 409, code)
	require.Equal(t, apierr.CodeInvalidTransition, errCode(b), "на проверке — не «уже в работе»")
	require.Contains(t, b["error"].(map[string]any)["message"], "в статусе На проверке")

	orphan := f.state(t, f.memberU, "in_progress", nil)
	code, b = take(otherToken, orphan)
	require.Equal(t, 200, code, "в работе без исполнителя — берёт любой участник: %v", b)
	require.Equal(t, other.ID.String(), b["assignee_id"])
	require.Equal(t, "in_progress", b["status"])

	free := f.state(t, f.memberU, "todo", nil)
	code, _ = take(f.admin, free)
	require.Equal(t, 403, code, "администратор — не участник, исполнителем не станет")
	adminMove, _ := f.move(t, f.admin, free, `{"to":"in_progress"}`)
	require.Equal(t, 403, *adminMove)

	done := f.state(t, f.memberU, "done", f.memberU)
	code, b = take(otherToken, done)
	require.Equal(t, 409, code)
	require.Equal(t, apierr.CodeInvalidTransition, errCode(b))
	require.Contains(t, b["error"].(map[string]any)["message"], "в статусе Готово, взять нельзя")

	code, _ = take(f.outsider, key)
	require.Equal(t, 404, code)
	code, _ = take(f.member, f.p.Key+"-9999")
	require.Equal(t, 404, code)

	archived := f.state(t, f.memberU, "todo", nil)
	require.NoError(t, f.db.Model(f.p).Update("is_archived", true).Error)
	code, b = take(f.member, archived)
	require.Equal(t, 409, code)
	require.Equal(t, apierr.CodeProjectArchived, errCode(b))
}

// Критерий #20: 20 горутин одновременно берут задачу — ровно одна успешна.
func TestConcurrentTakeExactlyOneWins(t *testing.T) {
	f := newFixture(t)
	key := f.state(t, f.memberU, "todo", nil)
	svc := task.NewService(f.db, project.NewService(f.db))
	const n = 20
	takers := make([]*user.User, n)
	for i := range n {
		takers[i], _ = f.user(t, false)
		f.join(t, takers[i], project.RoleMember)
	}
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = svc.Take(context.Background(), takers[i], key)
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	for _, err := range errs {
		if err == nil {
			winners++
			continue
		}
		require.Equal(t, apierr.CodeTaskAlreadyTaken, apierr.From(err).Code, err.Error())
	}
	require.Equal(t, 1, winners)
	tk := f.get(t, key)
	require.Equal(t, task.StatusInProgress, tk.Status)
	var transitions int64
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM outbox_events WHERE type = 'task.transitioned' AND payload->>'key' = ?`, key).Scan(&transitions).Error)
	require.EqualValues(t, 1, transitions, "в группу уйдёт ровно одно сообщение")
}

// §3.1: при деактивации открытые задачи освобождаются, в истории «снят при деактивации».
func TestDeactivationReleasesTasks(t *testing.T) {
	f := newFixture(t)
	gone, _ := f.user(t, false)
	f.join(t, gone, project.RoleMember)
	inProgress := f.state(t, f.memberU, "in_progress", gone)
	todo := f.state(t, f.memberU, "todo", gone)
	done := f.state(t, f.memberU, "done", gone)

	off := false
	authSvc := auth.NewService(&config.Config{}, f.db, nil)
	_, err := users.NewService(f.db, authSvc).Update(context.Background(), gone.ID, users.UpdateInput{IsActive: &off})
	require.NoError(t, err)

	ip := f.get(t, inProgress)
	require.Equal(t, task.StatusTodo, ip.Status)
	require.Nil(t, ip.AssigneeID)
	require.Nil(t, f.get(t, todo).AssigneeID)
	require.Equal(t, gone.ID, *f.get(t, done).AssigneeID, "закрытые задачи не трогаются")

	var notes []string
	require.NoError(t, f.db.Raw(`SELECT note FROM task_history WHERE task_id = ? AND note IS NOT NULL ORDER BY created_at`, ip.ID).Scan(&notes).Error)
	require.Equal(t, []string{"снят при деактивации", "снят при деактивации"}, notes, "исполнитель и статус")

	var released []string
	require.NoError(t, f.db.Raw(`SELECT jsonb_array_elements_text(payload->'released_tasks') FROM outbox_events
		WHERE type = 'user.deactivated' AND payload->>'user_id' = ?`, gone.ID.String()).Scan(&released).Error)
	require.ElementsMatch(t, []string{inProgress, todo}, released)
}

// Ревью #85: смена статуса своей задачи и деактивация того же пользователя одновременно —
// без deadlock: деактивация проходит, переход либо успевает, либо получает 401.
func TestTransitionVsDeactivationNoDeadlock(t *testing.T) {
	f := newFixture(t)
	svc := task.NewService(f.db, project.NewService(f.db))
	usersSvc := users.NewService(f.db, auth.NewService(&config.Config{}, f.db, nil))
	off := false
	for range 20 {
		u, _ := f.user(t, false)
		f.join(t, u, project.RoleMember)
		key := f.state(t, f.memberU, "in_progress", u)
		var wg sync.WaitGroup
		var moveErr, deactErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, moveErr = svc.Transition(context.Background(), u, key, task.TransitionInput{To: task.StatusReview})
		}()
		go func() {
			defer wg.Done()
			_, deactErr = usersSvc.Update(context.Background(), u.ID, users.UpdateInput{IsActive: &off})
		}()
		wg.Wait()
		require.NoError(t, deactErr)
		if moveErr != nil {
			require.Equal(t, apierr.CodeUnauthorized, apierr.From(moveErr).Code, moveErr.Error())
		}
		tk := f.get(t, key)
		require.Nil(t, tk.AssigneeID, "деактивация освободила задачу")
	}
}

func TestReasonLengthCheckedByService(t *testing.T) {
	f := newFixture(t)
	key := f.state(t, f.memberU, "todo", nil)
	_, err := task.NewService(f.db, project.NewService(f.db)).Transition(context.Background(), f.managerU, key,
		task.TransitionInput{To: task.StatusCancelled, Comment: strings.Repeat("я", 5001)})
	require.Equal(t, apierr.CodeValidation, apierr.From(err).Code)
}
