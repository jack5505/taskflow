package project

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
)

var (
	ErrNotMember     = apierr.NotFound("Пользователь не участник проекта")
	ErrUserNotFound  = apierr.NotFound("Пользователь не найден")
	ErrLastManager   = apierr.Conflict(apierr.CodeLastManager, "Это единственный активный менеджер проекта: сначала назначьте другого менеджера")
	errInactiveToAdd = fieldError("user_id", "пользователь деактивирован")
)

// MemberView — участник в ответе API.
type MemberView struct {
	UserID      uuid.UUID `json:"user_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role" enums:"manager,member"`
	IsActive    bool      `json:"is_active"`
}

// Members — участники проекта: менеджеры первыми, затем по имени.
func (s *Service) Members(ctx context.Context, a *Access) ([]MemberView, error) {
	list := []MemberView{}
	err := s.db.WithContext(ctx).Raw(`SELECT m.user_id, u.email, u.display_name, m.role, u.is_active
		FROM project_members m JOIN users u ON u.id = m.user_id
		WHERE m.project_id = ? ORDER BY m.role = 'manager' DESC, u.display_name, u.email`, a.Project.ID).Scan(&list).Error
	if err != nil {
		return nil, fmt.Errorf("project: members: %w", err)
	}
	return list, nil
}

// PutMember добавляет участника или меняет его роль (§2: менеджер и администратор).
// Понижение единственного активного менеджера — 409 LAST_MANAGER. Возвращает added=true,
// если участник новый. Пишет member.added или member.role_changed (§9.4).
func (s *Service) PutMember(ctx context.Context, a *Access, userID uuid.UUID, role string) (view *MemberView, added bool, err error) {
	if !ValidRole(role) {
		return nil, false, fieldError("role", "manager или member")
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := s.lockForMembers(tx, a)
		if err != nil {
			return err
		}
		active, err := userActive(tx, userID)
		if err != nil {
			return err
		}
		current, err := memberRole(tx, p.ID, userID)
		if err != nil {
			return err
		}
		payload := map[string]any{"project_id": p.ID, "key": p.Key, "user_id": userID, "role": role, "by": a.userID}
		switch current {
		case role:
		case "":
			if !active {
				return errInactiveToAdd
			}
			if err := tx.Create(&Member{ProjectID: p.ID, UserID: userID, Role: role}).Error; err != nil {
				return fmt.Errorf("project: add member: %w", err)
			}
			added = true
			if err := events.Publish(tx, events.MemberAdded, payload); err != nil {
				return err
			}
		default:
			if current == RoleManager {
				if err := checkNotLastManager(tx, p, userID, active); err != nil {
					return err
				}
			}
			if err := tx.Model(&Member{}).Where("project_id = ? AND user_id = ?", p.ID, userID).
				Update("role", role).Error; err != nil {
				return fmt.Errorf("project: change role: %w", err)
			}
			payload["old_role"] = current
			if err := events.Publish(tx, events.MemberRoleChanged, payload); err != nil {
				return err
			}
		}
		view = &MemberView{UserID: userID, Role: role, IsActive: active}
		return tx.Raw(`SELECT email, display_name FROM users WHERE id = ?`, userID).Row().Scan(&view.Email, &view.DisplayName)
	})
	if err != nil {
		return nil, false, err
	}
	return view, added, nil
}

// RemoveMember исключает участника. Единственного активного менеджера — 409 LAST_MANAGER.
func (s *Service) RemoveMember(ctx context.Context, a *Access, userID uuid.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := s.lockForMembers(tx, a)
		if err != nil {
			return err
		}
		current, err := memberRole(tx, p.ID, userID)
		if err != nil {
			return err
		}
		if current == "" {
			return ErrNotMember
		}
		if current == RoleManager {
			active, err := userActive(tx, userID)
			if err != nil {
				return err
			}
			if err := checkNotLastManager(tx, p, userID, active); err != nil {
				return err
			}
		}
		if err := tx.Where("project_id = ? AND user_id = ?", p.ID, userID).Delete(&Member{}).Error; err != nil {
			return fmt.Errorf("project: remove member: %w", err)
		}
		return events.Publish(tx, events.MemberRemoved, map[string]any{
			"project_id": p.ID, "key": p.Key, "user_id": userID, "role": current, "by": a.userID,
		})
	})
}

// lockForMembers — общая часть изменений состава: строка проекта FOR UPDATE (на ней же
// сериализуется деактивация пользователя), архив — 409, права менеджера — под блокировкой.
func (s *Service) lockForMembers(tx *gorm.DB, a *Access) (*Project, error) {
	var p Project
	if err := lockProject(tx, "id = ?", a.Project.ID, &p); err != nil {
		return nil, err
	}
	if err := EnsureWritable(&p); err != nil {
		return nil, err
	}
	if !a.Admin {
		role, err := memberRole(tx, p.ID, a.userID)
		if err != nil {
			return nil, err
		}
		if role != RoleManager {
			return nil, ErrForbidden
		}
	}
	return &p, nil
}

// checkNotLastManager: активного менеджера нельзя понизить или исключить, если других
// активных менеджеров в проекте нет. Неактивный менеджер на это правило не влияет.
func checkNotLastManager(tx *gorm.DB, p *Project, userID uuid.UUID, active bool) error {
	if !active {
		return nil
	}
	var others int64
	if err := tx.Raw(`SELECT count(*) FROM project_members m JOIN users u ON u.id = m.user_id
		WHERE m.project_id = ? AND m.role = 'manager' AND m.user_id <> ? AND u.is_active`, p.ID, userID).
		Scan(&others).Error; err != nil {
		return fmt.Errorf("project: last manager check: %w", err)
	}
	if others == 0 {
		return ErrLastManager.With("projects", []string{p.Key})
	}
	return nil
}

func userActive(tx *gorm.DB, userID uuid.UUID) (bool, error) {
	var active []bool
	if err := tx.Raw(`SELECT is_active FROM users WHERE id = ?`, userID).Scan(&active).Error; err != nil {
		return false, fmt.Errorf("project: user: %w", err)
	}
	if len(active) == 0 {
		return false, ErrUserNotFound
	}
	return active[0], nil
}
