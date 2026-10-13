package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrNotInstalled — GitHub App не установлен в репозиторий (или репозитория нет).
var ErrNotInstalled = errors.New("github: app is not installed in the repository")

// errNotFound — ответ 404 GitHub; что он значит, решает вызывающий.
var errNotFound = errors.New("github: not found")

// reposTTL — сколько держим собранный список репозиториев: вкладку открывают часто, а список
// собирается многими запросами к GitHub.
const reposTTL = time.Minute

// Repo — репозиторий, доступный GitHub App.
type Repo struct {
	FullName       string `json:"full_name" example:"acme/backend"`
	Private        bool   `json:"private"`
	URL            string `json:"url" example:"https://github.com/acme/backend"`
	InstallationID int64  `json:"-"`
}

// Client — REST API GitHub от имени App: JWT App для /app и токены установок для репозиториев.
type Client struct {
	appID string
	key   *rsa.PrivateKey
	api   string
	http  *http.Client
	now   func() time.Time

	mu     sync.Mutex
	tokens map[int64]installationToken

	reposMu sync.Mutex // и кэш списка, и одна сборка за раз
	repos   []Repo
	reposAt time.Time
}

type installationToken struct {
	token   string
	expires time.Time
}

// NewClient читает приватный ключ App из PEM-файла.
func NewClient(appID, keyFile, apiURL string) (*Client, error) {
	pem, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("github: app key: %w", err)
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM(pem)
	if err != nil {
		return nil, fmt.Errorf("github: app key: %w", err)
	}
	return &Client{appID: appID, key: key, api: strings.TrimRight(apiURL, "/"),
		http: &http.Client{Timeout: 15 * time.Second}, now: time.Now, tokens: map[int64]installationToken{}}, nil
}

// appJWT — токен App на 9 минут (GitHub принимает не больше 10), выдан минутой раньше на случай
// расхождения часов.
func (c *Client) appJWT() (string, error) {
	now := c.now()
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    c.appID,
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	})
	s, err := t.SignedString(c.key)
	if err != nil {
		return "", fmt.Errorf("github: app jwt: %w", err)
	}
	return s, nil
}

// APIError — ответ GitHub с ошибкой. Temporary — стоит повторить позже: 5xx, 429 и исчерпанный
// лимит запросов (403 с x-ratelimit-remaining: 0).
type APIError struct {
	Status    int
	Temporary bool
	msg       string
}

func (e *APIError) Error() string { return e.msg }

// do выполняет запрос с авторизацией auth и разбирает JSON-ответ в out. 404 — errNotFound.
func (c *Client) do(ctx context.Context, method, path, auth string, out any) error {
	return c.send(ctx, method, path, auth, nil, out)
}

// send — как do, с телом запроса in в JSON (nil — без тела); out nil — ответ не разбирается.
func (c *Client) send(ctx context.Context, method, path, auth string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("github: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, body)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", auth)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode >= 300 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &APIError{Status: resp.StatusCode,
			Temporary: resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests ||
				(resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0"),
			msg: fmt.Sprintf("github: %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(text)))}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	return nil
}

// Repository — репозиторий owner/name (имя — как в GitHub, с его регистром) и установка App,
// через которую он доступен; не установлен или нет такого — ErrNotInstalled.
func (c *Client) Repository(ctx context.Context, fullName string) (*Repo, error) {
	jwtToken, err := c.appJWT()
	if err != nil {
		return nil, err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	owner, name, _ := strings.Cut(fullName, "/")
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	if err := c.do(ctx, http.MethodGet, path+"/installation", "Bearer "+jwtToken, &inst); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, ErrNotInstalled
		}
		return nil, err
	}
	tok, err := c.token(ctx, inst.ID)
	if err != nil {
		return nil, err
	}
	var r struct {
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
		HTMLURL  string `json:"html_url"`
	}
	if err := c.do(ctx, http.MethodGet, path, "token "+tok, &r); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, ErrNotInstalled
		}
		return nil, err
	}
	return &Repo{FullName: r.FullName, Private: r.Private, URL: r.HTMLURL, InstallationID: inst.ID}, nil
}

// token — токен установки, из кэша до 5 минут до истечения (GitHub выдаёт его на час).
func (c *Client) token(ctx context.Context, installationID int64) (string, error) {
	c.mu.Lock()
	t, ok := c.tokens[installationID]
	c.mu.Unlock()
	if ok && c.now().Before(t.expires.Add(-5*time.Minute)) {
		return t.token, nil
	}
	jwtToken, err := c.appJWT()
	if err != nil {
		return "", err
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := c.do(ctx, http.MethodPost, "/app/installations/"+strconv.FormatInt(installationID, 10)+"/access_tokens",
		"Bearer "+jwtToken, &out); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.tokens[installationID] = installationToken{token: out.Token, expires: out.ExpiresAt}
	c.mu.Unlock()
	return out.Token, nil
}

// pageSize — сколько элементов GitHub отдаёт на странице (максимум 100).
const pageSize = 100

// Repositories — репозитории всех установок App (кэш на минуту). Установка, которая не
// отвечает (приостановлена, только что удалена), пропускается — остальные доступны.
func (c *Client) Repositories(ctx context.Context) ([]Repo, error) {
	c.reposMu.Lock()
	defer c.reposMu.Unlock()
	if c.repos != nil && c.now().Before(c.reposAt.Add(reposTTL)) {
		return c.repos, nil
	}
	repos, err := c.collect(ctx)
	if err != nil {
		return nil, err
	}
	c.repos, c.reposAt = repos, c.now()
	return repos, nil
}

func (c *Client) collect(ctx context.Context) ([]Repo, error) {
	jwtToken, err := c.appJWT()
	if err != nil {
		return nil, err
	}
	var installs []struct {
		ID int64 `json:"id"`
	}
	for page := 1; ; page++ {
		var batch []struct {
			ID int64 `json:"id"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/app/installations?per_page=%d&page=%d", pageSize, page),
			"Bearer "+jwtToken, &batch); err != nil {
			return nil, err
		}
		installs = append(installs, batch...)
		if len(batch) < pageSize {
			break
		}
	}
	repos := []Repo{}
	for _, inst := range installs {
		got, err := c.installationRepos(ctx, inst.ID)
		if err != nil {
			slog.WarnContext(ctx, "github: installation repositories", "installation", inst.ID, "err", err)
			continue
		}
		repos = append(repos, got...)
	}
	return repos, nil
}

// installationRepos — все репозитории одной установки.
func (c *Client) installationRepos(ctx context.Context, instID int64) ([]Repo, error) {
	tok, err := c.token(ctx, instID)
	if err != nil {
		return nil, err
	}
	var repos []Repo
	for page := 1; ; page++ {
		var out struct {
			Repositories []struct {
				FullName string `json:"full_name"`
				Private  bool   `json:"private"`
				HTMLURL  string `json:"html_url"`
			} `json:"repositories"`
		}
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/installation/repositories?per_page=%d&page=%d", pageSize, page),
			"token "+tok, &out); err != nil {
			return nil, err
		}
		for _, r := range out.Repositories {
			repos = append(repos, Repo{FullName: r.FullName, Private: r.Private, URL: r.HTMLURL, InstallationID: instID})
		}
		if len(out.Repositories) < pageSize {
			break
		}
	}
	return repos, nil
}
