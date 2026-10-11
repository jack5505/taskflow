package task

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// KeyByID — ключ неудалённой задачи по id: кнопки бота несут id задачи (§8.8, take:<task_id>).
// Права здесь не проверяются — их проверяет операция, которую затем вызывают по ключу.
// Нет такой задачи или она удалена — ErrNotFound.
func (s *Service) KeyByID(ctx context.Context, id uuid.UUID) (string, error) {
	var keys []string
	if err := s.db.WithContext(ctx).Raw(`SELECT key FROM tasks WHERE id = ? AND deleted_at IS NULL`, id).Scan(&keys).Error; err != nil {
		return "", fmt.Errorf("task: key by id: %w", err)
	}
	if len(keys) == 0 {
		return "", ErrNotFound
	}
	return keys[0], nil
}
