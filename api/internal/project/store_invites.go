package project

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// activeInvites — неотозванные и неистёкшие ссылки проекта, новые первыми.
func activeInvites(db *gorm.DB, projectID uuid.UUID, now time.Time) ([]Invite, error) {
	var rows []Invite
	if err := db.Raw(`SELECT * FROM project_invites WHERE project_id = ? AND revoked_at IS NULL AND expires_at > ?
		ORDER BY created_at DESC, id`, projectID, now).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: invites: %w", err)
	}
	return rows, nil
}

// inviteByHash — ссылка по хешу токена (любая, в том числе отозванная); нет — nil.
func inviteByHash(db *gorm.DB, hash string) (*Invite, error) {
	var rows []Invite
	if err := db.Raw(`SELECT * FROM project_invites WHERE token_hash = ?`, hash).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("project: invite: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// revokeInvite отзывает действующую ссылку проекта; ok=false — нет такой или уже отозвана.
func revokeInvite(tx *gorm.DB, projectID, id uuid.UUID, now time.Time) (bool, error) {
	res := tx.Exec(`UPDATE project_invites SET revoked_at = ?, updated_at = ? WHERE id = ? AND project_id = ? AND revoked_at IS NULL`,
		now, now, id, projectID)
	if res.Error != nil {
		return false, fmt.Errorf("project: revoke invite: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// countInviteJoin — ещё один человек присоединился по ссылке.
func countInviteJoin(tx *gorm.DB, id uuid.UUID) error {
	if err := tx.Exec(`UPDATE project_invites SET joined_count = joined_count + 1, updated_at = now() WHERE id = ?`, id).Error; err != nil {
		return fmt.Errorf("project: count invite join: %w", err)
	}
	return nil
}

// projectByID — неудалённый проект по id; нет — ErrNotFound.
func projectByID(db *gorm.DB, id uuid.UUID) (*Project, error) {
	var p Project
	err := db.First(&p, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("project: by id: %w", err)
	}
	return &p, nil
}
