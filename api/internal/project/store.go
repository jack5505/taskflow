package project

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/user"
)

// KeyWithDeleted — ключ проекта id, в том числе удалённого; нет такого — "".
func KeyWithDeleted(db *gorm.DB, id uuid.UUID) (string, error) {
	var key string
	if err := db.Raw(`SELECT key FROM projects WHERE id = ?`, id).Scan(&key).Error; err != nil {
		return "", fmt.Errorf("project: key: %w", err)
	}
	return key, nil
}

// Timezones — часовые пояса проектов ids по id; withDeleted = false — без удалённых проектов.
func Timezones(db *gorm.DB, ids []uuid.UUID, withDeleted bool) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID       uuid.UUID
		Timezone string
	}
	if err := db.Raw(`SELECT id, timezone FROM projects WHERE id IN ? AND (? OR deleted_at IS NULL)`, ids, withDeleted).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: timezones: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r.Timezone
	}
	return out, nil
}

// Brief — ключ и часовой пояс проекта.
type Brief struct {
	ID       uuid.UUID
	Key      string
	Timezone string
}

// LiveBriefs — неудалённые проекты из ids по id; openOnly — ещё и не архивные. Блокировок не берёт.
func LiveBriefs(db *gorm.DB, ids []uuid.UUID, openOnly bool) (map[uuid.UUID]Brief, error) {
	out := make(map[uuid.UUID]Brief, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []Brief
	if err := db.Raw(`SELECT id, key, timezone FROM projects WHERE id IN ? AND deleted_at IS NULL AND NOT (? AND is_archived)`,
		ids, openOnly).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: briefs: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// Memberships — какие пары «проект — пользователь» из projectIDs × userIDs — участие. Блокировок не берёт.
func Memberships(db *gorm.DB, projectIDs, userIDs []uuid.UUID) (map[Membership]bool, error) {
	out := map[Membership]bool{}
	if len(projectIDs) == 0 || len(userIDs) == 0 {
		return out, nil
	}
	var rows []Membership
	if err := db.Raw(`SELECT project_id, user_id FROM project_members WHERE project_id IN ? AND user_id IN ?`,
		projectIDs, userIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: memberships: %w", err)
	}
	for _, r := range rows {
		out[r] = true
	}
	return out, nil
}

// UserProject — неудалённый проект пользователя с его ролью.
type UserProject struct {
	ID         uuid.UUID
	Key        string
	Name       string
	Role       string
	IsArchived bool
}

// ProjectsOf — неудалённые проекты, где userID участник, по ключу.
func ProjectsOf(db *gorm.DB, userID uuid.UUID) ([]UserProject, error) {
	var rows []UserProject
	if err := db.Raw(`SELECT p.id, p.key, p.name, m.role, p.is_archived
		FROM project_members m JOIN projects p ON p.id = m.project_id
		WHERE m.user_id = ? AND p.deleted_at IS NULL ORDER BY p.key`, userID).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: user projects: %w", err)
	}
	return rows, nil
}

// ManagesAny — userID менеджер хотя бы одного неудалённого проекта.
func ManagesAny(db *gorm.DB, userID uuid.UUID) (bool, error) {
	var ok bool
	if err := db.Raw(`SELECT EXISTS (
		SELECT 1 FROM project_members m JOIN projects p ON p.id = m.project_id
		WHERE m.user_id = ? AND m.role = 'manager' AND p.deleted_at IS NULL)`, userID).Scan(&ok).Error; err != nil {
		return false, fmt.Errorf("project: manager check: %w", err)
	}
	return ok, nil
}

// LockManagedBy блокирует FOR UPDATE проекты (и удалённые), где userID менеджер, в порядке id, и
// возвращает их id. Вызывается в транзакции деактивации после блокировок задач (задача → проект).
func LockManagedBy(tx *gorm.DB, userID uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if err := tx.Raw(`SELECT p.id FROM projects p JOIN project_members m ON m.project_id = p.id
		WHERE m.user_id = ? AND m.role = 'manager' ORDER BY p.id FOR UPDATE OF p`, userID).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("project: lock managed: %w", err)
	}
	return ids, nil
}

// SoleManagerKeys — ключи проектов из projectIDs, где кроме userID нет других активных менеджеров,
// по ключу (§3.1: «последний менеджер»). Строки проектов вызывающий уже заблокировал; здесь
// блокировок нет.
func SoleManagerKeys(tx *gorm.DB, userID uuid.UUID, projectIDs []uuid.UUID) ([]string, error) {
	if len(projectIDs) == 0 {
		return nil, nil
	}
	managers, err := Managers(tx, projectIDs)
	if err != nil {
		return nil, err
	}
	var others []uuid.UUID
	for _, m := range managers {
		if m.UserID != userID {
			others = append(others, m.UserID)
		}
	}
	active, err := user.ActiveAmong(tx, others)
	if err != nil {
		return nil, err
	}
	covered := map[uuid.UUID]bool{}
	for _, m := range managers {
		if m.UserID != userID && active[m.UserID] {
			covered[m.ProjectID] = true
		}
	}
	var sole []uuid.UUID
	for _, id := range projectIDs {
		if !covered[id] {
			sole = append(sole, id)
		}
	}
	if len(sole) == 0 {
		return nil, nil
	}
	var keys []string
	if err := tx.Raw(`SELECT key FROM projects WHERE id IN ? ORDER BY key`, sole).Scan(&keys).Error; err != nil {
		return nil, fmt.Errorf("project: last manager keys: %w", err)
	}
	return keys, nil
}

// LockShareLive — неудалённый проект id, строка FOR SHARE (операции над задачей берут её после
// строки задачи: задача → проект); нет или удалён — nil.
func LockShareLive(tx *gorm.DB, id uuid.UUID) (*Project, error) {
	var p Project
	res := tx.Raw(`SELECT * FROM projects WHERE id = ? AND deleted_at IS NULL FOR SHARE`, id).Scan(&p)
	if res.Error != nil {
		return nil, fmt.Errorf("project: lock share: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &p, nil
}
