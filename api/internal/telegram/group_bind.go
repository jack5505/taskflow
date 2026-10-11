package telegram

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/project"
)

// Действия журнала (§3.8: подключение групп).
const (
	AuditGroupBound   = "project.telegram_bound"
	AuditGroupUnbound = "project.telegram_unbound"
)

// Ошибки подключения группы (§8.2, шаг 3); тексты ответов бота — в texts.go.
var (
	// ErrNotManager — подключать и отключать группу может только менеджер проекта (или администратор).
	ErrNotManager = apierr.Forbidden("Подключать и отключать Telegram-группу может только менеджер проекта")
	// ErrProjectHasGroup — у проекта уже есть другая группа: сначала её нужно отключить.
	ErrProjectHasGroup = errors.New("telegram: к проекту уже подключена другая группа")
	// ErrGroupNotBound — проект не подключён к этой группе.
	ErrGroupNotBound = errors.New("telegram: проект не подключён к этой группе")
	// errNotMember — пользователь не участник проекта: в API это 404 (§2), бот отвечает как
	// участнику без прав.
	errNotMember = fmt.Errorf("%w: пользователь не участник", project.ErrNotFound)
)

// BindCode — выданный код подключения группы (§8.2, шаг 1).
type BindCode struct {
	Code      string    `json:"code" example:"K7M2X9QD"`
	ExpiresAt time.Time `json:"expires_at"`
	// Command — что написать в группе: /bind <код>.
	Command string `json:"command" example:"/bind K7M2X9QD"`
	// BotUsername — имя бота без @, чтобы найти его при добавлении в группу; пусто, если не задано.
	BotUsername string `json:"bot_username" example:"taskflow_bot"`
}

// Groups — подключение Telegram-групп к проектам (§8.2). Один сервис для настроек проекта в вебе
// (POST /projects/{key}/telegram-bind-code, DELETE /projects/{key}/telegram) и для бота
// (/bind, /unbind, служебные обновления группы).
type Groups struct {
	db          *gorm.DB
	botUsername string
	now         func() time.Time
}

func NewGroups(db *gorm.DB, botUsername string) *Groups {
	return &Groups{db: db, botUsername: strings.TrimPrefix(botUsername, "@"), now: time.Now}
}

// IssueBindCode выдаёт код подключения группы на 15 минут. Право менеджера проверяется под
// блокировкой проекта; прежние неиспользованные коды проекта гасятся: действует последний.
func (g *Groups) IssueBindCode(ctx context.Context, a *project.Access) (*BindCode, error) {
	c := OneTimeCode{ID: uuid.New(), Kind: CodeGroupBind, ProjectID: &a.Project.ID, ExpiresAt: g.now().Add(BindCodeTTL)}
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := lockManaged(tx, a.Project.ID, a.UserID()); err != nil {
			return err
		}
		if err := tx.Exec(`DELETE FROM one_time_codes WHERE kind = ? AND project_id = ? AND used_at IS NULL`,
			CodeGroupBind, a.Project.ID).Error; err != nil {
			return fmt.Errorf("telegram: drop old bind codes: %w", err)
		}
		for range 3 {
			c.Code = newCode()
			res := tx.Exec(`INSERT INTO one_time_codes (id, kind, code, project_id, expires_at) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (code) DO NOTHING`, c.ID, c.Kind, c.Code, a.Project.ID, c.ExpiresAt)
			if res.Error != nil {
				return fmt.Errorf("telegram: insert bind code: %w", res.Error)
			}
			if res.RowsAffected == 1 {
				return nil
			}
		}
		return errors.New("telegram: не удалось выдать уникальный код")
	})
	if err != nil {
		return nil, err
	}
	return &BindCode{Code: c.Code, ExpiresAt: c.ExpiresAt.UTC(), Command: "/bind " + c.Code, BotUsername: g.botUsername}, nil
}

// Bind подключает группу chatID к проекту по коду (§8.2, шаги 3–4): userID — менеджер проекта,
// код действует, у проекта нет другой группы. Повторное подключение той же группы — успех.
// Проект возвращается и при ошибке, если он известен (для текста ответа). Ошибки:
// ErrCodeInvalid, ErrNotManager, ErrProjectHasGroup, project.ErrArchived.
func (g *Groups) Bind(ctx context.Context, code string, chatID int64, title string, userID uuid.UUID) (*project.Project, error) {
	var p *project.Project
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Порядок блокировок как в IssueBindCode — сначала проект, потом его коды: иначе
		// одновременные «Обновить» в вебе и /bind в группе ждали бы друг друга (deadlock).
		var projectID []string
		if err := tx.Raw(`SELECT project_id FROM one_time_codes WHERE code = ? AND kind = ?`,
			NormalizeCode(code), CodeGroupBind).Scan(&projectID).Error; err != nil {
			return fmt.Errorf("telegram: find bind code: %w", err)
		}
		if len(projectID) == 0 {
			return ErrCodeInvalid
		}
		var err error
		if p, err = lockManaged(tx, uuid.MustParse(projectID[0]), userID); err != nil {
			if errors.Is(err, project.ErrNotFound) && !errors.Is(err, errNotMember) {
				return ErrCodeInvalid // проект удалён после выдачи кода
			}
			return err
		}
		var c OneTimeCode
		res := tx.Raw(`SELECT * FROM one_time_codes WHERE code = ? AND kind = ? FOR UPDATE`,
			NormalizeCode(code), CodeGroupBind).Scan(&c)
		if res.Error != nil {
			return fmt.Errorf("telegram: find bind code: %w", res.Error)
		}
		if res.RowsAffected == 0 || c.UsedAt != nil || !g.now().Before(c.ExpiresAt) {
			return ErrCodeInvalid
		}
		if p.TelegramChatID != nil && *p.TelegramChatID != chatID {
			return ErrProjectHasGroup
		}
		if err := tx.Exec(`UPDATE one_time_codes SET used_at = ?, updated_at = now() WHERE id = ?`, g.now(), c.ID).Error; err != nil {
			return fmt.Errorf("telegram: use bind code: %w", err)
		}
		if p.TelegramChatID != nil {
			return nil // та же группа: ничего не меняется
		}
		if err := tx.Exec(`UPDATE projects SET telegram_chat_id = ?, telegram_chat_title = ?, updated_at = now() WHERE id = ?`,
			chatID, nullable(title), p.ID).Error; err != nil {
			return fmt.Errorf("telegram: bind group: %w", err)
		}
		p.TelegramChatID, p.TelegramChatTitle = &chatID, nullable(title)
		return audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: AuditGroupBound, EntityType: "project", EntityID: p.ID.String(),
			New: map[string]any{"telegram_chat_id": chatID, "telegram_chat_title": title}})
	})
	return p, err
}

// ChatProjects — проекты, подключённые к группе chatID, по ключу.
func (g *Groups) ChatProjects(ctx context.Context, chatID int64) ([]project.Project, error) {
	var ps []project.Project
	if err := g.db.WithContext(ctx).Where("telegram_chat_id = ?", chatID).Order("key").Find(&ps).Error; err != nil {
		return nil, fmt.Errorf("telegram: chat projects: %w", err)
	}
	return ps, nil
}

// Unbind отключает проект от группы chatID командой /unbind (§8.2, шаг 5): только менеджер этого
// проекта. Проект уже не в этой группе — ErrGroupNotBound.
func (g *Groups) Unbind(ctx context.Context, projectID uuid.UUID, chatID int64, userID uuid.UUID) (*project.Project, error) {
	var p *project.Project
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if p, err = lockManaged(tx, projectID, userID); err != nil {
			return err
		}
		if p.TelegramChatID == nil || *p.TelegramChatID != chatID {
			return ErrGroupNotBound
		}
		return unbind(tx, p)
	})
	return p, err
}

// Disconnect — кнопка «Отключить» в настройках проекта (DELETE /projects/{key}/telegram).
// Группы нет — ничего не делает.
func (g *Groups) Disconnect(ctx context.Context, a *project.Access) error {
	return g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := lockManaged(tx, a.Project.ID, a.UserID())
		if err != nil || p.TelegramChatID == nil {
			return err
		}
		return unbind(tx, p)
	})
}

// BotRemoved — бота удалили из группы (§8.2, шаг 5): привязка снимается у всех проектов группы.
// Возвращает отключённые проекты (для уведомления менеджеров). Действие системное: без актора.
func (g *Groups) BotRemoved(ctx context.Context, chatID int64) ([]project.Project, error) {
	var ps []project.Project
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Raw(`SELECT * FROM projects WHERE telegram_chat_id = ? AND deleted_at IS NULL ORDER BY key FOR UPDATE`,
			chatID).Scan(&ps).Error; err != nil {
			return fmt.Errorf("telegram: chat projects: %w", err)
		}
		for i := range ps {
			if err := unbind(tx, &ps[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ps, nil
}

// Migrate — группа стала супергруппой (§8.2, шаг 6): telegram_chat_id всех её проектов
// переписывается на новый. Сообщения задач остались в старом чате и в новом не редактируются —
// их номера забываются, карточки бот опубликует заново. Повтор (второе служебное сообщение) ничего
// не меняет.
func (g *Groups) Migrate(ctx context.Context, from, to int64) (int64, error) {
	var n int64
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []string
		if err := tx.Raw(`SELECT id FROM projects WHERE telegram_chat_id = ? FOR UPDATE`, from).Scan(&rows).Error; err != nil {
			return fmt.Errorf("telegram: chat projects: %w", err)
		}
		ids := make([]uuid.UUID, len(rows))
		for i, id := range rows {
			ids[i] = uuid.MustParse(id)
		}
		if err := forgetMessages(tx, ids...); err != nil {
			return err
		}
		res := tx.Exec(`UPDATE projects SET telegram_chat_id = ?, updated_at = now() WHERE telegram_chat_id = ?`, to, from)
		if res.Error != nil {
			return fmt.Errorf("telegram: migrate chat: %w", res.Error)
		}
		n = res.RowsAffected
		return nil
	})
	return n, err
}

// Rename запоминает новое название группы у её проектов (служебное сообщение new_chat_title).
func (g *Groups) Rename(ctx context.Context, chatID int64, title string) error {
	err := g.db.WithContext(ctx).Exec(`UPDATE projects SET telegram_chat_title = ? WHERE telegram_chat_id = ?`,
		nullable(title), chatID).Error
	if err != nil {
		return fmt.Errorf("telegram: rename chat: %w", err)
	}
	return nil
}

// ManagersToNotify — активные менеджеры проектов с привязанным Telegram и их проекты из ps
// (по ключу): кому отправить личное уведомление об отключении группы.
func (g *Groups) ManagersToNotify(ctx context.Context, ps []project.Project) (map[int64][]project.Project, error) {
	if len(ps) == 0 {
		return nil, nil
	}
	byID := make(map[uuid.UUID]project.Project, len(ps))
	ids := make([]uuid.UUID, len(ps))
	for i, p := range ps {
		byID[p.ID], ids[i] = p, p.ID
	}
	var rows []struct {
		TelegramUserID int64
		ProjectID      uuid.UUID
	}
	err := g.db.WithContext(ctx).Raw(`SELECT u.telegram_user_id, m.project_id FROM project_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.project_id IN ? AND m.role = ? AND u.is_active AND u.telegram_user_id IS NOT NULL
		ORDER BY u.telegram_user_id`, ids, project.RoleManager).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("telegram: managers: %w", err)
	}
	out := map[int64][]project.Project{}
	for _, r := range rows {
		out[r.TelegramUserID] = append(out[r.TelegramUserID], byID[r.ProjectID])
	}
	for _, list := range out {
		slices.SortFunc(list, func(a, b project.Project) int { return cmp.Compare(a.Key, b.Key) })
	}
	return out, nil
}

// lockManaged — строка неудалённого проекта FOR UPDATE и проверка, что userID — его менеджер
// или администратор системы (§2: «Подключать Telegram-группу к проекту»). Не участник —
// project.ErrNotFound, участник без прав — ErrNotManager, архив — project.ErrArchived (§3.2).
func lockManaged(tx *gorm.DB, projectID, userID uuid.UUID) (*project.Project, error) {
	p, err := project.LockForUpdate(tx, projectID)
	if err != nil {
		return nil, err
	}
	manager, err := project.ManagerIn(tx, projectID, userID)
	if errors.Is(err, project.ErrNotFound) {
		return p, errNotMember
	}
	if err != nil {
		return p, err
	}
	if !manager {
		return p, ErrNotManager
	}
	return p, project.EnsureWritable(p)
}

// unbind снимает группу с заблокированного проекта p и пишет журнал. Номера сообщений задач
// забываются: в следующей группе это будут чужие сообщения.
func unbind(tx *gorm.DB, p *project.Project) error {
	if err := forgetMessages(tx, p.ID); err != nil {
		return err
	}
	if err := tx.Exec(`UPDATE projects SET telegram_chat_id = NULL, telegram_chat_title = NULL, updated_at = now() WHERE id = ?`,
		p.ID).Error; err != nil {
		return fmt.Errorf("telegram: unbind group: %w", err)
	}
	old := map[string]any{"telegram_chat_id": p.TelegramChatID, "telegram_chat_title": p.TelegramChatTitle}
	p.TelegramChatID, p.TelegramChatTitle = nil, nil
	return audit.Write(tx, audit.Record{ProjectID: &p.ID, Action: AuditGroupUnbound, EntityType: "project",
		EntityID: p.ID.String(), Old: old})
}

// forgetMessages очищает telegram_message_id задач проектов.
func forgetMessages(tx *gorm.DB, projectIDs ...uuid.UUID) error {
	if len(projectIDs) == 0 {
		return nil
	}
	err := tx.Exec(`UPDATE tasks SET telegram_message_id = NULL
		WHERE project_id IN ? AND telegram_message_id IS NOT NULL`, projectIDs).Error
	if err != nil {
		return fmt.Errorf("telegram: forget task messages: %w", err)
	}
	return nil
}
