package task_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/task"
)

const testSecret = "0123456789abcdef0123456789abcdef"

// zeros — бесконечный поток нулевых байт: большое тело без буфера в памяти.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// upload отправляет multipart с полем file; содержимое пишется в трубу потоком.
func (f *fixture) upload(t *testing.T, token, key, name, ctype string, content func(io.Writer) error) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close() }) // сервер мог не дочитать тело (413) — освобождаем писателя
	mw := multipart.NewWriter(pw)
	go func() {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, name))
		if ctype != "" {
			h.Set("Content-Type", ctype)
		}
		_ = mw.WriteField("comment", "лишнее поле пропускается")
		part, err := mw.CreatePart(h)
		if err == nil {
			err = content(part)
		}
		if err == nil {
			err = mw.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/tasks/"+key+"/attachments", pr)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+token)
	f.r.ServeHTTP(rec, r)
	var m map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &m)
	return rec, m
}

func (f *fixture) uploadText(t *testing.T, token, key, name, body string) map[string]any {
	t.Helper()
	rec, m := f.upload(t, token, key, name, "", func(w io.Writer) error {
		_, err := io.WriteString(w, body)
		return err
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	return m
}

// download открывает ссылку без токена — как браузер.
func (f *fixture) download(link string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, httptest.NewRequest("GET", link, nil))
	return rec
}

func (f *fixture) storedFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(f.files)
	require.NoError(t, err)
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// §3.9: загрузка, список, ссылка на 10 минут и безопасная отдача HTML-файла.
func TestAttachmentUploadListDownload(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.manager, `{"title":"С вложением"}`)["key"].(string)
	const html = "<html><body><script>alert(1)</script></body></html>"
	rec, a := f.upload(t, f.member, key, "отчёт <1>.html", "text/html", func(w io.Writer) error {
		_, err := io.WriteString(w, html)
		return err
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	id := a["id"].(string)
	require.Equal(t, "отчёт <1>.html", a["file_name"])
	require.Equal(t, "text/html", a["mime_type"])
	require.EqualValues(t, len(html), a["size_bytes"])
	require.Equal(t, f.memberU.ID.String(), a["author_id"])
	require.Equal(t, "Пользователь", a["author_name"])
	require.NotContains(t, a, "storage_path")
	require.Equal(t, []string{id}, f.storedFiles(t), "файл назван по uuid, временных файлов не осталось")

	var payload map[string]any
	var raw []byte
	require.NoError(t, f.db.Raw(`SELECT payload::text::bytea FROM outbox_events WHERE type = 'attachment.added' AND payload->>'key' = ?`, key).Row().Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &payload))
	require.Equal(t, id, payload["attachment_id"])
	require.Equal(t, "отчёт <1>.html", payload["file_name"])
	require.Equal(t, f.memberU.ID.String(), payload["author_id"])

	second := f.uploadText(t, f.manager, key, "b.txt", "второй")
	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/attachments", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var list []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list, 2)
	require.Equal(t, second["id"], list[0]["id"], "новые сверху")
	require.Equal(t, "text/plain", list[0]["mime_type"])

	before := time.Now()
	rec, link := f.do(t, "GET", "/api/v1/attachments/"+id, f.manager, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	exp, err := time.Parse(time.RFC3339, link["expires_at"].(string))
	require.NoError(t, err)
	require.WithinDuration(t, before.Add(10*time.Minute), exp, 5*time.Second)
	href := link["url"].(string)
	require.True(t, strings.HasPrefix(href, "/api/v1/attachments/"+id+"/download?"), href)

	rec = f.download(href)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, html, rec.Body.String())
	require.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"), "никогда не text/html")
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, `attachment; filename="_____ <1>.html"; filename*=UTF-8''%D0%BE%D1%82%D1%87%D1%91%D1%82%20%3C1%3E.html`,
		rec.Header().Get("Content-Disposition"))
	require.Contains(t, rec.Header().Get("Cache-Control"), "private")

	// Посторонний не видит ни список, ни ссылку; кривой id — 404.
	rec, body := f.do(t, "GET", "/api/v1/tasks/"+key+"/attachments", f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "NOT_FOUND", errCode(body))
	rec, _ = f.do(t, "GET", "/api/v1/attachments/"+id, f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/attachments/not-a-uuid", f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/attachments/"+uuid.NewString(), f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/attachments/"+id, "", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code, "ссылку выдают только с токеном")
	rec, _ = f.upload(t, f.outsider, key, "x.txt", "", func(w io.Writer) error { _, err := io.WriteString(w, "x"); return err })
	require.Equal(t, http.StatusNotFound, rec.Code)

	// Удалённая задача: вложения видит только администратор.
	rec, _ = f.do(t, "DELETE", "/api/v1/tasks/"+key, f.manager, "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/attachments", f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/attachments/"+id, f.manager, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/attachments/"+id, f.admin, "")
	require.Equal(t, http.StatusOK, rec.Code)
}

// Подделанная, изменённая и просроченная подпись — 403; верная подпись на несуществующее вложение — 404.
func TestAttachmentDownloadLinkRejected(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"Ссылки"}`)["key"].(string)
	id := f.uploadText(t, f.member, key, "a.txt", "секрет")["id"].(string)
	_, link := f.do(t, "GET", "/api/v1/attachments/"+id, f.member, "")
	u, err := url.Parse(link["url"].(string))
	require.NoError(t, err)
	q := u.Query()
	base := u.Path + "?"

	sig := []byte(q.Get("sig"))
	sig[0] ^= 1
	exp := q.Get("exp")
	for name, link := range map[string]string{
		"чужая подпись":     base + "exp=" + exp + "&sig=" + string(sig),
		"продлённый срок":   base + "exp=9999999999&sig=" + q.Get("sig"),
		"без подписи":       base + "exp=" + exp,
		"срок не число":     base + "exp=x&sig=" + q.Get("sig"),
		"просроченная":      task.SignedDownloadURL(testSecret, uuid.MustParse(id), time.Now().Add(-time.Second)),
		"подпись другим ID": strings.Replace(link["url"].(string), id, uuid.NewString(), 1),
	} {
		rec := f.download(link)
		require.Equal(t, http.StatusForbidden, rec.Code, name)
		require.NotEqual(t, "секрет", rec.Body.String(), name)
	}
	rec := f.download(task.SignedDownloadURL(testSecret, uuid.New(), time.Now().Add(time.Minute)))
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec = f.download(task.SignedDownloadURL(testSecret, uuid.MustParse(id), time.Now().Add(time.Minute)))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "секрет", rec.Body.String())
	rec = f.download("/api/v1/attachments/x/download?exp=1&sig=2")
	require.Equal(t, http.StatusNotFound, rec.Code)

	// Строка есть, файла на диске нет — 404, а не 500.
	require.NoError(t, os.Remove(filepath.Join(f.files, id)))
	rec = f.download(link["url"].(string))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// Сценарий приёмки §10.4: 49 МБ загружается, 51 МБ — 413 PAYLOAD_TOO_LARGE.
func TestAttachmentSizeLimit(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"Большие файлы"}`)["key"].(string)
	stream := func(n int64) func(io.Writer) error {
		return func(w io.Writer) error {
			_, err := io.CopyN(w, zeros{}, n)
			return err
		}
	}
	rec, a := f.upload(t, f.member, key, "49.bin", "", stream(49<<20))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.EqualValues(t, 49<<20, a["size_bytes"])
	require.Equal(t, "application/octet-stream", a["mime_type"])
	st, err := os.Stat(filepath.Join(f.files, a["id"].(string)))
	require.NoError(t, err)
	require.EqualValues(t, 49<<20, st.Size())

	rec, body := f.upload(t, f.member, key, "51.bin", "", stream(51<<20))
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	require.Equal(t, "PAYLOAD_TOO_LARGE", errCode(body))
	require.EqualValues(t, 50<<20, body["error"].(map[string]any)["details"].(map[string]any)["max_bytes"])
	// Ровно на байт больше лимита — тоже 413 (лимит файла, а не тела запроса).
	rec, _ = f.upload(t, f.member, key, "50.bin", "", stream(50<<20+1))
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Equal(t, []string{a["id"].(string)}, f.storedFiles(t), "отклонённые файлы не остаются на диске")

	// Заявленная длина больше лимита — отказ без чтения тела.
	r := httptest.NewRequest("POST", "/api/v1/tasks/"+key+"/attachments", strings.NewReader("x"))
	r.ContentLength = 60 << 20
	r.Header.Set("Authorization", "Bearer "+f.member)
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	rec = httptest.NewRecorder()
	f.r.ServeHTTP(rec, r)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)

	var n int
	require.NoError(t, f.db.Raw(`SELECT count(*) FROM attachments a JOIN tasks t ON t.id = a.task_id WHERE t.key = ?`, key).Scan(&n).Error)
	require.Equal(t, 1, n)
}

func TestAttachmentUploadValidation(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"Валидация"}`)["key"].(string)
	rec, body := f.do(t, "POST", "/api/v1/tasks/"+key+"/attachments", f.member, `{"file":"x"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "file", field(t, body))

	// multipart без поля file.
	r := httptest.NewRequest("POST", "/api/v1/tasks/"+key+"/attachments",
		strings.NewReader("--x\r\nContent-Disposition: form-data; name=\"other\"\r\n\r\nv\r\n--x--\r\n"))
	r.Header.Set("Authorization", "Bearer "+f.member)
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	rec = httptest.NewRecorder()
	f.r.ServeHTTP(rec, r)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	// Имя из одних точек — не имя файла.
	rec, body = f.upload(t, f.member, key, "..", "", func(w io.Writer) error { return nil })
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "file", field(t, body))

	// Тело оборвано посреди файла.
	rec, body = f.upload(t, f.member, key, "a.txt", "", func(w io.Writer) error {
		_, _ = io.WriteString(w, "начало")
		return io.ErrUnexpectedEOF
	})
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, "VALIDATION_FAILED", errCode(body))
	require.Empty(t, f.storedFiles(t))
}

// §2: удаляет автор и менеджер; исполнитель чужое — 403; посторонний — 404. Файл уходит с диска.
func TestAttachmentDelete(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"Удаление"}`)["key"].(string)
	byManager := f.uploadText(t, f.manager, key, "m.txt", "менеджер")["id"].(string)
	byMember := f.uploadText(t, f.member, key, "e.txt", "исполнитель")["id"].(string)
	byMember2 := f.uploadText(t, f.member, key, "e2.txt", "исполнитель")["id"].(string)

	rec, body := f.do(t, "DELETE", "/api/v1/attachments/"+byManager, f.member, "")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "FORBIDDEN", errCode(body))
	rec, _ = f.do(t, "DELETE", "/api/v1/attachments/"+byMember, f.outsider, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	rec, _ = f.do(t, "DELETE", "/api/v1/attachments/bad", f.member, "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec, _ = f.do(t, "DELETE", "/api/v1/attachments/"+byMember, f.member, "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	rec, _ = f.do(t, "DELETE", "/api/v1/attachments/"+byMember2, f.manager, "")
	require.Equal(t, http.StatusNoContent, rec.Code, "менеджер удаляет чужое")
	rec, _ = f.do(t, "DELETE", "/api/v1/attachments/"+byManager, f.admin, "")
	require.Equal(t, http.StatusNoContent, rec.Code, "администратор системы — права менеджера")
	rec, _ = f.do(t, "DELETE", "/api/v1/attachments/"+byManager, f.manager, "")
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Empty(t, f.storedFiles(t))
	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/attachments", f.member, "")
	require.Equal(t, "[]", rec.Body.String())
}

// В архивном проекте вложения читаются, но не добавляются и не удаляются (409 PROJECT_ARCHIVED).
func TestAttachmentArchivedProject(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"Архив"}`)["key"].(string)
	id := f.uploadText(t, f.member, key, "a.txt", "a")["id"].(string)
	require.NoError(t, f.db.Exec(`UPDATE projects SET is_archived = true WHERE id = ?`, f.p.ID).Error)

	rec, body := f.upload(t, f.member, key, "b.txt", "", func(w io.Writer) error { _, err := io.WriteString(w, "b"); return err })
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	require.Equal(t, "PROJECT_ARCHIVED", errCode(body))
	rec, body = f.do(t, "DELETE", "/api/v1/attachments/"+id, f.member, "")
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Equal(t, "PROJECT_ARCHIVED", errCode(body))
	rec, _ = f.do(t, "GET", "/api/v1/tasks/"+key+"/attachments", f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec, _ = f.do(t, "GET", "/api/v1/attachments/"+id, f.member, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{id}, f.storedFiles(t))
}

// Транзакция не прошла (пользователь деактивирован между проверкой прав и записью) — файла не остаётся.
func TestAttachmentFileRemovedWhenTxFails(t *testing.T) {
	f := newFixture(t)
	key := f.create(t, f.member, `{"title":"Откат"}`)["key"].(string)
	dir := t.TempDir()
	svc := task.NewService(f.db, project.NewService(f.db)).WithAttachments(dir, 1<<20, testSecret)
	require.NoError(t, f.db.Exec(`UPDATE users SET is_active = false WHERE id = ?`, f.memberU.ID).Error)
	_, err := svc.AddAttachment(context.Background(), f.memberU, key, task.UploadInput{FileName: "a.txt", Body: strings.NewReader("a")})
	require.Error(t, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}
