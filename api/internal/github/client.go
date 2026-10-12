package github

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// do выполняет запрос с авторизацией auth и разбирает JSON-ответ в out. 404 — ErrNotInstalled.
func (c *Client) do(ctx context.Context, method, path, auth string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, nil)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", auth)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotInstalled
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("github: %s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	return nil
}

// InstallationFor — установка App, через которую доступен репозиторий owner/name; не
// установлен — ErrNotInstalled.
func (c *Client) InstallationFor(ctx context.Context, fullName string) (int64, error) {
	jwtToken, err := c.appJWT()
	if err != nil {
		return 0, err
	}
	var inst struct {
		ID int64 `json:"id"`
	}
	owner, name, _ := strings.Cut(fullName, "/")
	if err := c.do(ctx, http.MethodGet, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name)+"/installation",
		"Bearer "+jwtToken, &inst); err != nil {
		return 0, err
	}
	return inst.ID, nil
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

// Repositories — все репозитории всех установок App, по имени.
func (c *Client) Repositories(ctx context.Context) ([]Repo, error) {
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
		tok, err := c.token(ctx, inst.ID)
		if err != nil {
			return nil, err
		}
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
				repos = append(repos, Repo{FullName: r.FullName, Private: r.Private, URL: r.HTMLURL, InstallationID: inst.ID})
			}
			if len(out.Repositories) < pageSize {
				break
			}
		}
	}
	return repos, nil
}
