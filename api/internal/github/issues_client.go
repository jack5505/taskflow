package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Issue — issue GitHub: номер и адрес.
type Issue struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
}

// IssueInput — каким должен быть issue (§12.9). StateReason — completed или not_planned для
// закрытого, пусто — для открытого.
type IssueInput struct {
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	State       string   `json:"state,omitempty"`
	StateReason string   `json:"state_reason,omitempty"`
	Assignees   []string `json:"assignees"`
}

// recentIssues — сколько последних issues просматривает FindIssue.
const recentIssues = 100

func repoPath(fullName string) string {
	owner, name, _ := strings.Cut(fullName, "/")
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

// CreateIssue создаёт issue в репозитории repo от имени установки installationID. Исполнитель,
// которого GitHub не принимает (нет доступа к репозиторию), — issue создаётся без исполнителя.
func (c *Client) CreateIssue(ctx context.Context, installationID int64, repo string, in IssueInput) (*Issue, error) {
	tok, err := c.token(ctx, installationID)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"title": in.Title, "body": in.Body, "assignees": in.Assignees}
	var out Issue
	err = c.send(ctx, http.MethodPost, repoPath(repo)+"/issues", "token "+tok, body, &out)
	if badAssignee(err, in) {
		body["assignees"] = []string{}
		err = c.send(ctx, http.MethodPost, repoPath(repo)+"/issues", "token "+tok, body, &out)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateIssue переписывает issue number целиком по in; исполнитель, которого GitHub не
// принимает, снимается.
func (c *Client) UpdateIssue(ctx context.Context, installationID int64, repo string, number int, in IssueInput) error {
	tok, err := c.token(ctx, installationID)
	if err != nil {
		return err
	}
	path := repoPath(repo) + "/issues/" + strconv.Itoa(number)
	err = c.send(ctx, http.MethodPatch, path, "token "+tok, in, nil)
	if badAssignee(err, in) {
		in.Assignees = []string{}
		err = c.send(ctx, http.MethodPatch, path, "token "+tok, in, nil)
	}
	return err
}

// FindIssue — среди последних issues репозитория тот, в описании которого есть marker; нет — nil.
// Нужен, если прошлая попытка создала issue, но не успела записать связь.
func (c *Client) FindIssue(ctx context.Context, installationID int64, repo, marker string) (*Issue, error) {
	tok, err := c.token(ctx, installationID)
	if err != nil {
		return nil, err
	}
	var list []Issue
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("%s/issues?state=all&sort=created&direction=desc&per_page=%d",
		repoPath(repo), recentIssues), "token "+tok, &list); err != nil {
		return nil, err
	}
	for i := range list {
		if strings.Contains(list[i].Body, marker) {
			return &list[i], nil
		}
	}
	return nil, nil
}

// badAssignee — 422 на запрос с исполнителем: повторяем без него.
func badAssignee(err error, in IssueInput) bool {
	var e *APIError
	return len(in.Assignees) > 0 && errors.As(err, &e) && e.Status == http.StatusUnprocessableEntity
}
