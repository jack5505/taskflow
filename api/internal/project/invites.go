package project

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/user"
)

// InviteTTL — срок ссылки-приглашения (ТЗ §7.1, §3.8 ProjectInvite).
const InviteTTL = 7 * 24 * time.Hour

var (
	// ErrInviteInvalid — ссылки нет, она отозвана или истекла, проект в архиве или удалён (§7.1).
	ErrInviteInvalid  = apierr.NotFound("Ссылка-приглашение недействительна: попросите менеджера проекта прислать новую")
	ErrInviteNotFound = apierr.NotFound("Ссылка-приглашение не найдена")
	ErrInviteRemoved  = apierr.Forbidden("Вас исключили из проекта после создания этой ссылки: попросите менеджера прислать новую")
	errEmailTaken     = fieldError("email", "уже зарегистрирован: войдите и примите приглашение")
)

// Invite — строка таблицы project_invites.
type Invite struct {
	ID          uuid.UUID  `gorm:"type:uuid;primaryKey"`
	ProjectID   uuid.UUID  `gorm:"type:uuid;not null"`
	Role        string     `gorm:"not null"`
	TokenHash   string     `gorm:"not null"`
	CreatedBy   *uuid.UUID `gorm:"type:uuid"`
	ExpiresAt   time.Time  `gorm:"not null"`
	RevokedAt   *time.Time
	JoinedCount int `gorm:"not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (Invite) TableName() string { return "project_invites" }

// InviteView — действующая ссылка во вкладке «Участники» (7.9). Сам токен не хранится и не
// показывается: полная ссылка есть только в ответе на создание.
type InviteView struct {
	ID            uuid.UUID  `json:"id"`
	Role          string     `json:"role" enums:"manager,member"`
	CreatedBy     *uuid.UUID `json:"created_by"`
	CreatedByName string     `json:"created_by_name"`
	ExpiresAt     time.Time  `json:"expires_at"`
	JoinedCount   int        `json:"joined_count"`
	CreatedAt     time.Time  `json:"created_at"`
}

// CreatedInvite — новая ссылка: всё из InviteView и полный адрес страницы приглашения.
type CreatedInvite struct {
	InviteView
	URL string `json:"url"`
}

// InviteInfo — что видит человек на странице приглашения до входа: проект и роль.
type InviteInfo struct {
	ProjectKey  string    `json:"project_key"`
	ProjectName string    `json:"project_name"`
	Role        string    `json:"role" enums:"manager,member"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// JoinResult — итог вступления по ссылке. Added=false — пользователь уже был участником, его
// роль не менялась (§7.1).
type JoinResult struct {
	ProjectKey string `json:"project_key"`
	Role       string `json:"role" enums:"manager,member"`
	Added      bool   `json:"added"`
}

// Registrar — то, что регистрации по ссылке нужно от модуля входа (auth.Service).
type Registrar interface {
	TakeRegisterAttempt(ctx context.Context, ip string) error
	IssueTx(tx *gorm.DB, u *user.User, userAgent string) (*auth.Session, error)
}

// WithInvites подключает регистрацию по ссылке: вход (сессия нового пользователя, лимит попыток)
// и адрес приложения для полной ссылки.
func (s *Service) WithInvites(reg Registrar, baseURL string) *Service {
	s.registrar, s.baseURL = reg, baseURL
	return s
}

// Invites — действующие ссылки проекта, новые первыми (менеджер, §7.9).
func (s *Service) Invites(ctx context.Context, a *Access) ([]InviteView, error) {
	db := s.db.WithContext(ctx)
	rows, err := activeInvites(db, a.Project.ID, s.now())
	if err != nil {
		return nil, err
	}
	var authors []uuid.UUID
	for _, r := range rows {
		if r.CreatedBy != nil {
			authors = append(authors, *r.CreatedBy)
		}
	}
	names, err := user.Names(db, authors...)
	if err != nil {
		return nil, err
	}
	list := make([]InviteView, len(rows))
	for i, r := range rows {
		list[i] = inviteView(&r, names)
	}
	return list, nil
}

func inviteView(r *Invite, names map[uuid.UUID]user.Name) InviteView {
	v := InviteView{ID: r.ID, Role: r.Role, CreatedBy: r.CreatedBy, ExpiresAt: r.ExpiresAt, JoinedCount: r.JoinedCount, CreatedAt: r.CreatedAt}
	if r.CreatedBy != nil {
		v.CreatedByName = names[*r.CreatedBy].DisplayName
	}
	return v
}

// CreateInvite создаёт ссылку с ролью на InviteTTL (менеджер, §7.1). Права и архив проверяются
// под блокировкой проекта — как у изменения состава.
func (s *Service) CreateInvite(ctx context.Context, a *Access, role string) (*CreatedInvite, error) {
	if !ValidRole(role) {
		return nil, fieldError("role", "manager или member")
	}
	raw, hash, err := auth.NewLinkToken()
	if err != nil {
		return nil, err
	}
	var out *CreatedInvite
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := s.lockForMembers(tx, a)
		if err != nil {
			return err
		}
		now := s.now()
		by := a.userID
		inv := Invite{ID: uuid.New(), ProjectID: p.ID, Role: role, TokenHash: hash, CreatedBy: &by,
			ExpiresAt: now.Add(InviteTTL), CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&inv).Error; err != nil {
			return fmt.Errorf("project: create invite: %w", err)
		}
		if err := audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: audit.InviteCreated, EntityType: "invite",
			EntityID: inv.ID.String(), New: map[string]any{"role": role, "expires_at": inv.ExpiresAt}}); err != nil {
			return err
		}
		names, err := user.Names(tx, by)
		if err != nil {
			return err
		}
		out = &CreatedInvite{InviteView: inviteView(&inv, names), URL: s.baseURL + "/join/" + raw}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RevokeInvite отзывает ссылку проекта (менеджер). Уже отозванная или чужая — 404.
func (s *Service) RevokeInvite(ctx context.Context, a *Access, id uuid.UUID) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := s.lockForMembers(tx, a)
		if err != nil {
			return err
		}
		ok, err := revokeInvite(tx, p.ID, id, s.now())
		if err != nil {
			return err
		}
		if !ok {
			return ErrInviteNotFound
		}
		return audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: audit.InviteRevoked, EntityType: "invite", EntityID: id.String()})
	})
}

// InviteInfo — проект и роль по токену из ссылки; недействительная ссылка — 404 (§7.1).
func (s *Service) InviteInfo(ctx context.Context, token string) (*InviteInfo, error) {
	db := s.db.WithContext(ctx)
	inv, err := s.validInvite(db, token)
	if err != nil {
		return nil, err
	}
	p, err := projectByID(db, inv.ProjectID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrInviteInvalid
	}
	if err != nil {
		return nil, err
	}
	if p.IsArchived {
		return nil, ErrInviteInvalid
	}
	return &InviteInfo{ProjectKey: p.Key, ProjectName: p.Name, Role: inv.Role, ExpiresAt: inv.ExpiresAt}, nil
}

// validInvite — неотозванная и неистёкшая ссылка по токену.
func (s *Service) validInvite(db *gorm.DB, token string) (*Invite, error) {
	inv, err := inviteByHash(db, auth.HashLinkToken(token))
	if err != nil {
		return nil, err
	}
	if inv == nil || inv.RevokedAt != nil || !s.now().Before(inv.ExpiresAt) {
		return nil, ErrInviteInvalid
	}
	return inv, nil
}

// AcceptInvite — вошедший пользователь u присоединяется к проекту по ссылке (§7.1). Уже участник —
// роль не меняется (Added=false). Блокировки: пользователь → проект, как у изменения состава.
func (s *Service) AcceptInvite(ctx context.Context, token string, u *user.User) (*JoinResult, error) {
	var out *JoinResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockUser(tx, u.ID)
		if err != nil {
			return err
		}
		if !row.IsActive {
			return ErrInviteInvalid
		}
		out, err = s.joinTx(tx, token, u.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RegisterInput — регистрация по ссылке: данные формы и откуда пришёл запрос.
type RegisterInput struct {
	Email, DisplayName, Password string
	IP, UserAgent                string
}

// Register создаёт пользователя по ссылке-приглашению, делает его участником проекта с ролью из
// ссылки и выпускает сессию — одним коммитом (§7.1). Email письмом не подтверждается; занятый —
// 400 по полю email (страница предлагает войти). Не больше 10 попыток в час с IP.
func (s *Service) Register(ctx context.Context, token string, in RegisterInput) (*auth.Session, error) {
	if err := s.registrar.TakeRegisterAttempt(ctx, in.IP); err != nil {
		return nil, err
	}
	// Недействительная ссылка — 404 раньше ошибок формы и до дорогого bcrypt; проект и повторная
	// проверка — в joinTx под замком.
	if _, err := s.validInvite(s.db.WithContext(ctx), token); err != nil {
		return nil, err
	}
	name, ok := user.NormalizeDisplayName(in.DisplayName)
	if !ok {
		return nil, fieldError("display_name", fmt.Sprintf("от 1 до %d символов", user.MaxDisplayNameRunes))
	}
	if err := auth.ValidatePassword("password", in.Password); err != nil {
		return nil, err
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u := user.New(in.Email, hash, name)
	u.ID = uuid.New()
	// Актор записей аудита — сам новый пользователь: регистрация идёт без входа.
	var sess *auth.Session
	err = s.db.WithContext(audit.WithActor(ctx, u.ID)).Transaction(func(tx *gorm.DB) error {
		created, err := user.CreateNew(tx, u)
		if err != nil {
			return err
		}
		if !created {
			return errEmailTaken
		}
		if err := audit.Write(tx, audit.Record{Action: audit.UserCreated, EntityType: "user", EntityID: u.ID.String(),
			New: map[string]any{"email": u.Email, "display_name": u.DisplayName, "timezone": u.Timezone, "is_system_admin": false, "via": "invite"}}); err != nil {
			return err
		}
		if _, err := s.joinTx(tx, token, u.ID); err != nil {
			return err
		}
		sess, err = s.registrar.IssueTx(tx, u, in.UserAgent)
		return err
	})
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// joinTx — вступление userID по ссылке в транзакции tx. Блокирует строку проекта (FOR UPDATE),
// затем строку ссылки: отзыв ссылки берёт тот же замок проекта, поэтому отозванная ссылка после
// коммита отзыва никого не впустит. Строку пользователя вызывающий блокирует раньше (или создал её).
func (s *Service) joinTx(tx *gorm.DB, token string, userID uuid.UUID) (*JoinResult, error) {
	inv, err := s.validInvite(tx, token)
	if err != nil {
		return nil, err
	}
	var p Project
	if err := lockProject(tx, "id = ?", inv.ProjectID, &p); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrInviteInvalid
		}
		return nil, err
	}
	if p.IsArchived {
		return nil, ErrInviteInvalid
	}
	// Под замком проекта — ещё раз: ссылку могли отозвать между чтением и блокировкой.
	if inv, err = s.validInvite(tx, token); err != nil {
		return nil, err
	}
	current, err := memberRole(tx, p.ID, userID)
	if err != nil {
		return nil, err
	}
	if current != "" {
		return &JoinResult{ProjectKey: p.Key, Role: current}, nil
	}
	// Исключённый после создания ссылки по ней не возвращается: исключение не должно отменяться
	// тем, что у человека осталась многоразовая ссылка (§7.1).
	removed, err := audit.RemovedSince(tx, p.ID, userID, inv.CreatedAt)
	if err != nil {
		return nil, err
	}
	if removed {
		return nil, ErrInviteRemoved
	}
	if err := tx.Create(&Member{ProjectID: p.ID, UserID: userID, Role: inv.Role}).Error; err != nil {
		return nil, fmt.Errorf("project: join: %w", err)
	}
	if err := countInviteJoin(tx, inv.ID); err != nil {
		return nil, err
	}
	if err := events.Publish(tx, events.MemberAdded, map[string]any{
		"project_id": p.ID, "key": p.Key, "user_id": userID, "role": inv.Role, "by": userID, "invite_id": inv.ID,
	}); err != nil {
		return nil, err
	}
	if err := audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: audit.MemberAdded, EntityType: "user",
		EntityID: userID.String(), New: map[string]any{"role": inv.Role, "invite_id": inv.ID}}); err != nil {
		return nil, err
	}
	return &JoinResult{ProjectKey: p.Key, Role: inv.Role, Added: true}, nil
}
