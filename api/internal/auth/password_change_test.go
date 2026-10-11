package auth_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/auth"
)

// Критерий #138: смена с верным текущим работает, остальные сессии закрыты, текущая остаётся.
func TestChangePasswordKeepsCurrentSessionOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	current, err := f.svc.Login(ctx, f.u.Email, password, "этот браузер")
	require.NoError(t, err)
	other, err := f.svc.Login(ctx, f.u.Email, password, "телефон")
	require.NoError(t, err)
	link := f.resetLink(t)

	require.NoError(t, f.svc.ChangePassword(ctx, f.u, password, newPassword, current.RefreshToken))

	_, err = f.svc.Refresh(ctx, current.RefreshToken, "")
	require.NoError(t, err, "текущая сессия жива")
	_, err = f.svc.Refresh(ctx, other.RefreshToken, "")
	require.ErrorIs(t, err, auth.ErrBadRefresh, "остальные отозваны")
	_, err = f.svc.Login(ctx, f.u.Email, password, "")
	require.ErrorIs(t, err, auth.ErrBadCredentials, "старый пароль больше не подходит")
	_, err = f.svc.Login(ctx, f.u.Email, newPassword, "")
	require.NoError(t, err)
	require.ErrorIs(t, f.svc.SetPassword(ctx, link, "another secret 1"), auth.ErrBadPasswordToken,
		"ссылка, выданная до смены, погашена")
}

// Без refresh-токена текущего клиента (например, вызов не из браузера) отзываются все сессии.
func TestChangePasswordWithoutCookieRevokesAll(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s, err := f.svc.Login(ctx, f.u.Email, password, "")
	require.NoError(t, err)
	require.NoError(t, f.svc.ChangePassword(ctx, f.u, password, newPassword, ""))
	_, err = f.svc.Refresh(ctx, s.RefreshToken, "")
	require.ErrorIs(t, err, auth.ErrBadRefresh)
}

// Новый пароль — по правилам установки; ошибка в нём не тратит попытку ввода текущего.
func TestChangePasswordValidatesNewPassword(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, bad := range []string{"short", strings.Repeat("пароль", 7)} {
		for i := 0; i < 10; i++ {
			err := f.svc.ChangePassword(ctx, f.u, "wrong", bad, "")
			require.Equal(t, "new_password", fieldOf(t, err))
		}
	}
	require.NoError(t, f.svc.ChangePassword(ctx, f.u, password, newPassword, ""), "счётчик не тронут")
}

// Неверный текущий — 400 у поля current_password; после 10 неудач смена закрыта даже с верным,
// а вход по email не заблокирован: счётчики раздельные.
func TestChangePasswordWrongCurrentAndLockout(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		err := f.svc.ChangePassword(ctx, f.u, "wrong", newPassword, "")
		require.ErrorIs(t, err, auth.ErrBadCurrentPassword, "попытка %d", i+1)
		require.Equal(t, "current_password", fieldOf(t, err))
	}
	err := f.svc.ChangePassword(ctx, f.u, password, newPassword, "")
	require.ErrorIs(t, err, auth.ErrPasswordChangeLocked)
	e := apierr.From(err)
	require.Equal(t, http.StatusTooManyRequests, e.Status)
	retry, _ := e.Details["retry_after_seconds"].(int)
	require.Greater(t, retry, 0)
	require.LessOrEqual(t, retry, 15*60)

	_, err = f.svc.Login(ctx, f.u.Email, password, "")
	require.NoError(t, err, "вход не заблокирован, пароль прежний")

	// Установка пароля по ссылке снимает блокировку смены.
	require.NoError(t, f.svc.SetPassword(ctx, f.resetLink(t), newPassword))
	require.NoError(t, f.svc.ChangePassword(ctx, f.u, newPassword, password, ""))
}

// Успешная смена сбрасывает счётчик: 9 неудач, успех, снова 9 неудач — блокировки нет.
func TestChangePasswordSuccessResetsCounter(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := 0; i < 9; i++ {
		require.Error(t, f.svc.ChangePassword(ctx, f.u, "wrong", newPassword, ""))
	}
	require.NoError(t, f.svc.ChangePassword(ctx, f.u, password, newPassword, ""))
	for i := 0; i < 9; i++ {
		require.Error(t, f.svc.ChangePassword(ctx, f.u, "wrong", password, ""))
	}
	require.NoError(t, f.svc.ChangePassword(ctx, f.u, newPassword, password, ""))
}

// Две одновременные смены с одним и тем же текущим паролем: проходит ровно одна,
// вторая получает «неверный текущий» — её «текущий» уже не текущий.
func TestChangePasswordConcurrent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	targets := []string{"first new secret", "second new secret"}
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i, p := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = f.svc.ChangePassword(ctx, f.u, password, p, "")
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			require.ErrorIs(t, err, auth.ErrBadCurrentPassword)
		}
	}
	require.Equal(t, 1, ok)
}

func TestPasswordChangeEndpoint(t *testing.T) {
	f := newHTTPFixture(t)
	login := func() (string, string) {
		rec, body := f.do(t, req{method: "POST", path: "/api/v1/auth/login", body: `{"email":"` + f.u.Email + `","password":"` + password + `"}`})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return body["access_token"].(string), refreshCookie(t, rec).Value
	}
	access, cookie := login()
	_, otherCookie := login()
	path := "/api/v1/auth/password-change"

	rec, _ := f.do(t, req{method: "POST", path: path, body: `{"current_password":"` + password + `","new_password":"` + newPassword + `"}`})
	require.Equal(t, http.StatusUnauthorized, rec.Code, "без Bearer")

	rec, body := f.do(t, req{method: "POST", path: path, bearer: access, body: `{"new_password":"` + newPassword + `"}`})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, apierr.CodeValidation, errCode(body))
	require.Contains(t, rec.Body.String(), `"field":"current_password"`)

	rec, body = f.do(t, req{method: "POST", path: path, bearer: access, body: `{"current_password":"wrong","new_password":"` + newPassword + `"}`})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, apierr.CodeValidation, errCode(body))
	require.Contains(t, rec.Body.String(), `"field":"current_password"`)

	rec, _ = f.do(t, req{method: "POST", path: path, bearer: access, body: `{"current_password":"` + password + `","new_password":"short"}`})
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), `"field":"new_password"`)

	rec, _ = f.do(t, req{method: "POST", path: path, bearer: access, cookie: cookie, body: `{"current_password":"` + password + `","new_password":"` + newPassword + `"}`})
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	rec, _ = f.do(t, req{method: "POST", path: "/api/v1/auth/refresh", cookie: cookie, csrf: true})
	require.Equal(t, http.StatusOK, rec.Code, "сессия из cookie запроса осталась")
	rec, _ = f.do(t, req{method: "POST", path: "/api/v1/auth/refresh", cookie: otherCookie, csrf: true})
	require.Equal(t, http.StatusUnauthorized, rec.Code, "другая сессия отозвана")

	for i := 0; i < 10; i++ {
		f.do(t, req{method: "POST", path: path, bearer: access, body: `{"current_password":"wrong","new_password":"` + password + `"}`})
	}
	rec, body = f.do(t, req{method: "POST", path: path, bearer: access, body: `{"current_password":"` + newPassword + `","new_password":"` + password + `"}`})
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, apierr.CodeRateLimited, errCode(body))
}
