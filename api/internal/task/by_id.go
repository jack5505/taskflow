package task

import (
	"context"

	"github.com/google/uuid"
)

// KeyByID — ключ неудалённой задачи по id: кнопки бота несут id задачи (§8.8, take:<task_id>).
// Права здесь не проверяются — их проверяет операция, которую затем вызывают по ключу.
// Нет такой задачи или она удалена — ErrNotFound.
func (s *Service) KeyByID(ctx context.Context, id uuid.UUID) (string, error) {
	key, err := liveKeyByID(s.db.WithContext(ctx), id)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", ErrNotFound
	}
	return key, nil
}
