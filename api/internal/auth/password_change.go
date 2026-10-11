package auth

import (
	"context"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/user"
)

var (
	// ErrBadCurrentPassword — при смене пароля текущий введён неверно (поле current_password).
	ErrBadCurrentPassword = apierr.BadRequest(apierr.CodeValidation, "Текущий пароль введён неверно").
				With("fields", []apierr.FieldError{{Field: "current_password", Reason: "неверный пароль"}})
	// ErrPasswordChangeLocked — ввод текущего пароля закрыт после серии неудач (§10.3).
	ErrPasswordChangeLocked = apierr.RateLimited("Слишком много неверных попыток ввода текущего пароля, подождите")
)

// ChangePassword — смена пароля в профиле с вводом текущего (ТЗ §7.8, §9.1).
//
// Неверный текущий пароль засчитывается в счётчик pwLock — те же 10 неудач за 15 минут, что
// у входа (§10.3), но по id пользователя и отдельно от счётчика входа: опечатки в форме профиля
// не должны закрывать вход по email на других устройствах, а неудачи входа — смену пароля.
// Подобрать пароль через этот эндпоинт можно только с действующим access-токеном, и перебор
// в нём ограничен так же, как во входе.
//
// После смены отзываются все сессии пользователя, кроме keepRefresh — refresh-токена клиента,
// который меняет пароль (cookie запроса): он остаётся в системе. Пустой keepRefresh — отзываются все.
// Заодно гасятся неиспользованные ссылки установки пароля: выданные до смены не должны его перебить.
func (s *Service) ChangePassword(ctx context.Context, u *user.User, current, next, keepRefresh string) error {
	// Новый пароль проверяем до текущего: ошибка в нём не тратит попытку и не стоит bcrypt.
	if utf8.RuneCountInString(next) < MinPasswordLength {
		return newPasswordFieldError("не короче 10 символов")
	}
	if len(next) > MaxPasswordBytes {
		return newPasswordFieldError("не длиннее 72 байт")
	}
	id := u.ID.String()
	if locked, wait, err := s.pwLock.locked(ctx, id); err != nil {
		return err
	} else if locked {
		return ErrPasswordChangeLocked.With("retry_after_seconds", int(wait.Seconds()))
	}

	// Хеш перечитываем из базы: пользователь в контексте запроса мог устареть.
	var stored user.User
	if err := s.db.WithContext(ctx).Select("id", "password_hash").First(&stored, "id = ?", u.ID).Error; err != nil {
		return fmt.Errorf("auth: load user: %w", err)
	}
	if !CheckPassword(stored.PasswordHash, current) {
		if err := s.pwLock.fail(ctx, id); err != nil {
			return err
		}
		return ErrBadCurrentPassword
	}
	hash, err := HashPassword(next)
	if err != nil {
		return fmt.Errorf("auth: hash password: %w", err)
	}

	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Условие по старому хешу: если пароль успели сменить параллельно (другая вкладка,
		// ссылка из письма), проверенный выше «текущий» уже не текущий.
		res := tx.Model(&user.User{}).Where("id = ? AND password_hash = ?", u.ID, stored.PasswordHash).
			Update("password_hash", hash)
		if res.Error != nil {
			return fmt.Errorf("auth: save password: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return ErrBadCurrentPassword
		}
		now := s.now()
		if err := tx.Model(&PasswordToken{}).Where("user_id = ? AND used_at IS NULL", u.ID).
			Update("used_at", now).Error; err != nil {
			return fmt.Errorf("auth: use password links: %w", err)
		}
		revoke := tx.Model(&RefreshToken{}).Where("user_id = ? AND revoked_at IS NULL", u.ID)
		if keepRefresh != "" {
			revoke = revoke.Where("token_hash <> ?", hashToken(keepRefresh))
		}
		if err := revoke.Update("revoked_at", now).Error; err != nil {
			return fmt.Errorf("auth: revoke sessions: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := s.pwLock.reset(ctx, id); err != nil {
		// Пароль уже сменён; счётчик истечёт сам.
		slog.Warn("auth: pwchange lockout reset", "err", err)
	}
	return nil
}

func newPasswordFieldError(reason string) error {
	return apierr.BadRequest(apierr.CodeValidation, "Некорректные данные запроса").
		With("fields", []apierr.FieldError{{Field: "new_password", Reason: reason}})
}
