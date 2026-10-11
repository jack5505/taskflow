package app_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/config"
	"github.com/jack5505/taskflow/api/internal/telegram"
)

// Telegram-группа в настройках проекта (§7.9, §8.2, §9.2): код подключения и отключение —
// менеджер; исполнителю 403, постороннему 404; без бота кода нет, отключить можно.
func TestTelegramGroupRoutes(t *testing.T) {
	w := newWorld(t, func(c *config.Config) { c.TelegramBotToken, c.TelegramBotUsername = "1:T", "taskflow_bot" })
	proj := "/api/v1/projects/" + w.key

	rec := w.do("POST", proj+"/telegram-bind-code", w.manager, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var code telegram.BindCode
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &code))
	require.Len(t, code.Code, 8)
	require.Equal(t, "/bind "+code.Code, code.Command)
	require.Equal(t, "taskflow_bot", code.BotUsername)
	require.WithinDuration(t, time.Now().Add(15*time.Minute), code.ExpiresAt, 10*time.Second)
	require.Equal(t, http.StatusCreated, w.do("POST", proj+"/telegram-bind-code", w.admin, "").Code)
	require.Equal(t, http.StatusForbidden, w.do("POST", proj+"/telegram-bind-code", w.member, "").Code)
	require.Equal(t, http.StatusNotFound, w.do("POST", proj+"/telegram-bind-code", w.outsider, "").Code)

	// Подключённая группа видна в карточке проекта с названием.
	require.NoError(t, w.db.Exec(`UPDATE projects SET telegram_chat_id = -100500, telegram_chat_title = 'Команда' WHERE key = ?`, w.key).Error)
	rec = w.do("GET", proj, w.manager, "")
	require.Contains(t, rec.Body.String(), `"telegram_chat_id":-100500`)
	require.Contains(t, rec.Body.String(), `"telegram_chat_title":"Команда"`)

	require.Equal(t, http.StatusForbidden, w.do("DELETE", proj+"/telegram", w.member, "").Code)
	require.Equal(t, http.StatusNotFound, w.do("DELETE", proj+"/telegram", w.outsider, "").Code)
	require.Equal(t, http.StatusNoContent, w.do("DELETE", proj+"/telegram", w.manager, "").Code)
	rec = w.do("GET", proj, w.manager, "")
	require.NotContains(t, rec.Body.String(), "telegram_chat")
	require.Equal(t, http.StatusNoContent, w.do("DELETE", proj+"/telegram", w.manager, "").Code, "не подключена — тоже 204")
	var actions []string
	require.NoError(t, w.db.Raw(`SELECT a.action FROM audit_logs a JOIN projects p ON p.id = a.project_id WHERE p.key = ?`, w.key).Scan(&actions).Error)
	require.Equal(t, []string{telegram.AuditGroupUnbound}, actions)

	// Архив (§3.2): изменения запрещены.
	require.NoError(t, w.db.Exec(`UPDATE projects SET is_archived = true WHERE key = ?`, w.key).Error)
	rec = w.do("POST", proj+"/telegram-bind-code", w.manager, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "PROJECT_ARCHIVED")
	require.Equal(t, http.StatusConflict, w.do("DELETE", proj+"/telegram", w.manager, "").Code)
}

// Без TELEGRAM_BOT_TOKEN кода подключения нет (404), отключение работает.
func TestTelegramGroupRoutesWithoutBot(t *testing.T) {
	w := newWorld(t)
	proj := "/api/v1/projects/" + w.key
	require.Equal(t, http.StatusNotFound, w.do("POST", proj+"/telegram-bind-code", w.manager, "").Code)
	require.Equal(t, http.StatusNoContent, w.do("DELETE", proj+"/telegram", w.manager, "").Code)
}
