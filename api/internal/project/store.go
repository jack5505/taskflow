package project

import (
	"database/sql"
	"errors"
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
	sole, err := soleManaged(tx, userID, projectIDs)
	if err != nil || len(sole) == 0 {
		return nil, err
	}
	var keys []string
	if err := tx.Raw(`SELECT key FROM projects WHERE id IN ? ORDER BY key`, sole).Scan(&keys).Error; err != nil {
		return nil, fmt.Errorf("project: last manager keys: %w", err)
	}
	return keys, nil
}

// soleManaged — проекты из projectIDs без других активных менеджеров, кроме userID.
func soleManaged(tx *gorm.DB, userID uuid.UUID, projectIDs []uuid.UUID) ([]uuid.UUID, error) {
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
	return sole, nil
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

// projectByKey — проект по ключу; withDeleted — и удалённый. Нет — nil.
func projectByKey(db *gorm.DB, key string, withDeleted bool) (*Project, error) {
	var p Project
	q := db
	if withDeleted {
		q = q.Unscoped()
	}
	err := q.First(&p, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("project: find: %w", err)
	}
	return &p, nil
}

// reloadProject перечитывает строку проекта p.
func reloadProject(tx *gorm.DB, p *Project) error {
	return tx.First(p, "id = ?", p.ID).Error
}

// lockAnyByKey — проект по ключу (и удалённый) в p, строка FOR UPDATE; нет — found = false.
func lockAnyByKey(tx *gorm.DB, key string, p *Project) (found bool, err error) {
	res := tx.Raw(`SELECT * FROM projects WHERE key = ? FOR UPDATE`, key).Scan(p)
	if res.Error != nil {
		return false, fmt.Errorf("project: find: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// incrementTaskCounter — следующий номер задачи неудалённого неархивного проекта (UPDATE …
// RETURNING); проект удалён или в архиве — ok = false.
func incrementTaskCounter(tx *gorm.DB, projectID uuid.UUID) (n int, ok bool, err error) {
	var ns []int
	if err := tx.Raw(`UPDATE projects SET task_counter = task_counter + 1
		WHERE id = ? AND deleted_at IS NULL AND NOT is_archived RETURNING task_counter`, projectID).Scan(&ns).Error; err != nil {
		return 0, false, fmt.Errorf("project: next task number: %w", err)
	}
	if len(ns) != 1 {
		return 0, false, nil
	}
	return ns[0], true, nil
}

// liveArchived — в архиве ли неудалённый проект; удалён или нет — live = false.
func liveArchived(tx *gorm.DB, projectID uuid.UUID) (archived, live bool, err error) {
	var flags []bool
	if err := tx.Raw(`SELECT is_archived FROM projects WHERE id = ? AND deleted_at IS NULL`, projectID).Scan(&flags).Error; err != nil {
		return false, false, fmt.Errorf("project: next task number: %w", err)
	}
	if len(flags) != 1 {
		return false, false, nil
	}
	return flags[0], true, nil
}

// listRow — проект списка с ролью пользователя.
type listRow struct {
	Project `gorm:"embedded"`
	MyRole  *string
}

// listRows — проекты по фильтру f с ролью userID, по ключу; membersOnly — только где он участник.
func listRows(db *gorm.DB, userID uuid.UUID, membersOnly bool, f ListFilter) ([]listRow, error) {
	where := "p.deleted_at IS NULL AND p.is_archived = @archived"
	if f.Deleted {
		where = "p.deleted_at IS NOT NULL"
	}
	if membersOnly {
		where += " AND m.user_id IS NOT NULL"
	}
	var rows []listRow
	if err := db.Raw(`SELECT p.*, m.role AS my_role FROM projects p
		LEFT JOIN project_members m ON m.project_id = p.id AND m.user_id = @user
		WHERE `+where+` ORDER BY p.key`, sql.Named("user", userID), sql.Named("archived", f.Archived)).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: list: %w", err)
	}
	return rows, nil
}

// memberRoleRow — участник проекта и его роль.
type memberRoleRow struct {
	UserID uuid.UUID
	Role   string
}

// memberRoles — участники проекта с ролями.
func memberRoles(db *gorm.DB, projectID uuid.UUID) ([]memberRoleRow, error) {
	var roles []memberRoleRow
	if err := db.Raw(`SELECT user_id, role FROM project_members WHERE project_id = ?`, projectID).Scan(&roles).Error; err != nil {
		return nil, fmt.Errorf("project: members: %w", err)
	}
	return roles, nil
}
