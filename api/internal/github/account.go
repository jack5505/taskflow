package github

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/audit"
	"github.com/jack5505/taskflow/api/internal/user"
)

// stateTTL — сколько живёт ссылка входа в GitHub: время на вход и подтверждение на github.com.
const stateTTL = 10 * time.Minute

// CallbackPath — куда GitHub возвращает пользователя после входа (Callback URL App).
const CallbackPath = "/github/callback"

// ErrBadState — state не наш, подделан или устарел: ссылку входа нужно получить заново.
var ErrBadState = apierr.BadRequest(apierr.CodeValidation, "Ссылка входа в GitHub устарела или неверна — начните привязку заново")

// Действия журнала: связка GitHub-аккаунта (§12.5).
const (
	AuditAccountLinked   = "user.github_linked"
	AuditAccountUnlinked = "user.github_unlinked"
)

// errInactive — пользователь деактивирован, пока шла привязка.
var errInactive = apierr.BadRequest(apierr.CodeValidation, "Пользователь деактивирован — привязка GitHub невозможна")

// errGitHubTaken — GitHub-аккаунт уже связан с другим пользователем (§12.5).
var errGitHubTaken = apierr.BadRequest(apierr.CodeValidation, "Этот GitHub-аккаунт уже связан с другим пользователем")

// Accounts — связка аккаунта TaskFlow с GitHub через авторизацию пользователя App (§12.5).
type Accounts struct {
	db           *gorm.DB
	clientID     string
	clientSecret string
	stateKey     []byte
	webURL       string
	apiURL       string
	baseURL      string
	http         *http.Client
	now          func() time.Time
}

// NewAccounts — stateKey подписывает state (секрет JWT сервера); baseURL — адрес TaskFlow.
func NewAccounts(db *gorm.DB, clientID, clientSecret, stateKey, webURL, apiURL, baseURL string) *Accounts {
	return &Accounts{db: db, clientID: clientID, clientSecret: clientSecret, stateKey: []byte(stateKey),
		webURL: strings.TrimRight(webURL, "/"), apiURL: strings.TrimRight(apiURL, "/"), baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{Timeout: 15 * time.Second}, now: time.Now}
}

// ConnectURL — ссылка входа в GitHub для пользователя userID и nonce браузера: nonce кладётся
// в cookie этого браузера, а state связывает возврат и с пользователем, и с этим nonce.
func (a *Accounts) ConnectURL(userID uuid.UUID) (link, nonce string) {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	nonce = base64.RawURLEncoding.EncodeToString(b)
	q := url.Values{
		"client_id":    {a.clientID},
		"state":        {a.state(userID, nonce, a.now().Add(stateTTL))},
		"redirect_uri": {a.baseURL + "/api/v1" + CallbackPath},
	}
	return a.webURL + "/login/oauth/authorize?" + q.Encode(), nonce
}

// state = base64(userID|nonce|exp|HMAC): подпись не даёт подставить чужого пользователя, nonce
// из cookie — завершить чужую ссылку в своём браузере (CSRF), срок — использовать старую.
func (a *Accounts) state(userID uuid.UUID, nonce string, exp time.Time) string {
	body := userID.String() + "|" + nonce + "|" + strconv.FormatInt(exp.Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(body + "|" + a.sign(body)))
}

func (a *Accounts) sign(body string) string {
	mac := hmac.New(sha256.New, a.stateKey)
	mac.Write([]byte("github-state|" + body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// userFromState — пользователь из действующего state, выданного этому браузеру (nonce из его
// cookie), иначе ErrBadState.
func (a *Accounts) userFromState(state, nonce string) (uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(state)
	if err != nil {
		return uuid.Nil, ErrBadState
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 || !hmac.Equal([]byte(a.sign(strings.Join(parts[:3], "|"))), []byte(parts[3])) {
		return uuid.Nil, ErrBadState
	}
	if nonce == "" || !hmac.Equal([]byte(parts[1]), []byte(nonce)) {
		return uuid.Nil, ErrBadState
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || !a.now().Before(time.Unix(exp, 0)) {
		return uuid.Nil, ErrBadState
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, ErrBadState
	}
	return id, nil
}

// Complete — возврат с github.com: код обменивается на токен пользователя, по нему — логин,
// логин записывается пользователю из state. nonce — из cookie браузера. Токен не хранится.
func (a *Accounts) Complete(ctx context.Context, code, state, nonce string) (string, error) {
	userID, err := a.userFromState(state, nonce)
	if err != nil {
		return "", err
	}
	if code == "" {
		return "", ErrBadState
	}
	token, err := a.exchange(ctx, code)
	if err != nil {
		return "", err
	}
	login, err := a.login(ctx, token)
	if err != nil {
		return "", err
	}
	if err := a.set(ctx, userID, &login); err != nil {
		return "", err
	}
	return login, nil
}

// Unlink снимает связку GitHub у пользователя.
func (a *Accounts) Unlink(ctx context.Context, userID uuid.UUID) error {
	return a.set(ctx, userID, nil)
}

// set записывает логин (nil — снимает) с журналом: связка решает, от чьего имени работают PR (§12.4).
func (a *Accounts) set(ctx context.Context, userID uuid.UUID, login *string) error {
	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := user.SetGitHubLogin(tx, userID, login)
		switch {
		case errors.Is(err, user.ErrGitHubTaken):
			return errGitHubTaken
		case errors.Is(err, user.ErrInactive):
			return errInactive
		case err != nil:
			return err
		}
		action := AuditAccountLinked
		if login == nil {
			action = AuditAccountUnlinked
		}
		return audit.Write(tx, audit.Record{Action: action, EntityType: "user", EntityID: userID.String(),
			New: map[string]any{"github_login": login}})
	})
}

func (a *Accounts) exchange(ctx context.Context, code string) (string, error) {
	form := url.Values{"client_id": {a.clientID}, "client_secret": {a.clientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.webURL+"/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("github: oauth: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if err := a.call(req, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		// bad_verification_code: код истёк или уже использован — пусть пользователь начнёт заново.
		return "", ErrBadState
	}
	return out.AccessToken, nil
}

func (a *Accounts) login(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.apiURL+"/user", nil)
	if err != nil {
		return "", fmt.Errorf("github: user: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	var out struct {
		Login string `json:"login"`
	}
	if err := a.call(req, &out); err != nil {
		return "", err
	}
	if out.Login == "" {
		return "", errors.New("github: user: пустой логин")
	}
	return out.Login, nil
}

func (a *Accounts) call(req *http.Request, out any) error {
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s: %w", req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("github: %s: %s: %s", req.URL.Path, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("github: %s: %w", req.URL.Path, err)
	}
	return nil
}
