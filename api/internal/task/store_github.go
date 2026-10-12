package task

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Связи задач с ветками и PR GitHub (§12.3, §12.8): таблица github_links модуля task.

// Виды связи и состояния PR.
const (
	LinkBranch = "branch"
	LinkPR     = "pr"

	PROpen   = "open"
	PRDraft  = "draft"
	PRMerged = "merged"
	PRClosed = "closed"
)

// Link — ветка или pull request, связанные с задачей.
type Link struct {
	ID          uuid.UUID `json:"id"`
	TaskID      uuid.UUID `json:"-"`
	Kind        string    `json:"kind" enums:"branch,pr"`
	Repo        string    `json:"repo" example:"acme/backend"`
	Number      *int      `json:"number,omitempty" example:"7"`
	Name        string    `json:"name" example:"TM-42: вход по паролю"`
	URL         string    `json:"url" example:"https://github.com/acme/backend/pull/7"`
	State       string    `json:"state" enums:"open,draft,merged,closed"`
	AuthorLogin *string   `json:"author_login,omitempty"`
	// SourceUpdatedAt — updated_at PR в GitHub: доставка старше сохранённой состояние не меняет.
	SourceUpdatedAt *time.Time `json:"-"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (Link) TableName() string { return "github_links" }

// lockLink — связь с тем же ключом, что l (ветка — по имени, PR — по номеру в репозитории),
// строка FOR UPDATE; нет — nil.
func lockLink(tx *gorm.DB, l Link) (*Link, error) {
	q := `SELECT * FROM github_links WHERE task_id = ? AND kind = 'pr' AND lower(repo) = lower(?) AND number = ? FOR UPDATE`
	args := []any{l.TaskID, l.Repo, l.Number}
	if l.Kind == LinkBranch {
		q = `SELECT * FROM github_links WHERE task_id = ? AND kind = 'branch' AND lower(repo) = lower(?) AND name = ? FOR UPDATE`
		args = []any{l.TaskID, l.Repo, l.Name}
	}
	var rows []Link
	if err := tx.Raw(q, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: github link: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// insertLink вставляет связь l; такая уже есть (вставили одновременно) — inserted = false.
func insertLink(tx *gorm.DB, l Link) (inserted bool, err error) {
	target := `(task_id, lower(repo), number) WHERE kind = 'pr'`
	if l.Kind == LinkBranch {
		target = `(task_id, lower(repo), name) WHERE kind = 'branch'`
	}
	res := tx.Exec(`INSERT INTO github_links (id, task_id, kind, repo, number, name, url, state, author_login, source_updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT `+target+` DO NOTHING`,
		uuid.New(), l.TaskID, l.Kind, l.Repo, l.Number, l.Name, l.URL, l.State, l.AuthorLogin, l.SourceUpdatedAt)
	if res.Error != nil {
		return false, fmt.Errorf("task: github link: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// updateLink переписывает название, адрес, состояние, автора и время источника связи id.
func updateLink(tx *gorm.DB, id uuid.UUID, l Link) error {
	if err := tx.Exec(`UPDATE github_links SET name = ?, url = ?, state = ?, author_login = ?, source_updated_at = ?,
		updated_at = now() WHERE id = ?`, l.Name, l.URL, l.State, l.AuthorLogin, l.SourceUpdatedAt, id).Error; err != nil {
		return fmt.Errorf("task: github link: %w", err)
	}
	return nil
}

// LinksOf — связи задачи: ветки, затем PR, по времени появления.
func LinksOf(db *gorm.DB, taskID uuid.UUID) ([]Link, error) {
	list := []Link{}
	if err := db.Raw(`SELECT * FROM github_links WHERE task_id = ? ORDER BY kind, created_at, id`, taskID).Scan(&list).Error; err != nil {
		return nil, fmt.Errorf("task: github links: %w", err)
	}
	return list, nil
}

// OpenPRsExcept — сколько у задачи открытых (и черновиков) PR, кроме PR number в repo.
func OpenPRsExcept(db *gorm.DB, taskID uuid.UUID, repo string, number int) (int64, error) {
	var n int64
	if err := db.Raw(`SELECT count(*) FROM github_links WHERE task_id = ? AND kind = 'pr' AND state IN ('open', 'draft')
		AND NOT (lower(repo) = lower(?) AND number = ?)`, taskID, repo, number).Scan(&n).Error; err != nil {
		return 0, fmt.Errorf("task: open prs: %w", err)
	}
	return n, nil
}

// LiveByKeys — неудалённые задачи проекта с ключами keys (ключи — в верхнем регистре).
func LiveByKeys(db *gorm.DB, projectID uuid.UUID, keys []string) ([]Titled, error) {
	var rows []Titled
	if len(keys) == 0 {
		return rows, nil
	}
	if err := db.Raw(`SELECT id, key, title FROM tasks WHERE project_id = ? AND key IN ? AND deleted_at IS NULL ORDER BY number`,
		projectID, keys).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("task: by keys: %w", err)
	}
	return rows, nil
}
