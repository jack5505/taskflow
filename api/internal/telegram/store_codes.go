package telegram

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Запросы одноразовых кодов (§8.1, §8.2): one_time_codes.

// dropOpenCodes удаляет неиспользованные коды вида kind владельца ownerID; owner — колонка
// владельца: user_id (привязка аккаунта) или project_id (подключение группы).
func dropOpenCodes(tx *gorm.DB, kind, owner string, ownerID uuid.UUID) error {
	if owner != "user_id" && owner != "project_id" {
		return fmt.Errorf("telegram: unknown code owner %q", owner)
	}
	if err := tx.Exec(`DELETE FROM one_time_codes WHERE kind = ? AND `+owner+` = ? AND used_at IS NULL`, kind, ownerID).Error; err != nil {
		return fmt.Errorf("telegram: drop old codes: %w", err)
	}
	return nil
}

// insertCode записывает код c; такой код уже есть у другого — inserted = false.
func insertCode(tx *gorm.DB, c *OneTimeCode) (inserted bool, err error) {
	res := tx.Exec(`INSERT INTO one_time_codes (id, kind, code, user_id, project_id, expires_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (code) DO NOTHING`, c.ID, c.Kind, c.Code, c.UserID, c.ProjectID, c.ExpiresAt)
	if res.Error != nil {
		return false, fmt.Errorf("telegram: insert code: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// codeProject — проект кода подключения группы без блокировки; нет кода — nil.
func codeProject(tx *gorm.DB, code, kind string) (*uuid.UUID, error) {
	var ids []uuid.UUID
	if err := tx.Raw(`SELECT project_id FROM one_time_codes WHERE code = ? AND kind = ?`, code, kind).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("telegram: find bind code: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return &ids[0], nil
}

// lockCode — код вида kind, строка FOR UPDATE; нет — nil.
func lockCode(tx *gorm.DB, code, kind string) (*OneTimeCode, error) {
	var c OneTimeCode
	res := tx.Raw(`SELECT * FROM one_time_codes WHERE code = ? AND kind = ? FOR UPDATE`, code, kind).Scan(&c)
	if res.Error != nil {
		return nil, fmt.Errorf("telegram: find code: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &c, nil
}

// useCode гасит код id (используется в момент at).
func useCode(tx *gorm.DB, id uuid.UUID, at time.Time) error {
	if err := tx.Exec(`UPDATE one_time_codes SET used_at = ?, updated_at = now() WHERE id = ?`, at, id).Error; err != nil {
		return fmt.Errorf("telegram: use code: %w", err)
	}
	return nil
}

// deleteCodesExpiredBefore удаляет коды, истёкшие раньше before; возвращает их число.
func deleteCodesExpiredBefore(db *gorm.DB, before time.Time) (int64, error) {
	res := db.Exec(`DELETE FROM one_time_codes WHERE expires_at < ?`, before)
	if res.Error != nil {
		return 0, fmt.Errorf("telegram: cleanup codes: %w", res.Error)
	}
	return res.RowsAffected, nil
}
