package auth

import (
	"context"
	"fmt"
	"log/slog"

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
// Перебор текущего пароля ограничен счётчиком pwLock — те же 10 неудач за 15 минут, что
// у входа (§10.3), но по id пользователя и отдельно от счётчика входа: опечатки в форме профиля
// не должны закрывать вход по email на других устройствах, а неудачи входа — смену пароля.
// Попытка засчитывается до проверки (take), успех счётчик сбрасывает: параллельные запросы
// не проверят больше 10 паролей за окно.
//
// После смены отзываются все сессии пользователя, кроме keepRefresh — refresh-токена клиента,
// который меняет пароль (cookie запроса): он остаётся в системе. Пустой keepRefresh — отзываются все.
// Заодно гасятся неиспользованные ссылки установки пароля: выданные до смены не должны его перебить.
func (s *Service) ChangePassword(ctx context.Context, u *user.User, current, next, keepRefresh string) error {
	// Новый пароль проверяем до текущего: ошибка в нём не тратит попытку и не стоит bcrypt.
	if err := validatePassword("new_password", next); err != nil {
		return err
	}
	id := u.ID.String()
	n, locked, wait, err := s.pwLock.take(ctx, id)
	if err != nil {
		return err
	}
	if locked {
		return ErrPasswordChangeLocked.With("retry_after_seconds", int(wait.Seconds()))
	}

	// Хеш перечитываем из базы: пользователь в контексте запроса мог устареть.
	stored, err := user.ByID(s.db.WithContext(ctx), u.ID)
	if err != nil {
		s.release(ctx, s.pwLock, id) // пароль не проверен — попытка не в счёт
		return fmt.Errorf("auth: load user: %w", err)
	}
	if !CheckPassword(stored.PasswordHash, current) {
		if err := s.pwLock.takeFailed(ctx, id, n); err != nil {
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
		changed, err := user.SetPasswordIf(tx, u.ID, stored.PasswordHash, hash)
		if err != nil {
			return err
		}
		if !changed {
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
	// Пароль уже сменён; при сбое Redis счётчики истекут сами. Счётчик входа сбрасываем,
	// как и при установке пароля по ссылке: с новым паролем вход должен открыться сразу.
	if err := s.pwLock.reset(ctx, id); err != nil {
		slog.Warn("auth: pwchange lockout reset", "err", err)
	}
	if err := s.lock.reset(ctx, u.Email); err != nil {
		slog.Warn("auth: lockout reset after password change", "err", err)
	}
	return nil
}
