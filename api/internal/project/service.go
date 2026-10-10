package project

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/user"
)

var (
	// ErrNotFound — проекта нет, он удалён или пользователь в нём не участвует (§2: чужой проект не виден).
	ErrNotFound = apierr.NotFound("Проект не найден")
	// ErrArchived — §3.2: в архивном проекте запрещены любые изменения.
	ErrArchived  = apierr.Conflict(apierr.CodeProjectArchived, "Проект в архиве, изменения запрещены")
	ErrForbidden = apierr.Forbidden("Недостаточно прав в проекте")
	errAdminOnly = apierr.Forbidden("Доступно только администратору системы")
)

var keyRe = regexp.MustCompile(`^[A-Z]{2,10}$`)

func fieldError(field, reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: field, Reason: reason}})
}

// Access — проект и роль в нём текущего пользователя. Role пуста у администратора
// системы, который не участник проекта: права у него есть по глобальной роли (§2).
type Access struct {
	Project *Project
	Role    string
	Admin   bool
	userID  uuid.UUID
}

// CanManage — права менеджера проекта (§2): администратор системы или менеджер.
func (a *Access) CanManage() bool { return a.Admin || a.Role == RoleManager }

// EnsureWritable — проверка перед любым изменением данных проекта (§3.2).
func EnsureWritable(p *Project) error {
	if p.IsArchived {
		return ErrArchived
	}
	return nil
}

// View — проект в ответе API: настройки событий с умолчаниями §8.6 и роль текущего пользователя.
type View struct {
	*Project
	NotificationSettings map[string]bool `json:"notification_settings"`
	MyRole               *string         `json:"my_role"`
}

func newView(p *Project, role string) View {
	v := View{Project: p, NotificationSettings: EffectiveNotifications(p.NotificationSettings)}
	if role != "" {
		v.MyRole = &role
	}
	return v
}

// Service — проекты (ТЗ §3.2, §9.2).
type Service struct {
	db  *gorm.DB
	now func() time.Time
}

func NewService(db *gorm.DB) *Service { return &Service{db: db, now: time.Now} }

// Access находит неудалённый проект по ключу и роль пользователя в нём. Не участник и не
// администратор получает ErrNotFound, а не 403: существование чужого проекта не раскрывается.
func (s *Service) Access(ctx context.Context, u *user.User, key string) (*Access, error) {
	var p Project
	if err := s.db.WithContext(ctx).First(&p, "key = ?", key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("project: find: %w", err)
	}
	var m Member
	err := s.db.WithContext(ctx).First(&m, "project_id = ? AND user_id = ?", p.ID, u.ID).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if !u.IsSystemAdmin {
			return nil, ErrNotFound
		}
	case err != nil:
		return nil, fmt.Errorf("project: membership: %w", err)
	}
	return &Access{Project: &p, Role: m.Role, Admin: u.IsSystemAdmin, userID: u.ID}, nil
}

// View — проект из Access для ответа.
func (a *Access) View() View { return newView(a.Project, a.Role) }

// FindWithDeleted — проект по ключу, в том числе удалённый (журнал и корзина администратора).
func (s *Service) FindWithDeleted(ctx context.Context, key string) (*Project, error) {
	var p Project
	err := s.db.WithContext(ctx).Unscoped().First(&p, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project: find: %w", err)
	}
	return &p, nil
}

// ListFilter — фильтры списка (§7.2): обычные, «Архив», «Удалённые» (только администратор).
type ListFilter struct {
	Archived bool
	Deleted  bool
}

// List — проекты, в которых пользователь участник; администратору — все (§7.2).
func (s *Service) List(ctx context.Context, u *user.User, f ListFilter) ([]View, error) {
	if f.Deleted && !u.IsSystemAdmin {
		return nil, errAdminOnly
	}
	q := s.db.WithContext(ctx).Model(&Project{}).
		Select("projects.*, m.role AS my_role").
		Joins("LEFT JOIN project_members m ON m.project_id = projects.id AND m.user_id = ?", u.ID)
	if f.Deleted {
		q = q.Unscoped().Where("projects.deleted_at IS NOT NULL")
	} else {
		q = q.Where("projects.is_archived = ?", f.Archived)
	}
	if !u.IsSystemAdmin {
		q = q.Where("m.user_id IS NOT NULL")
	}
	var rows []struct {
		Project `gorm:"embedded"`
		MyRole  *string
	}
	if err := q.Order("projects.key").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: list: %w", err)
	}
	out := make([]View, len(rows))
	for i := range rows {
		role := ""
		if rows[i].MyRole != nil {
			role = *rows[i].MyRole
		}
		out[i] = newView(&rows[i].Project, role)
	}
	return out, nil
}

type CreateInput struct {
	Key         string
	Name        string
	Description *string
	Timezone    string
}

// Create — новый проект (администратор системы). Ключ уникален и среди удалённых (§3.2).
func (s *Service) Create(ctx context.Context, in CreateInput) (*Project, error) {
	if !keyRe.MatchString(in.Key) {
		return nil, fieldError("key", "от 2 до 10 заглавных латинских букв")
	}
	name, err := projectName(in.Name)
	if err != nil {
		return nil, err
	}
	p := New(in.Key, name)
	if p.Description, err = description(in.Description); err != nil {
		return nil, err
	}
	if in.Timezone != "" {
		if err := checkTimezone(in.Timezone); err != nil {
			return nil, err
		}
		p.Timezone = in.Timezone
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoNothing: true}).Create(p)
		if res.Error != nil {
			return fmt.Errorf("project: create: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return fieldError("key", "уже занят (в том числе удалённым проектом)")
		}
		return audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: audit.ProjectCreated, EntityType: "project", EntityID: p.ID.String(),
			New: map[string]any{"key": p.Key, "name": p.Name, "timezone": p.Timezone}})
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// UpdateInput — изменяемые поля; nil — не менять. Description "" очищает описание.
type UpdateInput struct {
	Name                 *string
	Description          *string
	Timezone             *string
	NotificationSettings map[string]bool
	DailySummaryTime     *string
	IsArchived           *bool
}

func (in UpdateInput) changesData() bool {
	return in.Name != nil || in.Description != nil || in.Timezone != nil ||
		in.NotificationSettings != nil || in.DailySummaryTime != nil
}

// Update — PATCH /projects/{key}: менеджер; смена is_archived — только администратор (§9.2).
// В архивном проекте допустимо одно изменение — разархивация администратором; 409 проверяется
// раньше валидации полей. Роль перепроверяется под блокировкой строки проекта.
func (s *Service) Update(ctx context.Context, a *Access, in UpdateInput) (*Project, error) {
	var p Project
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockProject(tx, "id = ?", a.Project.ID, &p); err != nil {
			return err
		}
		role, err := memberRole(tx, p.ID, a.userID)
		if err != nil {
			return err
		}
		if !a.Admin && role != RoleManager {
			return ErrForbidden
		}
		archiveChange := in.IsArchived != nil && *in.IsArchived != p.IsArchived
		if archiveChange && !a.Admin {
			return errAdminOnly
		}
		if p.IsArchived && (in.changesData() || !archiveChange) {
			return ErrArchived
		}
		updates, err := in.updates(p.NotificationSettings)
		if err != nil {
			return err
		}
		if old, nw := settingsDiff(&p, updates); len(nw) > 0 {
			if err := audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: audit.ProjectUpdated, EntityType: "project",
				EntityID: p.ID.String(), Old: old, New: nw}); err != nil {
				return err
			}
		}
		if archiveChange {
			action := audit.ProjectUnarchived
			if *in.IsArchived {
				action = audit.ProjectArchived
			}
			if err := audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: action, EntityType: "project", EntityID: p.ID.String()}); err != nil {
				return err
			}
			updates["is_archived"] = *in.IsArchived
			if *in.IsArchived {
				// Остановку запущенных таймеров при архивации (§3.2) добавит задача таймера (#33).
				if err := events.Publish(tx, events.ProjectArchived, map[string]any{"project_id": p.ID, "key": p.Key}); err != nil {
					return err
				}
			}
		}
		if len(updates) == 0 {
			return nil
		}
		if err := tx.Model(&p).Updates(updates).Error; err != nil {
			return fmt.Errorf("project: update: %w", err)
		}
		return tx.First(&p, "id = ?", p.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// updates — проверенные изменения полей для UPDATE.
func (in UpdateInput) updates(stored events.JSON) (map[string]any, error) {
	updates := map[string]any{}
	if in.Name != nil {
		name, err := projectName(*in.Name)
		if err != nil {
			return nil, err
		}
		updates["name"] = name
	}
	if in.Description != nil {
		d, err := description(in.Description)
		if err != nil {
			return nil, err
		}
		updates["description"] = d
	}
	if in.Timezone != nil {
		if err := checkTimezone(*in.Timezone); err != nil {
			return nil, err
		}
		updates["timezone"] = *in.Timezone
	}
	if in.DailySummaryTime != nil {
		t, err := time.Parse("15:04", *in.DailySummaryTime)
		if err != nil {
			return nil, fieldError("daily_summary_time", "время в формате ЧЧ:ММ")
		}
		updates["daily_summary_time"] = model.TimeOfDay(t.Format("15:04"))
	}
	if in.NotificationSettings != nil {
		merged, err := MergeNotifications(stored, in.NotificationSettings)
		if err != nil {
			return nil, fieldError("notification_settings", err.Error())
		}
		updates["notification_settings"] = merged
	}
	return updates, nil
}

// Delete — мягкое удаление (администратор, §3.2): проект пропадает из списков и бота,
// Telegram-группа отключается; задачи и записи времени остаются.
func (s *Service) Delete(ctx context.Context, key string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var p Project
		if err := lockProject(tx, "key = ?", key, &p); err != nil {
			return err
		}
		if err := tx.Model(&p).Updates(map[string]any{"deleted_at": s.now(), "telegram_chat_id": nil}).Error; err != nil {
			return fmt.Errorf("project: delete: %w", err)
		}
		if err := audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: audit.ProjectDeleted, EntityType: "project",
			EntityID: p.ID.String(), Old: map[string]any{"telegram_chat_id": p.TelegramChatID}}); err != nil {
			return err
		}
		return events.Publish(tx, events.ProjectDeleted, map[string]any{"project_id": p.ID, "key": p.Key})
	})
}

// Restore — восстановление удалённого проекта (администратор). Не удалённый возвращается как есть.
// Отдельного события нет: список §9.4 закрыт, а бот о проекте узнаёт заново при подключении группы.
func (s *Service) Restore(ctx context.Context, key string) (*Project, error) {
	var p Project
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Raw(`SELECT * FROM projects WHERE key = ? FOR UPDATE`, key).Scan(&p)
		if res.Error != nil {
			return fmt.Errorf("project: find: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if !p.DeletedAt.Valid {
			return nil
		}
		if err := tx.Unscoped().Model(&p).Updates(map[string]any{"deleted_at": nil, "updated_at": s.now()}).Error; err != nil {
			return fmt.Errorf("project: restore: %w", err)
		}
		if err := audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: audit.ProjectRestored, EntityType: "project", EntityID: p.ID.String()}); err != nil {
			return err
		}
		return tx.First(&p, "id = ?", p.ID).Error
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// NextTaskNumber выдаёт следующий номер задачи проекта атомарно (§3.2: UPDATE … RETURNING):
// параллельные создания из веба и Telegram не получат одинаковых номеров. Вызывать в транзакции
// создания задачи — при откате номер не тратится. updated_at проекта не трогает: это время
// изменения настроек, а не появления задач. Архивный и удалённый проект номеров не выдают.
func NextTaskNumber(tx *gorm.DB, projectID uuid.UUID) (int, error) {
	var n []int
	if err := tx.Raw(`UPDATE projects SET task_counter = task_counter + 1
		WHERE id = ? AND deleted_at IS NULL AND NOT is_archived RETURNING task_counter`, projectID).Scan(&n).Error; err != nil {
		return 0, fmt.Errorf("project: next task number: %w", err)
	}
	if len(n) == 1 {
		return n[0], nil
	}
	var archived []bool
	if err := tx.Raw(`SELECT is_archived FROM projects WHERE id = ? AND deleted_at IS NULL`, projectID).Scan(&archived).Error; err != nil {
		return 0, fmt.Errorf("project: next task number: %w", err)
	}
	if len(archived) == 1 && archived[0] {
		return 0, ErrArchived
	}
	return 0, ErrNotFound
}

// settingsDiff — прежние и новые значения меняющихся настроек для журнала.
func settingsDiff(p *Project, updates map[string]any) (old, nw map[string]any) {
	old, nw = audit.Diff(map[string]any{"name": p.Name, "timezone": p.Timezone, "daily_summary_time": p.DailySummaryTime}, updates)
	if v, ok := updates["description"]; ok {
		d := v.(*string)
		if (d == nil) != (p.Description == nil) || (d != nil && *d != *p.Description) {
			old["description"], nw["description"] = p.Description, d
		}
	}
	if v, ok := updates["notification_settings"]; ok {
		before, after := EffectiveNotifications(p.NotificationSettings), EffectiveNotifications(v.(events.JSON))
		ob, nb := map[string]bool{}, map[string]bool{}
		for k, b := range after {
			if before[k] != b {
				ob[k], nb[k] = before[k], b
			}
		}
		if len(nb) > 0 {
			old["notification_settings"], nw["notification_settings"] = ob, nb
		}
	}
	return old, nw
}

// lockProject — строка неудалённого проекта FOR UPDATE по условию where ("id = ?" или "key = ?").
func lockProject(tx *gorm.DB, where string, arg any, p *Project) error {
	res := tx.Raw(`SELECT * FROM projects WHERE `+where+` AND deleted_at IS NULL FOR UPDATE`, arg).Scan(p)
	if res.Error != nil {
		return fmt.Errorf("project: lock: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// memberRole — роль пользователя в проекте, "" — не участник.
func memberRole(tx *gorm.DB, projectID, userID uuid.UUID) (string, error) {
	var roles []string
	if err := tx.Raw(`SELECT role FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID).
		Scan(&roles).Error; err != nil {
		return "", fmt.Errorf("project: membership: %w", err)
	}
	if len(roles) == 0 {
		return "", nil
	}
	return roles[0], nil
}

func projectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 100 {
		return "", fieldError("name", "от 1 до 100 символов")
	}
	return name, nil
}

func description(d *string) (*string, error) {
	if d == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*d)
	if v == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(v) > 2000 {
		return nil, fieldError("description", "не длиннее 2000 символов")
	}
	return &v, nil
}

func checkTimezone(tz string) error {
	if !model.ValidTimezone(tz) {
		return fieldError("timezone", "неизвестный часовой пояс IANA")
	}
	return nil
}
