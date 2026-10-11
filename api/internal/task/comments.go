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
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/user"
)

var (
	ErrCommentNotFound     = apierr.NotFound("Комментарий не найден")
	errCommentForbidden    = apierr.Forbidden("Изменять и удалять комментарий может его автор или менеджер проекта")
	errCommentBodyRequired = fieldError("body", "от 1 до 5000 символов")
)

// CommentView — комментарий в ответе: с именем автора и признаком правки.
type CommentView struct {
	Comment
	AuthorName string `json:"author_name"`
	Edited     bool   `json:"edited"`
}

func commentBody(s string) (string, error) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n < 1 || n > 5000 {
		return "", errCommentBodyRequired
	}
	return s, nil
}

// Comments — комментарии задачи по времени (§3.6). Видит участник; удалённую задачу — администратор.
func (s *Service) Comments(ctx context.Context, u *user.User, key string) ([]CommentView, error) {
	t, _, err := s.Get(ctx, u, key)
	if err != nil {
		return nil, err
	}
	list := []CommentView{}
	if err := s.db.WithContext(ctx).Raw(`SELECT c.*, u.display_name AS author_name, c.updated_at > c.created_at AS edited
		FROM comments c JOIN users u ON u.id = c.author_id
		WHERE c.task_id = ? ORDER BY c.created_at, c.id`, t.ID).Scan(&list).Error; err != nil {
		return nil, fmt.Errorf("task: comments: %w", err)
	}
	return list, nil
}

// AddComment — новый комментарий любого участника (§2). Версия задачи не меняется: комментарий
// не правит поля задачи и не должен ломать чужую правку карточки (If-Match).
// Источник — telegram для бота, иначе web; событие comment.created (§9.4).
func (s *Service) AddComment(ctx context.Context, u *user.User, key, body string) (*CommentView, error) {
	body, err := commentBody(body)
	if err != nil {
		return nil, err
	}
	var view CommentView
	err = s.onTask(ctx, u, key, func(tx *gorm.DB, t *Task, _ string) error {
		_, src := audit.From(ctx)
		source := "web"
		if src == audit.SourceBot {
			source = "telegram"
		}
		c := Comment{TaskID: t.ID, AuthorID: u.ID, Body: body, Source: source}
		if err := tx.Create(&c).Error; err != nil {
			return fmt.Errorf("task: comment: %w", err)
		}
		view = CommentView{Comment: c, AuthorName: u.DisplayName}
		preview := []rune(body)
		if len(preview) > 200 {
			preview = preview[:200]
		}
		return events.Publish(tx, events.CommentCreated, map[string]any{
			"comment_id": c.ID, "task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "title": t.Title,
			"author_id": u.ID, "body": string(preview), "assignee_id": t.AssigneeID, "reporter_id": t.ReporterID,
			"source": source,
		})
	})
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// UpdateComment — правка текста автором или менеджером (§3.6).
func (s *Service) UpdateComment(ctx context.Context, u *user.User, id uuid.UUID, body string) (*CommentView, error) {
	body, err := commentBody(body)
	if err != nil {
		return nil, err
	}
	var view CommentView
	err = s.onComment(ctx, u, id, func(tx *gorm.DB, c *Comment) error {
		if c.Body == body {
			view = CommentView{Comment: *c}
			return nil
		}
		c.Body = body
		if err := tx.Model(c).Updates(map[string]any{"body": body, "updated_at": time.Now()}).Error; err != nil {
			return fmt.Errorf("task: comment update: %w", err)
		}
		view = CommentView{Comment: *c, Edited: true}
		return tx.Raw(`SELECT display_name FROM users WHERE id = ?`, c.AuthorID).Scan(&view.AuthorName).Error
	})
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// DeleteComment — удаление автором или менеджером (§2: чужие — только менеджер).
func (s *Service) DeleteComment(ctx context.Context, u *user.User, id uuid.UUID) error {
	return s.onComment(ctx, u, id, func(tx *gorm.DB, c *Comment) error {
		return tx.Delete(c).Error
	})
}

// onComment — комментарий под FOR UPDATE с проверками задачи (onTask) и прав автора/менеджера.
func (s *Service) onComment(ctx context.Context, u *user.User, id uuid.UUID, fn func(*gorm.DB, *Comment) error) error {
	var key string
	if err := s.db.WithContext(ctx).Raw(`SELECT t.key FROM comments c JOIN tasks t ON t.id = c.task_id WHERE c.id = ?`, id).
		Scan(&key).Error; err != nil {
		return fmt.Errorf("task: comment: %w", err)
	}
	if key == "" {
		return ErrCommentNotFound
	}
	err := s.onTask(ctx, u, key, func(tx *gorm.DB, _ *Task, role string) error {
		var c Comment
		res := tx.Raw(`SELECT * FROM comments WHERE id = ? FOR UPDATE`, id).Scan(&c)
		if res.Error != nil {
			return fmt.Errorf("task: comment lock: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrCommentNotFound
		}
		if c.AuthorID != u.ID && role != project.RoleManager {
			return errCommentForbidden
		}
		return fn(tx, &c)
	})
	if errors.Is(err, ErrNotFound) {
		return ErrCommentNotFound
	}
	return err
}

// onTask — действие над задачей без изменения её полей: актор (FOR SHARE, активен), задача
// (FOR SHARE, не удалена), проект (FOR SHARE, не архив), участие. Порядок блокировок — как у change().
func (s *Service) onTask(ctx context.Context, u *user.User, key string, fn func(*gorm.DB, *Task, string) error) error {
	if keyRe.FindStringSubmatch(key) == nil {
		return ErrNotFound
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var me []struct {
			IsSystemAdmin bool
			IsActive      bool
		}
		if err := tx.Raw(`SELECT is_system_admin, is_active FROM users WHERE id = ? FOR SHARE`, u.ID).Scan(&me).Error; err != nil {
			return fmt.Errorf("task: actor: %w", err)
		}
		if len(me) == 0 || !me[0].IsActive {
			return errUserBlocked
		}
		var t Task
		res := tx.Raw(`SELECT * FROM tasks WHERE key = ? AND deleted_at IS NULL FOR SHARE`, key).Scan(&t)
		if res.Error != nil {
			return fmt.Errorf("task: lock: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		var p project.Project
		pr := tx.Raw(`SELECT * FROM projects WHERE id = ? AND deleted_at IS NULL FOR SHARE`, t.ProjectID).Scan(&p)
		if pr.Error != nil {
			return fmt.Errorf("task: project: %w", pr.Error)
		}
		if pr.RowsAffected == 0 {
			return ErrNotFound
		}
		role, err := project.RoleIn(tx, p.ID, u.ID)
		if err != nil {
			return err
		}
		if role == "" && !me[0].IsSystemAdmin {
			return ErrNotFound
		}
		if err := project.EnsureWritable(&p); err != nil {
			return err
		}
		if me[0].IsSystemAdmin && role == "" {
			role = project.RoleManager // §2: администратор системы — права менеджера
		}
		return fn(tx, &t, role)
	})
}
