package auth

import (
	"fmt"

	"gorm.io/gorm"
)

// Запросы токенов: password_tokens и refresh_tokens.

// lockPasswordToken — ссылка установки пароля по хешу, строка FOR UPDATE; нет — ErrBadPasswordToken.
func lockPasswordToken(tx *gorm.DB, hash string) (*PasswordToken, error) {
	var pt PasswordToken
	res := tx.Raw(`SELECT id, user_id, token_hash, expires_at, used_at, created_at
		FROM password_tokens WHERE token_hash = ? FOR UPDATE`, hash).Scan(&pt)
	if res.Error != nil {
		return nil, fmt.Errorf("auth: find password link: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, ErrBadPasswordToken
	}
	return &pt, nil
}

// lockRefreshToken — refresh-токен по хешу, строка FOR UPDATE; нет — ErrBadRefresh.
func lockRefreshToken(tx *gorm.DB, hash string) (*RefreshToken, error) {
	var rt RefreshToken
	res := tx.Raw(`SELECT id, user_id, token_hash, expires_at, revoked_at, rotated, user_agent, created_at
		FROM refresh_tokens WHERE token_hash = ? FOR UPDATE`, hash).Scan(&rt)
	if res.Error != nil {
		return nil, fmt.Errorf("auth: find refresh: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, ErrBadRefresh
	}
	return &rt, nil
}
