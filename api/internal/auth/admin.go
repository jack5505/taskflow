package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	netmail "net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jack5505/taskflow/api/internal/mail"
	"github.com/jack5505/taskflow/api/internal/queue"
	"github.com/jack5505/taskflow/api/internal/user"
)

// Результат EnsureSystemAdmin.
type AdminResult int

const (
	AdminCreated  AdminResult = iota // новый пользователь
	AdminPromoted                    // был обычным пользователем, стал администратором
	AdminExisted                     // уже был администратором
)

var (
	ErrBadEmail       = errors.New("некорректный email")
	ErrBadDisplayName = errors.New("имя должно быть от 1 до 80 символов")
	ErrAdminInactive  = errors.New("пользователь с таким email деактивирован")
)

// EnsureSystemAdmin — первый администратор системы (ТЗ §7.1, команда `taskflow admin create`).
// Повторный вызов с тем же email дубликата не создаёт: существующий пользователь
// становится администратором. Пароля у нового пользователя нет — его задают по ссылке.
func (s *Service) EnsureSystemAdmin(ctx context.Context, email, displayName string) (*user.User, AdminResult, error) {
	addr, err := netmail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > 254 {
		return nil, 0, ErrBadEmail
	}
	if displayName == "" {
		displayName = truncateRunes(email[:strings.IndexByte(email, '@')], 80)
	}
	displayName = strings.TrimSpace(displayName)
	if n := utf8.RuneCountInString(displayName); n < 1 || n > 80 {
		return nil, 0, ErrBadDisplayName
	}

	var u user.User
	result := AdminExisted
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		nu := user.New(email, "", displayName) // пустой хеш не совпадёт ни с одним паролем
		nu.IsSystemAdmin = true
		res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "email"}}, DoNothing: true}).Create(nu)
		if res.Error != nil {
			return fmt.Errorf("auth: create admin: %w", res.Error)
		}
		if res.RowsAffected == 1 {
			u, result = *nu, AdminCreated
			return nil
		}
		found := tx.Raw(`SELECT * FROM users WHERE email = ? FOR UPDATE`, email).Scan(&u)
		if found.Error != nil {
			return fmt.Errorf("auth: find admin: %w", found.Error)
		}
		if found.RowsAffected == 0 {
			return fmt.Errorf("auth: admin %s пропал после ON CONFLICT", email)
		}
		if !u.IsActive {
			return ErrAdminInactive
		}
		if !u.IsSystemAdmin {
			if err := tx.Model(&u).Update("is_system_admin", true).Error; err != nil {
				return fmt.Errorf("auth: promote admin: %w", err)
			}
			result = AdminPromoted
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return &u, result, nil
}

// HasPassword сообщает, задан ли у пользователя пароль. Ссылку установки пароля командой
// выдаём только тем, у кого его нет: иначе по ссылке можно перехватить чужую учётную запись.
func HasPassword(u *user.User) bool { return u.PasswordHash != "" }

// PasswordSetLink выпускает ссылку установки пароля (24 часа, одноразовая) — для вывода в консоль.
func (s *Service) PasswordSetLink(ctx context.Context, u *user.User) (string, error) {
	return s.passwordLink(ctx, u.ID)
}

// SendPasswordSetLink отправляет пользователю письмо-приглашение со ссылкой установки пароля.
func (s *Service) SendPasswordSetLink(ctx context.Context, u *user.User) error {
	return s.sendLink(ctx, u, mail.PasswordSet)
}

// sendLink выпускает ссылку и ставит письмо по шаблону render. Если письмо не встало в очередь,
// ссылка останется неизвестной никому и истечёт.
func (s *Service) sendLink(ctx context.Context, u *user.User, render func(to, name, link string) (mail.Message, error)) error {
	if s.mailer == nil {
		return errNoMailer
	}
	link, err := s.passwordLink(ctx, u.ID)
	if err != nil {
		return err
	}
	m, err := render(u.Email, u.DisplayName, link)
	if err != nil {
		return err
	}
	return s.mailer.Send(ctx, m)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// TaskPasswordInvite — задание воркера: письмо-приглашение пользователю, созданному через API.
const TaskPasswordInvite = "auth:password_invite"

type invitePayload struct {
	UserID uuid.UUID `json:"user_id"`
}

// EnqueueInvite ставит задание на письмо-приглашение. Вызывается внутри транзакции создания
// пользователя, до коммита: если очередь недоступна, пользователь не создаётся, и запрос можно
// повторить; если коммит не прошёл, воркер не найдёт пользователя и задание выдохнется.
func (s *Service) EnqueueInvite(ctx context.Context, userID uuid.UUID) error {
	if s.jobs == nil {
		return errNoQueue
	}
	payload, err := json.Marshal(invitePayload{UserID: userID})
	if err != nil {
		return err
	}
	_, err = s.jobs.EnqueueContext(ctx, asynq.NewTask(TaskPasswordInvite, payload),
		asynq.Queue(queue.QueueDefault), asynq.MaxRetry(inviteMaxRetry), asynq.Timeout(time.Minute))
	if err != nil {
		return fmt.Errorf("auth: enqueue invite: %w", err)
	}
	return nil
}

// inviteMaxRetry — повторы, пока транзакция создания не закоммитится (воркер может успеть раньше).
const inviteMaxRetry = 5

// PasswordInviteHandler — обработчик TaskPasswordInvite в воркере.
func PasswordInviteHandler(s *Service) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p invitePayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("auth: invite payload: %w: %w", err, asynq.SkipRetry)
		}
		var u user.User
		err := s.db.WithContext(ctx).First(&u, "id = ?", p.UserID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("auth: invite: пользователь %s ещё не виден (транзакция не закоммичена?)", p.UserID)
		}
		if err != nil {
			return fmt.Errorf("auth: invite user: %w", err)
		}
		if !u.IsActive || HasPassword(&u) {
			return nil
		}
		return s.SendPasswordSetLink(ctx, &u)
	}
}
