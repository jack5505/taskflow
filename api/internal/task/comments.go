package task

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
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
	db := s.db.WithContext(ctx)
	if err := taskComments(db, t.ID, &list); err != nil {
		return nil, err
	}
	if err := fillCommentNames(db, list); err != nil {
		return nil, err
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
	if keyRe.FindStringSubmatch(key) == nil {
		return nil, ErrNotFound
	}
	var view CommentView
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, err := openTask(tx, u, key, preLock{}, forShare)
		if err != nil {
			return err
		}
		if err := project.EnsureWritable(&o.project); err != nil {
			return err
		}
		t := &o.task
		c := Comment{TaskID: t.ID, AuthorID: u.ID, Body: body, Source: commentSource(o.actor.source)}
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
			"source": c.Source,
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
	var view *CommentView
	err = s.onComment(ctx, u, id, func(tx *gorm.DB, c *Comment) error {
		if c.Body != body {
			if err := tx.Model(c).Updates(map[string]any{"body": body, "updated_at": s.now()}).Error; err != nil {
				return fmt.Errorf("task: comment update: %w", err)
			}
		}
		var err error
		view, err = commentView(tx, c.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// DeleteComment — удаление автором или менеджером (§2: чужие — только менеджер).
func (s *Service) DeleteComment(ctx context.Context, u *user.User, id uuid.UUID) error {
	return s.onComment(ctx, u, id, func(tx *gorm.DB, c *Comment) error {
		return tx.Delete(c).Error
	})
}

func commentView(tx *gorm.DB, id uuid.UUID) (*CommentView, error) {
	v, err := commentsByID(tx, id)
	if err != nil {
		return nil, err
	}
	if len(v) == 0 {
		return &CommentView{}, nil // как прежний JOIN без строк: пустое представление
	}
	if err := fillCommentNames(tx, v); err != nil {
		return nil, err
	}
	return &v[0], nil
}

// onComment — в одной транзакции: задача комментария (openTask, FOR SHARE), комментарий FOR UPDATE
// с проверкой, что он этой задачи, права автора/менеджера (403) и только затем архив (409).
func (s *Service) onComment(ctx context.Context, u *user.User, id uuid.UUID, fn func(*gorm.DB, *Comment) error) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		key, err := commentTaskKey(tx, id)
		if err != nil {
			return err
		}
		if key == "" {
			return ErrCommentNotFound
		}
		o, err := openTask(tx, u, key, preLock{}, forShare)
		if err != nil {
			return err
		}
		c, err := lockComment(tx, id, o.task.ID)
		if err != nil {
			return err
		}
		if c == nil {
			return ErrCommentNotFound
		}
		if c.AuthorID != u.ID && !o.actor.manager {
			return errCommentForbidden
		}
		if err := project.EnsureWritable(&o.project); err != nil {
			return err
		}
		return fn(tx, c)
	})
	if errors.Is(err, ErrNotFound) {
		return ErrCommentNotFound
	}
	return err
}
