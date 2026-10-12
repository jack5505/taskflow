// Package taskdb — запросы модуля task к таблице tasks, которые нужны модулю project. Отдельный
// пакет без зависимостей от project: task импортирует project, и project не может импортировать
// task без цикла. Владелец таблицы тот же — модуль task (api/CLAUDE.md).
package taskdb

import (
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OpenCounts — открытые задачи проекта (не удалённые, не done и не cancelled): всего и с
// исполнителем userID.
type OpenCounts struct {
	Open int
	Mine int
}

// OpenCountsOf — OpenCounts проектов projectIDs; проекта без открытых задач в ответе нет.
// Блокировок не берёт.
func OpenCountsOf(db *gorm.DB, projectIDs []uuid.UUID, userID uuid.UUID) (map[uuid.UUID]OpenCounts, error) {
	out := make(map[uuid.UUID]OpenCounts, len(projectIDs))
	if len(projectIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		ProjectID uuid.UUID
		OpenCounts
	}
	if err := db.Raw(`SELECT project_id, count(*) AS open, count(*) FILTER (WHERE assignee_id = ?) AS mine
		FROM tasks WHERE project_id IN ? AND deleted_at IS NULL AND status NOT IN ('done', 'cancelled')
		GROUP BY project_id`, userID, projectIDs).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: open counts: %w", err)
	}
	for _, r := range rows {
		out[r.ProjectID] = r.OpenCounts
	}
	return out, nil
}
