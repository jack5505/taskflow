package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/duration"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/sprint/sprintdb"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Источники записи времени (§3.5).
const (
	WorklogManual   = "manual"
	WorklogTimer    = "timer"
	WorklogTelegram = "telegram"
)

const (
	maxWorklogMinutes = 1440 // §3.5: не более суток одной записью
	dailyLimitMinutes = 1440 // §6.3: 24 часа на пользователя за дату
	editWindowDays    = 7    // §6.5: автор правит свою запись 7 календарных дней после work_date
)

var (
	ErrWorklogNotFound      = apierr.NotFound("Запись времени не найдена")
	errWorklogForbidden     = apierr.Forbidden("Списывать время может исполнитель задачи или менеджер проекта")
	errWorklogEditForbidden = apierr.Forbidden("Чужие записи времени изменяет и удаляет менеджер проекта")
	errWorklogExpired       = apierr.Forbidden("Свою запись времени можно изменить или удалить в течение 7 дней после даты работы; позже — менеджер проекта")
	errInvalidDuration      = apierr.BadRequest(apierr.CodeInvalidDuration, "Длительность в формате 2h 30m, 45m, 3h или 1d (1d = 8h), от 1m до 24h").
				With("fields", []apierr.FieldError{{Field: "duration", Reason: "формат 2h 30m, от 1m до 24h"}})
	// §6.5 и §4: состояние задачи не допускает операцию — 409 (§9.3: конфликт состояния), код
	// INVALID_TRANSITION — как у прочих запретов по статусу задачи; сами данные запроса верны.
	errSprintClosed  = apierr.Conflict(apierr.CodeInvalidTransition, "Задача в завершённом спринте: записи времени не добавляются, не изменяются и не удаляются")
	errTaskCancelled = apierr.Conflict(apierr.CodeInvalidTransition, "На отменённую задачу время не списывается")
)

// Worklog — строка worklogs (§3.5).
type Worklog struct {
	model.Base
	TaskID   uuid.UUID `gorm:"type:uuid" json:"task_id"`
	UserID   uuid.UUID `gorm:"type:uuid" json:"user_id"`
	Minutes  int       `json:"minutes" example:"150"`
	WorkDate Date      `gorm:"type:date" json:"work_date" swaggertype:"string" example:"2026-10-11"`
	Comment  *string   `json:"comment"`
	Source   string    `json:"source" enums:"manual,timer,telegram"`
}

func (Worklog) TableName() string { return "worklogs" }

// WorklogView — запись в ответе: с именем автора и длительностью в формате §6.1.
type WorklogView struct {
	Worklog
	UserName string `json:"user_name"`
	Duration string `json:"duration" example:"2h 30m"`
	Hours    string `json:"hours" example:"2.50"`
}

// WorklogInput — новая запись: длительность строкой §6.1, дата (nil — сегодня по поясу
// пользователя), комментарий до 500 символов.
type WorklogInput struct {
	Duration string
	WorkDate *string
	Comment  *string
}

// WorklogPatch — изменение записи: меняются только переданные поля, null в Comment очищает его.
type WorklogPatch struct {
	Duration Opt[string]
	WorkDate Opt[string]
	Comment  Opt[string]
}

// Worklogs — записи времени задачи, новые сверху. Видит участник; удалённой задачи — администратор
// (записи остаются в отчётах, §4).
func (s *Service) Worklogs(ctx context.Context, u *user.User, key string) ([]WorklogView, error) {
	t, _, err := s.Get(ctx, u, key)
	if err != nil {
		return nil, err
	}
	list := []WorklogView{}
	db := s.db.WithContext(ctx)
	if err := db.Raw(`SELECT * FROM worklogs WHERE task_id = ? ORDER BY work_date DESC, created_at DESC, id`, t.ID).
		Scan(&list).Error; err != nil {
		return nil, fmt.Errorf("task: worklogs: %w", err)
	}
	if err := fillWorklogNames(db, list); err != nil {
		return nil, err
	}
	for i := range list {
		list[i].fill()
	}
	return list, nil
}

func (v *WorklogView) fill() {
	v.Duration, v.Hours = duration.Format(v.Minutes), duration.Hours(v.Minutes)
}

// AddWorklog — ручное списание времени (§6.3) от имени u: исполнитель задачи или менеджер (§2).
// Источник — telegram для бота, иначе manual. Порядок ответов: 404 → 403 → 409 (архив, закрытый
// спринт, отменённая задача) → 400 (длительность, дата, комментарий) → 409 DAILY_LIMIT_EXCEEDED.
func (s *Service) AddWorklog(ctx context.Context, u *user.User, key string, in WorklogInput) (*WorklogView, error) {
	if keyRe.FindStringSubmatch(key) == nil {
		return nil, ErrNotFound
	}
	var view *WorklogView
	err := s.worklogTx(ctx, func(tx *gorm.DB) error {
		o, err := openForWorklog(tx, u, key)
		if err != nil {
			return err
		}
		t := &o.task
		if !o.actor.manager && (t.AssigneeID == nil || *t.AssigneeID != u.ID) {
			return errWorklogForbidden
		}
		if err := worklogWritable(tx, o, worklogAdd); err != nil {
			return err
		}
		minutes, err := parseWorklogMinutes(in.Duration)
		if err != nil {
			return err
		}
		loc, err := userLocation(tx, u.ID)
		if err != nil {
			return err
		}
		today := s.today(loc)
		date := today
		if in.WorkDate != nil {
			if date, err = workDate(*in.WorkDate, today, t, loc); err != nil {
				return err
			}
		}
		comment, err := worklogComment(in.Comment)
		if err != nil {
			return err
		}
		w := Worklog{TaskID: t.ID, UserID: u.ID, Minutes: minutes, WorkDate: Date{date}, Comment: comment,
			Source: worklogSource(o.actor.source)}
		view, err = insertWorklog(tx, t, &w, u.ID, s.now())
		return err
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// insertWorklog — запись с проверкой дневного лимита, spent_minutes задачи и worklog.created в
// одной транзакции. Задачу вызывающий держит FOR UPDATE. Через неё же пойдут записи таймера (§6.4).
func insertWorklog(tx *gorm.DB, t *Task, w *Worklog, by uuid.UUID, now time.Time) (*WorklogView, error) {
	w.CreatedAt, w.UpdatedAt = now, now
	if err := checkDailyLimit(tx, w.UserID, w.WorkDate.Time, w.Minutes, uuid.Nil); err != nil {
		return nil, err
	}
	if err := tx.Create(w).Error; err != nil {
		return nil, fmt.Errorf("task: worklog: %w", err)
	}
	spent, err := refreshSpent(tx, t)
	if err != nil {
		return nil, err
	}
	if err := events.Publish(tx, events.WorklogCreated, map[string]any{
		"worklog_id": w.ID, "task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "user_id": w.UserID,
		"by": by, "minutes": w.Minutes, "work_date": w.WorkDate, "source": w.Source, "spent_minutes": spent,
	}); err != nil {
		return nil, err
	}
	return worklogView(tx, w.ID)
}

// UpdateWorklog — правка записи (§6.5): автор — 7 дней после work_date, менеджер — любую без срока.
// Правка чужой записи пишется в журнал аудита (§3.8).
func (s *Service) UpdateWorklog(ctx context.Context, u *user.User, id uuid.UUID, in WorklogPatch) (*WorklogView, error) {
	var view *WorklogView
	err := s.onWorklog(ctx, u, id, worklogEdit, func(tx *gorm.DB, o *opened, w *Worklog, today time.Time, loc *time.Location) error {
		before := *w
		if in.Duration.Set {
			if in.Duration.Value == nil {
				return errInvalidDuration
			}
			minutes, err := parseWorklogMinutes(*in.Duration.Value)
			if err != nil {
				return err
			}
			w.Minutes = minutes
		}
		if in.WorkDate.Set {
			if in.WorkDate.Value == nil {
				return fieldError("work_date", "дата ГГГГ-ММ-ДД")
			}
			date, err := workDate(*in.WorkDate.Value, today, &o.task, loc)
			if err != nil {
				return err
			}
			if !o.actor.manager && today.After(date.AddDate(0, 0, editWindowDays)) {
				return fieldError("work_date", "не раньше чем за 7 дней до сегодня")
			}
			w.WorkDate = Date{date}
		}
		if in.Comment.Set {
			comment, err := worklogComment(in.Comment.Value)
			if err != nil {
				return err
			}
			w.Comment = comment
		}
		if w.Minutes != before.Minutes || !w.WorkDate.Equal(before.WorkDate.Time) || !samePtr(w.Comment, before.Comment) {
			if err := s.saveWorklog(tx, o, &before, w, u.ID); err != nil {
				return err
			}
		}
		var err error
		view, err = worklogView(tx, w.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

func (s *Service) saveWorklog(tx *gorm.DB, o *opened, before, w *Worklog, by uuid.UUID) error {
	if err := checkDailyLimit(tx, w.UserID, w.WorkDate.Time, w.Minutes, w.ID); err != nil {
		return err
	}
	if err := tx.Model(w).Updates(map[string]any{
		"minutes": w.Minutes, "work_date": w.WorkDate, "comment": w.Comment, "updated_at": s.now(),
	}).Error; err != nil {
		return fmt.Errorf("task: worklog update: %w", err)
	}
	t := &o.task
	spent, err := refreshSpent(tx, t)
	if err != nil {
		return err
	}
	if by != w.UserID {
		if err := audit.Write(tx, audit.Record{ProjectID: &t.ProjectID, Action: audit.WorklogUpdated,
			EntityType: "worklog", EntityID: w.ID.String(), Old: worklogAudit(before, t), New: worklogAudit(w, t)}); err != nil {
			return err
		}
	}
	return events.Publish(tx, events.WorklogUpdated, map[string]any{
		"worklog_id": w.ID, "task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "user_id": w.UserID, "by": by,
		"old": worklogAudit(before, t), "new": worklogAudit(w, t), "spent_minutes": spent,
	})
}

// DeleteWorklog — удаление записи по тем же правилам, что и правка (§6.5); чужой — в аудит (§3.8).
func (s *Service) DeleteWorklog(ctx context.Context, u *user.User, id uuid.UUID) error {
	return s.onWorklog(ctx, u, id, worklogDelete, func(tx *gorm.DB, o *opened, w *Worklog, _ time.Time, _ *time.Location) error {
		if err := tx.Delete(w).Error; err != nil {
			return fmt.Errorf("task: worklog delete: %w", err)
		}
		t := &o.task
		spent, err := refreshSpent(tx, t)
		if err != nil {
			return err
		}
		if u.ID != w.UserID {
			if err := audit.Write(tx, audit.Record{ProjectID: &t.ProjectID, Action: audit.WorklogDeleted,
				EntityType: "worklog", EntityID: w.ID.String(), Old: worklogAudit(w, t)}); err != nil {
				return err
			}
		}
		return events.Publish(tx, events.WorklogDeleted, map[string]any{
			"worklog_id": w.ID, "task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "user_id": w.UserID, "by": u.ID,
			"minutes": w.Minutes, "work_date": w.WorkDate, "spent_minutes": spent,
		})
	})
}

// onWorklog — в одной транзакции: задача записи (openForWorklog, FOR UPDATE), запись FOR UPDATE,
// права (§6.5: своя — 7 дней после work_date, чужая и старая — менеджер; 403), затем архив и
// закрытый спринт, отменённая задача для правки (409). fn получает «сегодня» и пояс владельца записи: к его календарю
// относятся work_date и дневной лимит, даже когда правит менеджер.
func (s *Service) onWorklog(ctx context.Context, u *user.User, id uuid.UUID, op worklogOp,
	fn func(tx *gorm.DB, o *opened, w *Worklog, today time.Time, loc *time.Location) error) error {
	err := s.worklogTx(ctx, func(tx *gorm.DB) error {
		var key string
		if err := tx.Raw(`SELECT t.key FROM worklogs w JOIN tasks t ON t.id = w.task_id WHERE w.id = ?`, id).Scan(&key).Error; err != nil {
			return fmt.Errorf("task: worklog: %w", err)
		}
		if key == "" {
			return ErrWorklogNotFound
		}
		o, err := openForWorklog(tx, u, key)
		if err != nil {
			return err
		}
		var w Worklog
		res := tx.Raw(`SELECT * FROM worklogs WHERE id = ? AND task_id = ? FOR UPDATE`, id, o.task.ID).Scan(&w)
		if res.Error != nil {
			return fmt.Errorf("task: worklog lock: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrWorklogNotFound
		}
		loc, err := userLocation(tx, w.UserID)
		if err != nil {
			return err
		}
		today := s.today(loc)
		if w.UserID != u.ID && !o.actor.manager {
			return errWorklogEditForbidden
		}
		if !o.actor.manager && today.After(w.WorkDate.AddDate(0, 0, editWindowDays)) {
			return errWorklogExpired
		}
		if err := worklogWritable(tx, o, op); err != nil {
			return err
		}
		return fn(tx, o, &w, today, loc)
	})
	if errors.Is(err, ErrNotFound) {
		return ErrWorklogNotFound
	}
	return err
}

// openForWorklog — openTask с FOR UPDATE строки задачи (spent_minutes меняется в этой же
// транзакции). Строку спринта задачи берём FOR SHARE до строки задачи — порядок блокировок
// закрытия спринта (спринт → задачи), иначе закрытие могло бы пройти между проверкой и записью.
// Если задачу успели перенести в другой спринт между чтением sprint_id и блокировкой задачи,
// транзакция откатывается и повторяется (worklogTx): брать новый спринт после задачи — deadlock.
func openForWorklog(tx *gorm.DB, u *user.User, key string) (*opened, error) {
	var sprints []uuid.UUID
	if err := tx.Raw(`SELECT sprint_id FROM tasks WHERE key = ? AND sprint_id IS NOT NULL`, key).Scan(&sprints).Error; err != nil {
		return nil, fmt.Errorf("task: worklog sprint: %w", err)
	}
	o, err := openTask(tx, u, key, preLock{sprints: sprints}, "FOR UPDATE")
	if err != nil {
		return nil, err
	}
	var locked *uuid.UUID
	if len(sprints) > 0 {
		locked = &sprints[0]
	}
	if !samePtr(o.task.SprintID, locked) {
		return nil, errSprintMoved
	}
	return o, nil
}

// errSprintMoved — задачу перенесли в другой спринт, пока брались блокировки; worklogTx повторит.
var errSprintMoved = errors.New("task: worklog: sprint moved")

// worklogTx — транзакция записи времени с повтором при errSprintMoved (не больше трёх попыток).
func (s *Service) worklogTx(ctx context.Context, fn func(*gorm.DB) error) error {
	var err error
	for range 3 {
		if err = s.db.WithContext(ctx).Transaction(fn); !errors.Is(err, errSprintMoved) {
			return err
		}
	}
	return fmt.Errorf("task: worklog: задача всё время меняет спринт: %w", err)
}

// worklogOp — операция с записью: от неё зависит, что запрещает отменённая задача.
type worklogOp int

const (
	worklogAdd worklogOp = iota
	worklogEdit
	worklogDelete
)

// worklogWritable — одна проверка состояния для добавления, правки и удаления (и таймера):
// архив проекта (409 PROJECT_ARCHIVED), закрытый спринт задачи — никаких изменений (§6.5),
// отменённая задача — нельзя списывать (§4): ни новой записью, ни правкой; удалить можно.
// Строка спринта уже под FOR SHARE (openForWorklog).
func worklogWritable(tx *gorm.DB, o *opened, op worklogOp) error {
	if err := project.EnsureWritable(&o.project); err != nil {
		return err
	}
	if o.task.SprintID != nil {
		if err := sprintNotClosed(tx, *o.task.SprintID); err != nil {
			return err
		}
	}
	if o.task.Status == StatusCancelled && op != worklogDelete {
		return errTaskCancelled
	}
	return nil
}

func sprintNotClosed(tx *gorm.DB, id uuid.UUID) error {
	state, err := sprintdb.State(tx, id)
	if err != nil {
		return err
	}
	if state == sprintdb.StateClosed {
		return errSprintClosed
	}
	return nil
}

// checkDailyLimit — сумма записей пользователя за дату по всем задачам (и удалённым — их записи
// остаются, §4) вместе с новой не больше 24 часов (§6.3). Записи одного пользователя
// сериализуются advisory-блокировкой по пользователю до конца транзакции: две одновременные
// записи не обходят лимит. Берётся после строки задачи — никто не ждёт задачу, держа эту блокировку
// раньше неё. exclude — изменяемая запись (её прежние минуты не считаются).
func checkDailyLimit(tx *gorm.DB, userID uuid.UUID, date time.Time, minutes int, exclude uuid.UUID) error {
	used, err := dayUsed(tx, userID, date, exclude)
	if err != nil {
		return err
	}
	if used+minutes > dailyLimitMinutes {
		return apierr.Conflict(apierr.CodeDailyLimitExceeded, "За один день можно списать не больше 24 часов").
			With("work_date", date.Format(time.DateOnly)).With("used_minutes", used).
			With("available_minutes", max(dailyLimitMinutes-used, 0))
	}
	return nil
}

// dayUsed — минуты пользователя за дату по всем задачам без записи exclude, под advisory-блокировкой
// пользователя до конца транзакции (повторный захват в той же транзакции не ждёт).
func dayUsed(tx *gorm.DB, userID uuid.UUID, date time.Time, exclude uuid.UUID) (int, error) {
	if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('worklog-day:' || ?))`, userID.String()).Error; err != nil {
		return 0, fmt.Errorf("task: worklog lock: %w", err)
	}
	var used int
	if err := tx.Raw(`SELECT COALESCE(sum(minutes), 0) FROM worklogs WHERE user_id = ? AND work_date = ? AND id <> ?`,
		userID, date.Format(time.DateOnly), exclude).Scan(&used).Error; err != nil {
		return 0, fmt.Errorf("task: worklog day sum: %w", err)
	}
	return used, nil
}

// refreshSpent — spent_minutes = сумма записей задачи (§3.3). Версия задачи не растёт: запись
// времени не правит редактируемые поля, и чужое списание не должно ломать правку карточки
// через If-Match (как комментарии); spent_minutes в PATCH не принимается — потерять его нельзя.
func refreshSpent(tx *gorm.DB, t *Task) (int, error) {
	if err := tx.Raw(`UPDATE tasks SET spent_minutes = (SELECT COALESCE(sum(minutes), 0) FROM worklogs WHERE task_id = ?)
		WHERE id = ? RETURNING spent_minutes`, t.ID, t.ID).Scan(&t.SpentMinutes).Error; err != nil {
		return 0, fmt.Errorf("task: spent: %w", err)
	}
	return t.SpentMinutes, nil
}

func worklogView(tx *gorm.DB, id uuid.UUID) (*WorklogView, error) {
	var v []WorklogView
	if err := tx.Raw(`SELECT * FROM worklogs WHERE id = ?`, id).Scan(&v).Error; err != nil {
		return nil, fmt.Errorf("task: worklog view: %w", err)
	}
	if len(v) == 0 {
		return &WorklogView{}, nil // как прежний JOIN без строк: пустое представление
	}
	if err := fillWorklogNames(tx, v); err != nil {
		return nil, err
	}
	v[0].fill()
	return &v[0], nil
}

// worklogAudit — значения записи для журнала аудита и события worklog.updated.
func worklogAudit(w *Worklog, t *Task) map[string]any {
	return map[string]any{"task_key": t.Key, "user_id": w.UserID, "minutes": w.Minutes,
		"work_date": w.WorkDate, "comment": w.Comment}
}

func parseWorklogMinutes(s string) (int, error) {
	m, err := duration.Parse(s)
	if err != nil || m < 1 || m > maxWorklogMinutes {
		return 0, errInvalidDuration
	}
	return m, nil
}

// workDate — дата работы (§3.5): не позже сегодняшней и не раньше даты создания задачи, обе — по
// поясу пользователя, к чьему дню относится запись.
func workDate(s string, today time.Time, t *Task, loc *time.Location) (time.Time, error) {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fieldError("work_date", "дата ГГГГ-ММ-ДД")
	}
	if d.After(today) {
		return time.Time{}, fieldError("work_date", "не позже сегодняшней даты")
	}
	if d.Before(dayIn(t.CreatedAt, loc)) {
		return time.Time{}, fieldError("work_date", "не раньше даты создания задачи")
	}
	return d, nil
}

func worklogComment(s *string) (*string, error) {
	if s == nil {
		return nil, nil
	}
	c := strings.TrimSpace(*s)
	if utf8.RuneCountInString(c) > 500 {
		return nil, fieldError("comment", "не длиннее 500 символов")
	}
	if c == "" {
		return nil, nil
	}
	return &c, nil
}

// worklogSource — источник записи по источнику действия: бот — telegram, остальное — manual
// (таймер пишет timer сам).
func worklogSource(s string) string {
	if historySource(s) == "telegram" {
		return WorklogTelegram
	}
	return WorklogManual
}

// userLocation — часовой пояс пользователя (§3.1) из базы, один раз на запрос; неизвестный — UTC.
func userLocation(tx *gorm.DB, id uuid.UUID) (*time.Location, error) {
	tz, err := user.Timezone(tx, id)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC, nil
	}
	return loc, nil
}

// today — сегодняшняя дата в поясе loc (полночь UTC с этой датой — как work_date из базы).
func (s *Service) today(loc *time.Location) time.Time {
	return dayIn(s.now(), loc)
}

// dayIn — календарная дата момента at в поясе loc.
func dayIn(at time.Time, loc *time.Location) time.Time {
	l := at.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}
