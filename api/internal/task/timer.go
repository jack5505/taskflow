package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/user"
)

// TimerLimit — §6.4: таймер дольше 12 часов останавливается на отметке 12 часов.
const TimerLimit = 12 * time.Hour

var (
	ErrNoTimer          = apierr.NotFound("Таймер не запущен")
	errTimerForbidden   = apierr.Forbidden("Таймер запускает исполнитель задачи")
	errTimerNotProgress = apierr.Conflict(apierr.CodeInvalidTransition, "Таймер запускается только на задаче в работе")
)

// ActiveTimer — строка active_timers (§3.8): запущенный таймер пользователя.
type ActiveTimer struct {
	UserID    uuid.UUID `gorm:"type:uuid;primaryKey"`
	TaskID    uuid.UUID `gorm:"type:uuid"`
	StartedAt time.Time
}

func (ActiveTimer) TableName() string { return "active_timers" }

// TimerView — запущенный таймер в ответе (GET /me, старт): задача и прошедшее время.
type TimerView struct {
	TaskID         uuid.UUID `json:"task_id"`
	TaskKey        string    `json:"task_key" example:"TM-42"`
	Title          string    `json:"title"`
	StartedAt      time.Time `json:"started_at"`
	ElapsedSeconds int64     `json:"elapsed_seconds" example:"2820"`
}

// TimerStop — итог остановки: записи времени (две — если таймер шёл через полночь), сколько
// записано, обрезана ли запись по дневному лимиту и остановлен ли таймер на отметке 12 часов.
type TimerStop struct {
	TaskKey     string        `json:"task_key" example:"TM-42"`
	Minutes     int           `json:"minutes" example:"47"`
	Duration    string        `json:"duration" example:"47m"`
	Worklogs    []WorklogView `json:"worklogs"`
	Trimmed     bool          `json:"trimmed"`
	AutoStopped bool          `json:"auto_stopped"`
}

// TimerStart — ответ запуска: новый таймер и итог остановки прежнего, если он шёл на другой задаче.
type TimerStart struct {
	Timer   TimerView  `json:"timer"`
	Stopped *TimerStop `json:"stopped"`
}

// StartTimer — POST /tasks/{key}/timer/start (§6.4): только исполнитель задачи в работе.
// Таймер на другой задаче останавливается с записью времени в той же транзакции; повторный
// старт на той же задаче — 409 TIMER_ALREADY_RUNNING. Порядок ответов: 404 → 403 → 409.
func (s *Service) StartTimer(ctx context.Context, u *user.User, key string) (*TimerStart, error) {
	if keyRe.FindStringSubmatch(key) == nil {
		return nil, ErrNotFound
	}
	var res *TimerStart
	err := s.timerTx(ctx, func(tx *gorm.DB) error {
		res = nil
		now := s.now()
		// Порядок блокировок: пользователь → таймеры пользователя (advisory) → задача прежнего
		// таймера → строка таймера → новая задача (openTask) → проект.
		if err := lockTimerOwner(tx, u.ID); err != nil {
			return err
		}
		cur, curTask, err := lockUserTimer(tx, u.ID)
		if err != nil {
			return err
		}
		o, err := openTask(tx, u, key, preLock{}, "FOR SHARE")
		if err != nil {
			return err
		}
		t := &o.task
		if t.AssigneeID == nil || *t.AssigneeID != u.ID {
			return errTimerForbidden
		}
		if err := project.EnsureWritable(&o.project); err != nil {
			return err
		}
		if t.Status != StatusInProgress {
			return errTimerNotProgress
		}
		res = &TimerStart{}
		if cur != nil {
			// Таймер старше 12 часов воркер ещё не погасил — гасим здесь и запускаем заново.
			if cur.TaskID == t.ID && now.Before(cur.StartedAt.Add(TimerLimit)) {
				return apierr.Conflict(apierr.CodeTimerAlreadyRunning, fmt.Sprintf("Таймер уже запущен на %s", t.Key)).
					With("timer", timerView(cur, curTask, now))
			}
			if res.Stopped, err = stopTimer(tx, cur, curTask, now); err != nil {
				return err
			}
		}
		timer := ActiveTimer{UserID: u.ID, TaskID: t.ID, StartedAt: now}
		if err := tx.Create(&timer).Error; err != nil {
			return fmt.Errorf("task: timer start: %w", err)
		}
		res.Timer = timerView(&timer, t, now)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// StopTimer — POST /me/timer/stop: остановить свой таймер с записью времени (§6.4).
// Нет запущенного — 404.
func (s *Service) StopTimer(ctx context.Context, u *user.User) (*TimerStop, error) {
	var res *TimerStop
	err := s.timerTx(ctx, func(tx *gorm.DB) error {
		if err := lockTimerOwner(tx, u.ID); err != nil {
			return err
		}
		cur, t, err := lockUserTimer(tx, u.ID)
		if err != nil {
			return err
		}
		if cur == nil {
			return ErrNoTimer
		}
		res, err = stopTimer(tx, cur, t, s.now())
		return err
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// CurrentTimer — запущенный таймер пользователя для GET /me; нет — nil.
func CurrentTimer(ctx context.Context, db *gorm.DB, userID uuid.UUID, now time.Time) (*TimerView, error) {
	var rows []struct {
		ActiveTimer
		Key   string
		Title string
	}
	if err := db.WithContext(ctx).Raw(`SELECT a.*, t.key, t.title FROM active_timers a JOIN tasks t ON t.id = a.task_id
		WHERE a.user_id = ?`, userID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: current timer: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	r := rows[0]
	v := timerView(&r.ActiveTimer, &Task{Key: r.Key, Title: r.Title}, now)
	return &v, nil
}

// StopStaleTimers — воркер (§6.4): останавливает таймеры старше 12 часов на отметке 12 часов
// и таймеры, которые больше не могут идти: задача удалена или не в работе, пользователь не
// исполнитель или деактивирован, проект в архиве или удалён (страховка для изменений мимо
// change(), например исключения из проекта). Ошибка одного пользователя не мешает остальным:
// обход идёт дальше, ошибки возвращаются вместе. Возвращает число остановленных.
func (s *Service) StopStaleTimers(ctx context.Context) (int64, error) {
	now := s.now()
	db := s.db.WithContext(ctx)
	rows, err := timerRows(db, "", nil)
	if err != nil {
		return 0, err
	}
	var userIDs, projectIDs []uuid.UUID
	for _, r := range rows {
		userIDs, projectIDs = append(userIDs, r.UserID), append(projectIDs, r.ProjectID)
	}
	active, err := user.ActiveAmong(db, userIDs)
	if err != nil {
		return 0, err
	}
	open, err := project.LiveBriefs(db, projectIDs, true)
	if err != nil {
		return 0, err
	}
	var stale []uuid.UUID
	for _, r := range rows {
		_, projectOpen := open[r.ProjectID]
		if !r.StartedAt.After(now.Add(-TimerLimit)) || r.Deleted || r.Status != StatusInProgress ||
			r.AssigneeID == nil || *r.AssigneeID != r.UserID || !active[r.UserID] || !projectOpen {
			stale = append(stale, r.UserID)
		}
	}
	return s.stopTimersOf(ctx, now, stale)
}

// timerRow — запущенный таймер и его задача.
type timerRow struct {
	UserID     uuid.UUID
	StartedAt  time.Time
	Deleted    bool
	Status     string
	AssigneeID *uuid.UUID
	ProjectID  uuid.UUID
}

// timerRows — запущенные таймеры с их задачами (where — условие по задаче t), по пользователю.
func timerRows(db *gorm.DB, where string, arg any) ([]timerRow, error) {
	q, args := `SELECT a.user_id, a.started_at, t.deleted_at IS NOT NULL AS deleted, t.status, t.assignee_id, t.project_id
		FROM active_timers a JOIN tasks t ON t.id = a.task_id`, []any{}
	if where != "" {
		q, args = q+` WHERE `+where, append(args, arg)
	}
	var rows []timerRow
	if err := db.Raw(q+` ORDER BY a.user_id`, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: timers: %w", err)
	}
	return rows, nil
}

// StopProjectTimers — таймеры проекта при его архивации и удалении (§3.2): воркер по событию
// project.archived / project.deleted, время записи — до момента at из события, а не до обработки.
// Остановка в самой транзакции архивации невозможна без deadlock: она держит проект FOR UPDATE
// раньше задач, а операции над задачами берут задачу раньше проекта.
func (s *Service) StopProjectTimers(ctx context.Context, projectID uuid.UUID, at time.Time) (int64, error) {
	rows, err := timerRows(s.db.WithContext(ctx), `t.project_id = ?`, projectID)
	if err != nil {
		return 0, err
	}
	users := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		users[i] = r.UserID
	}
	return s.stopTimersOf(ctx, at, users)
}

// stopTimersOf — остановка таймеров пользователей users в отдельной транзакции на каждого; под
// блокировкой условие «таймер больше не может идти» проверяется заново (timerStale).
func (s *Service) stopTimersOf(ctx context.Context, at time.Time, users []uuid.UUID) (int64, error) {
	var n int64
	var errs []error
	for _, id := range users {
		stopped := false
		err := s.timerTx(ctx, func(tx *gorm.DB) error {
			stopped = false
			cur, t, err := lockUserTimer(tx, id)
			if err != nil || cur == nil {
				return err
			}
			if stale, err := timerStale(tx, cur, t, at); err != nil || !stale {
				return err
			}
			_, err = stopTimer(tx, cur, t, at)
			stopped = err == nil
			return err
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("пользователь %s: %w", id, err))
		} else if stopped {
			n++
		}
	}
	return n, errors.Join(errs...)
}

// timerStale — таймер старше 12 часов или больше не может идти (см. StopStaleTimers).
func timerStale(tx *gorm.DB, timer *ActiveTimer, t *Task, now time.Time) (bool, error) {
	if !timer.StartedAt.After(now.Add(-TimerLimit)) || t.DeletedAt != nil || t.Status != StatusInProgress ||
		t.AssigneeID == nil || *t.AssigneeID != timer.UserID {
		return true, nil
	}
	active, err := user.IsActive(tx, timer.UserID)
	if err != nil || !active {
		return !active, err
	}
	open, err := project.LiveBriefs(tx, []uuid.UUID{t.ProjectID}, true)
	if err != nil {
		return false, err
	}
	return len(open) == 0, nil
}

// StopUserTimer — остановка таймера пользователя внутри чужой транзакции (деактивация §3.1).
// Вызывающий держит строку пользователя; задачи таймера блокируются раньше строки таймера.
func StopUserTimer(tx *gorm.DB, userID uuid.UUID, at time.Time) error {
	cur, t, err := lockUserTimer(tx, userID)
	if err != nil || cur == nil {
		return err
	}
	_, err = stopTimer(tx, cur, t, at)
	return err
}

// stopStaleTaskTimers — в change(): после изменения задачи на ней может идти только таймер
// исполнителя задачи в работе (§6.4: уход из in_progress, смена исполнителя и удаление
// останавливают таймер). Строка задачи уже под FOR UPDATE.
func stopStaleTaskTimers(tx *gorm.DB, t *Task, at time.Time) error {
	keep := uuid.Nil
	if t.Status == StatusInProgress && t.DeletedAt == nil && t.AssigneeID != nil {
		keep = *t.AssigneeID
	}
	var timers []ActiveTimer
	if err := tx.Raw(`SELECT * FROM active_timers WHERE task_id = ? AND user_id <> ? ORDER BY user_id FOR UPDATE`,
		t.ID, keep).Scan(&timers).Error; err != nil {
		return fmt.Errorf("task: task timers: %w", err)
	}
	for i := range timers {
		if _, err := stopTimer(tx, &timers[i], t, at); err != nil {
			return err
		}
	}
	return nil
}

// lockTimerOwner — блокировка действующего пользователя (FOR SHARE, как в openTask — раньше
// любых задач) и advisory-блокировка его таймера: два одновременных старта одного пользователя
// идут по очереди и не создают второй таймер.
func lockTimerOwner(tx *gorm.DB, userID uuid.UUID) error {
	me, err := user.LockActor(tx, userID)
	if err != nil {
		return err
	}
	if me == nil || !me.IsActive {
		return errUserBlocked
	}
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('timer:' || ?))`, userID.String()).Error; err != nil {
		return fmt.Errorf("task: timer lock: %w", err)
	}
	return nil
}

// lockUserTimer — таймер пользователя под блокировкой вместе с его задачей. Порядок «задача →
// строка таймера», как в change(): id задачи читается без блокировки, задача берётся FOR UPDATE
// (остановка меняет spent_minutes), затем строка таймера; если таймер за это время перезапустили
// на другой задаче — повтор. Нет таймера — nil.
func lockUserTimer(tx *gorm.DB, userID uuid.UUID) (*ActiveTimer, *Task, error) {
	for range 3 {
		var ids []uuid.UUID
		if err := tx.Raw(`SELECT task_id FROM active_timers WHERE user_id = ?`, userID).Scan(&ids).Error; err != nil {
			return nil, nil, fmt.Errorf("task: timer: %w", err)
		}
		if len(ids) == 0 {
			return nil, nil, nil
		}
		var t Task
		if err := tx.Raw(`SELECT * FROM tasks WHERE id = ? FOR UPDATE`, ids[0]).Scan(&t).Error; err != nil {
			return nil, nil, fmt.Errorf("task: timer task: %w", err)
		}
		var timers []ActiveTimer
		if err := tx.Raw(`SELECT * FROM active_timers WHERE user_id = ? FOR UPDATE`, userID).Scan(&timers).Error; err != nil {
			return nil, nil, fmt.Errorf("task: timer lock: %w", err)
		}
		if len(timers) == 0 {
			return nil, nil, nil
		}
		if timers[0].TaskID == t.ID {
			return &timers[0], &t, nil
		}
	}
	return nil, nil, errors.New("task: timer: таймер всё время перезапускается")
}

// stopTimer — остановка (§6.4): строка таймера удаляется, время до at (не дальше 12 часов от
// старта — тогда timer.auto_stopped) делится по полуночам пояса пользователя, каждая часть
// округляется вверх до минуты и пишется записью source = timer. Меньше минуты всего — записи нет.
// Часть, не влезающая в дневной лимит 24 часа (§6.3), обрезается до остатка (timer.trimmed);
// остатка нет — запись не создаётся. Архив, закрытый спринт и отмена задачи не проверяются:
// время уже отработано, а таймер в таком состоянии остаться не может — его останавливают раньше.
// Задача t — под FOR UPDATE, строка таймера — тоже.
func stopTimer(tx *gorm.DB, timer *ActiveTimer, t *Task, at time.Time) (*TimerStop, error) {
	if err := tx.Exec(`DELETE FROM active_timers WHERE user_id = ?`, timer.UserID).Error; err != nil {
		return nil, fmt.Errorf("task: timer delete: %w", err)
	}
	res := &TimerStop{TaskKey: t.Key, Worklogs: []WorklogView{}}
	end := at
	if limit := timer.StartedAt.Add(TimerLimit); end.After(limit) {
		end, res.AutoStopped = limit, true
	}
	if end.Sub(timer.StartedAt) >= time.Minute {
		loc, err := userLocation(tx, timer.UserID)
		if err != nil {
			return nil, err
		}
		for _, p := range splitByDay(timer.StartedAt, end, loc) {
			written, err := timerWorklog(tx, t, timer.UserID, p, at)
			if err != nil {
				return nil, err
			}
			if written == nil {
				res.Trimmed = true
				continue
			}
			res.Trimmed = res.Trimmed || written.Minutes < p.minutes
			res.Minutes += written.Minutes
			res.Worklogs = append(res.Worklogs, *written)
		}
	}
	res.Duration = duration.Format(res.Minutes)
	if res.AutoStopped {
		if err := events.Publish(tx, events.TimerAutoStopped, map[string]any{
			"user_id": timer.UserID, "task_id": t.ID, "key": t.Key, "title": t.Title, "project_id": t.ProjectID,
			"started_at": timer.StartedAt, "stopped_at": end, "minutes": res.Minutes,
		}); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// dayPart — часть работы таймера, приходящаяся на одну дату пояса пользователя.
type dayPart struct {
	date    time.Time
	minutes int
}

// splitByDay делит [from, to) по полуночам пояса loc; каждая часть округляется вверх до минуты.
func splitByDay(from, to time.Time, loc *time.Location) []dayPart {
	var parts []dayPart
	for cur := from; cur.Before(to); {
		l := cur.In(loc)
		next := time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, loc)
		if next.After(to) {
			next = to
		}
		parts = append(parts, dayPart{date: dayIn(cur, loc), minutes: int((next.Sub(cur) + time.Minute - 1) / time.Minute)})
		cur = next
	}
	return parts
}

// timerWorklog — запись части таймера с обрезкой по дневному лимиту; nil — остатка лимита нет.
// Обрезка публикует timer.trimmed (уведомление пользователю, §6.4, §7.7).
func timerWorklog(tx *gorm.DB, t *Task, userID uuid.UUID, p dayPart, now time.Time) (*WorklogView, error) {
	used, err := dayUsed(tx, userID, p.date, uuid.Nil)
	if err != nil {
		return nil, err
	}
	minutes := min(p.minutes, max(dailyLimitMinutes-used, 0))
	if minutes < p.minutes {
		if err := events.Publish(tx, events.TimerTrimmed, map[string]any{
			"user_id": userID, "task_id": t.ID, "key": t.Key, "title": t.Title, "project_id": t.ProjectID,
			"work_date": p.date.Format(time.DateOnly), "requested_minutes": p.minutes, "written_minutes": minutes,
		}); err != nil {
			return nil, err
		}
	}
	if minutes == 0 {
		return nil, nil
	}
	w := Worklog{TaskID: t.ID, UserID: userID, Minutes: minutes, WorkDate: Date{p.date}, Source: WorklogTimer}
	return insertWorklog(tx, t, &w, userID, now)
}

func timerView(timer *ActiveTimer, t *Task, now time.Time) TimerView {
	return TimerView{TaskID: timer.TaskID, TaskKey: t.Key, Title: t.Title, StartedAt: timer.StartedAt,
		ElapsedSeconds: max(int64(now.Sub(timer.StartedAt)/time.Second), 0)}
}

// timerTx — транзакция таймера с повтором при deadlock: таймер держит две задачи (прежнюю и
// новую), а операции спринта блокируют задачи спринта своим порядком — встречу Postgres
// разрывает ошибкой 40P01, и транзакция повторяется целиком.
func (s *Service) timerTx(ctx context.Context, fn func(*gorm.DB) error) error {
	var err error
	for range 3 {
		err = s.db.WithContext(ctx).Transaction(fn)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "40P01" {
			return err
		}
	}
	return err
}
