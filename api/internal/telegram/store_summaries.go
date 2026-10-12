package telegram

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Запросы отметок ежедневных сводок (§8.6): daily_summaries.

// markSummary отмечает сводку проекта за дату; уже отмечена (другой процесс) — marked = false.
func markSummary(db *gorm.DB, projectID uuid.UUID, date time.Time) (marked bool, err error) {
	res := db.Exec(`INSERT INTO daily_summaries (project_id, date) VALUES (?, ?) ON CONFLICT DO NOTHING`,
		projectID, date.Format(time.DateOnly))
	if res.Error != nil {
		return false, fmt.Errorf("telegram: summary mark: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// unmarkSummary снимает отметку сводки проекта за дату.
func unmarkSummary(db *gorm.DB, projectID uuid.UUID, date time.Time) error {
	if err := db.Exec(`DELETE FROM daily_summaries WHERE project_id = ? AND date = ?`,
		projectID, date.Format(time.DateOnly)).Error; err != nil {
		return fmt.Errorf("telegram: summary unmark: %w", err)
	}
	return nil
}

// summaryMark — отметка сводки.
type summaryMark struct {
	ProjectID uuid.UUID
	Date      time.Time
}

// summaryMarks — отметки проектов projectIDs за даты dates (ГГГГ-ММ-ДД).
func summaryMarks(db *gorm.DB, projectIDs []uuid.UUID, dates []string) ([]summaryMark, error) {
	var rows []summaryMark
	if err := db.Raw(`SELECT project_id, date FROM daily_summaries WHERE project_id IN ? AND date IN ?`,
		projectIDs, dates).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("telegram: sent summaries: %w", err)
	}
	return rows, nil
}

// deleteSummariesBefore удаляет отметки с датой раньше before; возвращает их число.
func deleteSummariesBefore(db *gorm.DB, before time.Time) (int64, error) {
	res := db.Exec(`DELETE FROM daily_summaries WHERE date < ?`, before.Format(time.DateOnly))
	if res.Error != nil {
		return 0, fmt.Errorf("telegram: cleanup summaries: %w", res.Error)
	}
	return res.RowsAffected, nil
}
