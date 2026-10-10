package httpserver_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/httpserver"
	"github.com/jack5505/taskflow/api/internal/logx"
)

type createReq struct {
	Title    string `json:"title" binding:"required,min=1,max=200"`
	Priority string `json:"priority" binding:"omitempty,oneof=low medium high critical"`
	Estimate int    `json:"estimate_minutes" binding:"omitempty,gte=1,lte=60000"`
}

// testRouter — роутер сервера плюс маршруты, воспроизводящие типичные ситуации обработчиков.
func testRouter(t *testing.T) *gin.Engine {
	t.Helper()
	r := httpserver.New(&config.Config{Env: "test"}, nil, nil).Router()
	api := r.Group("/api/v1/_test")
	api.POST("/bind", func(c *gin.Context) {
		var req createReq
		if !httpserver.Bind(c, &req) {
			return
		}
		c.JSON(http.StatusOK, gin.H{"title": req.Title})
	})
	api.GET("/domain", func(c *gin.Context) {
		httpserver.Abort(c, apierr.Conflict(apierr.CodeInvalidTransition, "Переход done → todo не разрешён").
			With("from", "done").With("to", "todo"))
	})
	api.GET("/internal", func(c *gin.Context) {
		httpserver.Abort(c, errors.New("секретная причина: пароль базы"))
	})
	api.GET("/panic", func(*gin.Context) { panic("boom") })
	api.GET("/log", func(c *gin.Context) {
		logx.From(c.Request.Context()).Info("из обработчика")
		c.Status(http.StatusNoContent)
	})
	return r
}

func do(t *testing.T, r *gin.Engine, method, path, body string) (*httptest.ResponseRecorder, apierr.Response) {
	t.Helper()
	rec := httptest.NewRecorder()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	var resp apierr.Response
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp
}

func requireFormat(t *testing.T, rec *httptest.ResponseRecorder, resp apierr.Response, status int, code string) {
	t.Helper()
	require.Equal(t, status, rec.Code)
	require.Equal(t, code, resp.Error.Code)
	require.NotEmpty(t, resp.Error.Message)
	require.NotEmpty(t, rec.Header().Get("X-Request-Id"))
	require.Equal(t, rec.Header().Get("X-Request-Id"), resp.Error.Details["request_id"], "request_id из заголовка и из details совпадают")
}

func TestValidationErrorListsFields(t *testing.T) {
	r := testRouter(t)
	rec, resp := do(t, r, http.MethodPost, "/api/v1/_test/bind", `{"title":"","priority":"urgent","estimate_minutes":0}`)
	requireFormat(t, rec, resp, http.StatusBadRequest, apierr.CodeValidation)

	raw, _ := json.Marshal(resp.Error.Details["fields"])
	var fields []apierr.FieldError
	require.NoError(t, json.Unmarshal(raw, &fields))
	got := map[string]string{}
	for _, f := range fields {
		got[f.Field] = f.Reason
	}
	require.Equal(t, "обязательное поле", got["title"], "имя поля как в JSON, а не Title")
	require.Contains(t, got["priority"], "low, medium, high, critical")
	_, hasEstimate := got["estimate_minutes"]
	require.False(t, hasEstimate, "0 с omitempty не проверяется")
}

func TestInvalidJSONAndWrongType(t *testing.T) {
	r := testRouter(t)
	rec, resp := do(t, r, http.MethodPost, "/api/v1/_test/bind", `{"title": `)
	requireFormat(t, rec, resp, http.StatusBadRequest, apierr.CodeInvalidJSON)

	rec, resp = do(t, r, http.MethodPost, "/api/v1/_test/bind", "")
	requireFormat(t, rec, resp, http.StatusBadRequest, apierr.CodeInvalidJSON)

	rec, resp = do(t, r, http.MethodPost, "/api/v1/_test/bind", `{"title":"ok","estimate_minutes":"много"}`)
	requireFormat(t, rec, resp, http.StatusBadRequest, apierr.CodeValidation)
	require.Contains(t, rec.Body.String(), `"field":"estimate_minutes"`)

	rec, _ = do(t, r, http.MethodPost, "/api/v1/_test/bind", `{"title":"ok"}`)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestDomainErrorKeepsCodeAndDetails(t *testing.T) {
	r := testRouter(t)
	rec, resp := do(t, r, http.MethodGet, "/api/v1/_test/domain", "")
	requireFormat(t, rec, resp, http.StatusConflict, apierr.CodeInvalidTransition)
	require.Equal(t, "done", resp.Error.Details["from"])
	require.Equal(t, "todo", resp.Error.Details["to"])
}

func TestUnknownErrorIs500WithoutDetails(t *testing.T) {
	r := testRouter(t)
	rec, resp := do(t, r, http.MethodGet, "/api/v1/_test/internal", "")
	requireFormat(t, rec, resp, http.StatusInternalServerError, apierr.CodeInternal)
	require.NotContains(t, rec.Body.String(), "пароль базы", "причина 500 не уходит клиенту")
}

func TestPanicIs500InSameFormat(t *testing.T) {
	r := testRouter(t)
	rec, resp := do(t, r, http.MethodGet, "/api/v1/_test/panic", "")
	requireFormat(t, rec, resp, http.StatusInternalServerError, apierr.CodeInternal)
	require.NotContains(t, rec.Body.String(), "boom")
}

func TestNotFoundAndMethodNotAllowed(t *testing.T) {
	r := testRouter(t)
	rec, resp := do(t, r, http.MethodGet, "/api/v1/nope", "")
	requireFormat(t, rec, resp, http.StatusNotFound, apierr.CodeNotFound)

	rec, resp = do(t, r, http.MethodDelete, "/api/v1/_test/domain", "")
	requireFormat(t, rec, resp, http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed)
}

func TestRequestIDAppearsInEveryLogLine(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	r := testRouter(t)
	rec, _ := do(t, r, http.MethodGet, "/api/v1/_test/log", "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	id := rec.Header().Get("X-Request-Id")
	require.NotEmpty(t, id)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2, "строка обработчика и строка access-лога")
	for _, line := range lines {
		require.Contains(t, line, `"request_id":"`+id+`"`, line)
	}
	require.Contains(t, lines[0], "из обработчика")
	require.Contains(t, lines[1], `"msg":"http"`)
}

func TestClientRequestIDIsKept(t *testing.T) {
	r := testRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/_test/domain", nil)
	req.Header.Set("X-Request-Id", "client-abc-123")
	r.ServeHTTP(rec, req)
	require.Equal(t, "client-abc-123", rec.Header().Get("X-Request-Id"))
}
