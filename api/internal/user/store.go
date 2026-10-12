package user

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Name — имя пользователя для текстов бота и писем.
type Name struct {
	ID               uuid.UUID
	DisplayName      string
	TelegramUsername *string
}

// Names — имена пользователей ids (и деактивированных) по id; кого нет — того нет в ответе.
func Names(db *gorm.DB, ids ...uuid.UUID) (map[uuid.UUID]Name, error) {
	out := make(map[uuid.UUID]Name, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []Name
	if err := db.Raw(`SELECT id, display_name, telegram_username FROM users WHERE id IN ?`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: names: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// DisplayName — имя пользователя id (и деактивированного); нет такого — ok = false.
func DisplayName(db *gorm.DB, id uuid.UUID) (name string, ok bool, err error) {
	var names []string
	if err := db.Raw(`SELECT display_name FROM users WHERE id = ?`, id).Scan(&names).Error; err != nil {
		return "", false, fmt.Errorf("user: display name: %w", err)
	}
	if len(names) == 0 {
		return "", false, nil
	}
	return names[0], true, nil
}

// ActiveTelegramIDs — Telegram-аккаунты активных пользователей из ids, у кого он привязан, по id.
func ActiveTelegramIDs(db *gorm.DB, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	rows, err := TelegramRecipients(db, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]int64, len(rows))
	for _, r := range rows {
		out[r.ID] = r.ChatID
	}
	return out, nil
}

// LockRow блокирует строку пользователя id FOR UPDATE (нет такого — ничего не блокирует).
func LockRow(tx *gorm.DB, id uuid.UUID) error {
	if err := tx.Exec(`SELECT 1 FROM users WHERE id = ? FOR UPDATE`, id).Error; err != nil {
		return fmt.Errorf("user: lock: %w", err)
	}
	return nil
}

// LockActive — активный пользователь id, строка FOR UPDATE; нет или деактивирован — nil.
func LockActive(tx *gorm.DB, id uuid.UUID) (*User, error) {
	return lockUser(tx, id, "AND is_active FOR UPDATE")
}

// TelegramTaken — Telegram-аккаунт tgID привязан к другому пользователю, не exceptID.
func TelegramTaken(tx *gorm.DB, tgID int64, exceptID uuid.UUID) (bool, error) {
	var n int64
	if err := tx.Raw(`SELECT count(*) FROM users WHERE telegram_user_id = ? AND id <> ?`, tgID, exceptID).Scan(&n).Error; err != nil {
		return false, fmt.Errorf("user: telegram taken: %w", err)
	}
	return n > 0, nil
}

// SetTelegram привязывает Telegram tgID с именем username к заблокированному пользователю u
// (поля u обновляются). Тот же tgID уже у другого — ошибка gorm.ErrDuplicatedKey без обёртки.
func SetTelegram(tx *gorm.DB, u *User, tgID int64, username *string) error {
	return tx.Model(u).Updates(map[string]any{"telegram_user_id": tgID, "telegram_username": username}).Error
}

// ClearTelegram снимает привязку Telegram у пользователя id; без привязки ничего не меняет.
func ClearTelegram(db *gorm.DB, id uuid.UUID) error {
	if err := db.Exec(`UPDATE users SET telegram_user_id = NULL, telegram_username = NULL, updated_at = now()
		WHERE id = ? AND telegram_user_id IS NOT NULL`, id).Error; err != nil {
		return fmt.Errorf("user: unlink telegram: %w", err)
	}
	return nil
}

// ActiveByTelegram — активный пользователь, привязанный к Telegram tgID; не привязан — nil.
func ActiveByTelegram(db *gorm.DB, tgID int64) (*User, error) {
	var u User
	res := db.Raw(`SELECT * FROM users WHERE telegram_user_id = ? AND is_active`, tgID).Scan(&u)
	if res.Error != nil {
		return nil, fmt.Errorf("user: by telegram id: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &u, nil
}

// SetTelegramUsername запоминает новое имя Telegram пользователя u (поле u обновляется);
// служебное поле — updated_at профиля не меняется.
func SetTelegramUsername(db *gorm.DB, u *User, username *string) error {
	if err := db.Model(u).UpdateColumn("telegram_username", username).Error; err != nil {
		return fmt.Errorf("user: update telegram username: %w", err)
	}
	return nil
}

// AdminTelegramIDs — Telegram активных администраторов системы с привязкой, по id пользователя.
func AdminTelegramIDs(db *gorm.DB) ([]int64, error) {
	var ids []int64
	if err := db.Raw(`SELECT telegram_user_id FROM users
		WHERE is_system_admin AND is_active AND telegram_user_id IS NOT NULL ORDER BY id`).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("user: admin telegram ids: %w", err)
	}
	return ids, nil
}

// Recipient — активный пользователь с привязанным Telegram: личный чат и настройки уведомлений §8.7.
type Recipient struct {
	ID       uuid.UUID
	ChatID   int64 // личный чат = telegram_user_id
	Settings events.JSON
}

// TelegramRecipients — активные пользователи из ids с привязанным Telegram (порядок не задан).
func TelegramRecipients(db *gorm.DB, ids []uuid.UUID) ([]Recipient, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []Recipient
	if err := db.Raw(`SELECT id, telegram_user_id AS chat_id, notification_settings AS settings FROM users
		WHERE id IN ? AND is_active AND telegram_user_id IS NOT NULL`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: telegram recipients: %w", err)
	}
	return rows, nil
}

// ActiveTimezones — часовые пояса активных пользователей из ids по id; деактивированных нет.
// Блокировок не берёт.
func ActiveTimezones(db *gorm.DB, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID       uuid.UUID
		Timezone string
	}
	if err := db.Raw(`SELECT id, timezone FROM users WHERE id IN ? AND is_active`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: timezones: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r.Timezone
	}
	return out, nil
}

// NamesOrdered — имена пользователей ids по display_name, затем по id (сортировка базы, как в
// ORDER BY отчётов); кого нет — того нет в ответе.
func NamesOrdered(db *gorm.DB, ids []uuid.UUID) ([]Name, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []Name
	if err := db.Raw(`SELECT id, display_name, telegram_username FROM users WHERE id IN ? ORDER BY display_name, id`, ids).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: names: %w", err)
	}
	return rows, nil
}

// LockForUpdate — пользователь id, строка FOR UPDATE; нет такого — nil.
func LockForUpdate(tx *gorm.DB, id uuid.UUID) (*User, error) {
	return lockUser(tx, id, "FOR UPDATE")
}

// LockNoKeyUpdate — пользователь id, строка FOR NO KEY UPDATE: проверки внешних ключей (история,
// комментарии со ссылкой на пользователя) берут FOR KEY SHARE и с ней не конфликтуют. Нет — nil.
func LockNoKeyUpdate(tx *gorm.DB, id uuid.UUID) (*User, error) {
	return lockUser(tx, id, "FOR NO KEY UPDATE")
}

// lockUser — строка пользователя id с условием и блокировкой tail; нет — nil.
func lockUser(tx *gorm.DB, id uuid.UUID, tail string) (*User, error) {
	var u User
	res := tx.Raw(`SELECT * FROM users WHERE id = ? `+tail, id).Scan(&u)
	if res.Error != nil {
		return nil, fmt.Errorf("user: lock: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &u, nil
}

// LockActiveAdmins блокирует FOR NO KEY UPDATE всех активных администраторов в порядке id и
// возвращает их id: перед снятием прав или деактивацией, чтобы не остаться без администратора.
func LockActiveAdmins(tx *gorm.DB) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	if err := tx.Raw(`SELECT id FROM users WHERE is_system_admin AND is_active ORDER BY id FOR NO KEY UPDATE`).
		Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("user: lock admins: %w", err)
	}
	return ids, nil
}

// Update записывает поля updates пользователю u (поля u обновляются). Строку вызывающий уже
// заблокировал (LockForUpdate, LockNoKeyUpdate); UPDATE других строк не трогает.
func Update(tx *gorm.DB, u *User, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	if err := tx.Model(u).Updates(updates).Error; err != nil {
		return fmt.Errorf("user: update: %w", err)
	}
	return nil
}

// Reload перечитывает строку пользователя u. Блокировок не берёт.
func Reload(tx *gorm.DB, u *User) error {
	if err := tx.First(u, "id = ?", u.ID).Error; err != nil {
		return fmt.Errorf("user: reload: %w", err)
	}
	return nil
}

// CreateNew создаёт пользователя u; email уже занят — created = false, ничего не пишется.
// Блокирует только новую строку (и запись уникального индекса email до конца транзакции).
func CreateNew(tx *gorm.DB, u *User) (created bool, err error) {
	res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "email"}}, DoNothing: true}).Create(u)
	if res.Error != nil {
		return false, fmt.Errorf("user: create: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// All — все пользователи по имени и email.
func All(db *gorm.DB) ([]User, error) {
	var list []User
	if err := db.Order("display_name, email").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("user: list: %w", err)
	}
	return list, nil
}

// ActiveBriefs — активные пользователи без служебных полей по имени и email.
func ActiveBriefs(db *gorm.DB) ([]Brief, error) {
	list := []Brief{}
	if err := db.Model(&User{}).Where("is_active").Order("display_name, email").Find(&list).Error; err != nil {
		return nil, fmt.Errorf("user: list: %w", err)
	}
	return list, nil
}

// ActiveAmong — кто из ids активен. Блокировок не берёт.
func ActiveAmong(db *gorm.DB, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []uuid.UUID
	if err := db.Raw(`SELECT id FROM users WHERE id IN ? AND is_active`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: active: %w", err)
	}
	for _, id := range rows {
		out[id] = true
	}
	return out, nil
}

// LockShare блокирует FOR SHARE строки пользователей ids в порядке id: модуль task берёт их раньше
// строк задач и проекта — тот же порядок, что у деактивации (пользователь → задачи → проект).
func LockShare(tx *gorm.DB, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	var got []uuid.UUID
	if err := tx.Raw(`SELECT id FROM users WHERE id IN ? ORDER BY id FOR SHARE`, ids).Scan(&got).Error; err != nil {
		return fmt.Errorf("user: lock share: %w", err)
	}
	return nil
}

// Actor — права действующего пользователя из строки под блокировкой.
type Actor struct {
	IsSystemAdmin bool
	IsActive      bool
}

// LockActor — флаги пользователя id, строка FOR SHARE (первая блокировка операций над задачами);
// нет такого — nil.
func LockActor(tx *gorm.DB, id uuid.UUID) (*Actor, error) {
	var rows []Actor
	if err := tx.Raw(`SELECT is_system_admin, is_active FROM users WHERE id = ? FOR SHARE`, id).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: actor: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// IsActive — пользователь id есть и активен. Блокировок не берёт.
func IsActive(db *gorm.DB, id uuid.UUID) (bool, error) {
	var active []bool
	if err := db.Raw(`SELECT is_active FROM users WHERE id = ?`, id).Scan(&active).Error; err != nil {
		return false, fmt.Errorf("user: active: %w", err)
	}
	return len(active) > 0 && active[0], nil
}

// Timezone — часовой пояс пользователя id (§3.1); нет такого — "".
func Timezone(db *gorm.DB, id uuid.UUID) (string, error) {
	var tz string
	if err := db.Raw(`SELECT timezone FROM users WHERE id = ?`, id).Scan(&tz).Error; err != nil {
		return "", fmt.Errorf("user: timezone: %w", err)
	}
	return tz, nil
}

// ActiveAdmin — флаг администратора активного пользователя id; нет или деактивирован — found = false.
// Блокировок не берёт.
func ActiveAdmin(db *gorm.DB, id uuid.UUID) (admin, found bool, err error) {
	var flags []bool
	if err := db.Raw(`SELECT is_system_admin FROM users WHERE id = ? AND is_active`, id).Scan(&flags).Error; err != nil {
		return false, false, fmt.Errorf("user: actor: %w", err)
	}
	if len(flags) == 0 {
		return false, false, nil
	}
	return flags[0], true, nil
}

// Contact — пользователь в списке участников: email, имя, активность.
type Contact struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
	IsActive    bool
}

// ContactsOrdered — пользователи ids по имени, затем email (сортировка базы). Блокировок не берёт.
func ContactsOrdered(db *gorm.DB, ids []uuid.UUID) ([]Contact, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []Contact
	if err := db.Raw(`SELECT id, email, display_name, is_active FROM users WHERE id IN ? ORDER BY display_name, email`, ids).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("user: contacts: %w", err)
	}
	return rows, nil
}

// LockNoKeyUpdateByEmail — пользователь с email, строка FOR NO KEY UPDATE; нет — nil.
func LockNoKeyUpdateByEmail(tx *gorm.DB, email string) (*User, error) {
	var u User
	res := tx.Raw(`SELECT * FROM users WHERE email = ? FOR NO KEY UPDATE`, email).Scan(&u)
	if res.Error != nil {
		return nil, fmt.Errorf("user: lock: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	return &u, nil
}

// ByID — пользователь id (и деактивированный). Нет — gorm.ErrRecordNotFound без обёртки: вызывающие
// отличают «нет пользователя» от сбоя. Блокировок не берёт.
func ByID(db *gorm.DB, id uuid.UUID) (*User, error) {
	var u User
	if err := db.First(&u, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// ByEmail — пользователь с email (citext, без учёта регистра). Нет — gorm.ErrRecordNotFound без
// обёртки. Блокировок не берёт.
func ByEmail(db *gorm.DB, email string) (*User, error) {
	var u User
	if err := db.First(&u, "email = ?", email).Error; err != nil {
		return nil, err
	}
	return &u, nil
}

// SetPasswordIf меняет хеш пароля пользователя id, только если текущий хеш — old (смену не
// перебила параллельная). changed = false — хеш уже другой.
func SetPasswordIf(tx *gorm.DB, id uuid.UUID, old, hash string) (changed bool, err error) {
	res := tx.Model(&User{}).Where("id = ? AND password_hash = ?", id, old).Update("password_hash", hash)
	if res.Error != nil {
		return false, fmt.Errorf("user: save password: %w", res.Error)
	}
	return res.RowsAffected > 0, nil
}

// ErrGitHubTaken — GitHub-логин уже связан с другим пользователем (§12.5).
var ErrGitHubTaken = errors.New("user: github login is linked to another user")

// ErrInactive — пользователя нет или он деактивирован.
var ErrInactive = errors.New("user: not found or deactivated")

// SetGitHubLogin связывает (login) или отвязывает (nil) GitHub-аккаунт активного пользователя id.
// Логин у другого пользователя — ErrGitHubTaken; пользователя нет или он деактивирован — ErrInactive.
func SetGitHubLogin(db *gorm.DB, id uuid.UUID, login *string) error {
	res := db.Exec(`UPDATE users SET github_login = ?, updated_at = now() WHERE id = ? AND is_active`, login, id)
	if errors.Is(res.Error, gorm.ErrDuplicatedKey) {
		return ErrGitHubTaken
	}
	if res.Error != nil {
		return fmt.Errorf("user: github login: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrInactive
	}
	return nil
}

// ActiveIDByGitHubLogin — активный пользователь со связанным GitHub-логином (регистр не важен);
// нет — nil. Блокировок не берёт.
func ActiveIDByGitHubLogin(db *gorm.DB, login string) (*uuid.UUID, error) {
	var ids []uuid.UUID
	if err := db.Raw(`SELECT id FROM users WHERE github_login = ? AND is_active`, login).Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("user: by github login: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return &ids[0], nil
}
