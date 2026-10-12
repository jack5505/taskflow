package audit

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// pageCursor — ключ продолжения: время и id последней выданной записи.
type pageCursor struct {
	at time.Time
	id uuid.UUID
}

// entries — записи журнала проекта по фильтру f после after, новые первыми, до limit.
func entries(db *gorm.DB, projectID uuid.UUID, f Filter, after *pageCursor, limit int) ([]Entry, error) {
	where := []string{"project_id = ?"}
	args := []any{projectID}
	if f.From != nil {
		where, args = append(where, "created_at >= ?"), append(args, *f.From)
	}
	if f.To != nil {
		where, args = append(where, "created_at < ?"), append(args, *f.To)
	}
	if f.ActorID != nil {
		where, args = append(where, "actor_id = ?"), append(args, *f.ActorID)
	}
	if f.Action != "" {
		where, args = append(where, "action = ?"), append(args, f.Action)
	}
	if after != nil {
		where, args = append(where, "(created_at, id) < (?, ?)"), append(args, after.at, after.id)
	}
	items := []Entry{}
	if err := db.Raw(`SELECT * FROM audit_logs WHERE `+strings.Join(where, " AND ")+
		` ORDER BY created_at DESC, id DESC LIMIT ?`, append(args, limit)...).Scan(&items).Error; err != nil {
		return nil, fmt.Errorf("audit: list: %w", err)
	}
	return items, nil
}

// parseCursor — курсор страницы; пустой — nil (первая страница).
func parseCursor(c string) (*pageCursor, error) {
	if c == "" {
		return nil, nil
	}
	at, id, err := decodeCursor(c)
	if err != nil {
		return nil, err
	}
	return &pageCursor{at, id}, nil
}
