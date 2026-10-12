package task

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/apierr"
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/logx"
	"github.com/jack5505/taskflow/api/internal/model"
	"github.com/jack5505/taskflow/api/internal/project"
	"github.com/jack5505/taskflow/api/internal/user"
)

// DownloadTTL — срок подписанной ссылки на скачивание (§3.9).
const DownloadTTL = 10 * time.Minute

const maxFileNameRunes = 255

var (
	ErrAttachmentNotFound  = apierr.NotFound("Вложение не найдено")
	ErrAttachmentTooLarge  = apierr.PayloadTooLarge("Вложение больше допустимого размера (50 МБ)")
	errAttachmentForbidden = apierr.Forbidden("Удалить вложение может его автор или менеджер проекта")
	errBadDownloadLink     = apierr.Forbidden("Ссылка на скачивание недействительна или устарела")
	errFileRequired        = fieldError("file", "нужен файл в поле file (multipart/form-data)")
	errFileRead            = fieldError("file", "файл передан не полностью")
)

// Attachment — строка attachments (§3.9). StoragePath — имя файла в каталоге вложений, наружу не отдаётся.
type Attachment struct {
	model.Base
	TaskID      uuid.UUID `gorm:"type:uuid" json:"task_id"`
	AuthorID    uuid.UUID `gorm:"type:uuid" json:"author_id"`
	FileName    string    `json:"file_name"`
	MimeType    string    `json:"mime_type"`
	SizeBytes   int64     `json:"size_bytes"`
	StoragePath string    `json:"-"`
}

func (Attachment) TableName() string { return "attachments" }

// AttachmentView — вложение в ответе: с именем автора.
type AttachmentView struct {
	Attachment
	AuthorName string `json:"author_name"`
}

// DownloadLink — временная ссылка на скачивание (§3.9).
type DownloadLink struct {
	URL       string    `json:"url" example:"/api/v1/attachments/9b2c…/download?exp=1760180000&sig=…"`
	ExpiresAt time.Time `json:"expires_at"`
}

// attachmentStore — каталог файлов, лимит размера и ключ подписи ссылок.
type attachmentStore struct {
	dir      string
	maxBytes int64
	key      []byte
}

// WithAttachments задаёт каталог вложений (ATTACHMENTS_DIR), лимит размера и секрет, из которого
// выводится ключ подписи ссылок: отдельный от ключа JWT, чтобы подпись ссылки не годилась как токен.
func (s *Service) WithAttachments(dir string, maxBytes int64, secret string) *Service {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("taskflow/attachments/download"))
	s.files = attachmentStore{dir: dir, maxBytes: maxBytes, key: m.Sum(nil)}
	return s
}

// MaxAttachmentBytes — лимит размера вложения (обработчику — для MaxBytesReader).
func (s *Service) MaxAttachmentBytes() int64 { return s.files.maxBytes }

// tooLarge — 413 с лимитом в details.
func (st attachmentStore) tooLarge() error {
	return ErrAttachmentTooLarge.With("max_bytes", st.maxBytes)
}

func (st attachmentStore) sign(id uuid.UUID, exp int64) string {
	m := hmac.New(sha256.New, st.key)
	m.Write([]byte(id.String() + "|" + strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

func (st attachmentStore) url(id uuid.UUID, exp time.Time) string {
	e := exp.Unix()
	return fmt.Sprintf("/api/v1/attachments/%s/download?exp=%d&sig=%s", id, e, st.sign(id, e))
}

// Attachments — вложения задачи, новые сверху. Видит участник; удалённую задачу — администратор.
func (s *Service) Attachments(ctx context.Context, u *user.User, key string) ([]AttachmentView, error) {
	t, _, err := s.Get(ctx, u, key)
	if err != nil {
		return nil, err
	}
	list := []AttachmentView{}
	db := s.db.WithContext(ctx)
	if err := taskAttachments(db, t.ID, &list); err != nil {
		return nil, err
	}
	if err := fillNames(db, list, func(v *AttachmentView) *uuid.UUID { return &v.AuthorID },
		func(v *AttachmentView, name string) { v.AuthorName = name }); err != nil {
		return nil, err
	}
	return list, nil
}

// UploadInput — загружаемый файл: имя из multipart, заявленный тип части и поток содержимого.
type UploadInput struct {
	FileName    string
	ContentType string
	Body        io.Reader
}

// AddAttachment — загрузка вложения любым участником (§2, §3.9). Файл пишется во временный файл
// в каталоге вложений и переименовывается в имя-uuid; строка и событие attachment.added — в одной
// транзакции (openTask, как у AddComment: версия задачи не меняется), при её ошибке файл удаляется. Больше лимита — 413 PAYLOAD_TOO_LARGE.
func (s *Service) AddAttachment(ctx context.Context, u *user.User, key string, in UploadInput) (*AttachmentView, error) {
	// Права и архив проверяются до приёма файла: постороннему не на что тратить 50 МБ диска.
	// Окончательная проверка — в транзакции под блокировками (openTask).
	_, a, err := s.Get(ctx, u, key)
	if err != nil {
		return nil, err
	}
	if err := project.EnsureWritable(a.Project); err != nil {
		return nil, err
	}
	name := cleanFileName(in.FileName)
	if name == "" {
		return nil, errFileRequired
	}
	id := uuid.New()
	size, mimeType, err := s.files.save(id, in)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.files.dir, id.String())
	var view AttachmentView
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o, err := openTask(tx, u, key, preLock{}, "FOR SHARE")
		if err != nil {
			return err
		}
		if err := project.EnsureWritable(&o.project); err != nil {
			return err
		}
		t := &o.task
		at := Attachment{Base: model.Base{ID: id}, TaskID: t.ID, AuthorID: u.ID, FileName: name,
			MimeType: mimeType, SizeBytes: size, StoragePath: id.String()}
		if err := tx.Create(&at).Error; err != nil {
			return fmt.Errorf("task: attachment: %w", err)
		}
		view = AttachmentView{Attachment: at, AuthorName: u.DisplayName}
		return events.Publish(tx, events.AttachmentAdded, map[string]any{
			"attachment_id": at.ID, "task_id": t.ID, "key": t.Key, "project_id": t.ProjectID, "title": t.Title,
			"file_name": name, "size_bytes": size, "author_id": u.ID, "author_name": u.DisplayName,
			"assignee_id": t.AssigneeID, "reporter_id": t.ReporterID,
		})
	})
	if err != nil {
		removeFile(ctx, path)
		return nil, err
	}
	return &view, nil
}

// save пишет поток во временный файл и атомарно переименовывает его в dir/<id>.
// Возвращает размер и тип по содержимому (заявленный — только если по содержимому не понять).
func (st attachmentStore) save(id uuid.UUID, in UploadInput) (int64, string, error) {
	if err := os.MkdirAll(st.dir, 0o750); err != nil {
		return 0, "", fmt.Errorf("task: attachments dir: %w", err)
	}
	f, err := os.CreateTemp(st.dir, ".upload-*")
	if err != nil {
		return 0, "", fmt.Errorf("task: attachment temp: %w", err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	sn := &sniffer{}
	n, err := io.Copy(io.MultiWriter(f, sn), io.LimitReader(in.Body, st.maxBytes+1))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return 0, "", st.tooLarge()
	case err != nil:
		return 0, "", errFileRead
	case n > st.maxBytes:
		return 0, "", st.tooLarge()
	}
	if err := f.Sync(); err != nil {
		return 0, "", fmt.Errorf("task: attachment sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, "", fmt.Errorf("task: attachment close: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(st.dir, id.String())); err != nil {
		return 0, "", fmt.Errorf("task: attachment rename: %w", err)
	}
	ok = true
	return n, detectMime(sn.buf, in.ContentType), nil
}

// sniffer запоминает первые 512 байт — столько смотрит http.DetectContentType.
type sniffer struct{ buf []byte }

func (s *sniffer) Write(p []byte) (int, error) {
	if rest := 512 - len(s.buf); rest > 0 {
		s.buf = append(s.buf, p[:min(rest, len(p))]...)
	}
	return len(p), nil
}

// detectMime — тип для показа (превью картинок), без параметров. На отдачу не влияет: скачивание
// всегда application/octet-stream с Content-Disposition: attachment. По содержимому определяются
// картинки, PDF, архивы; для текста и неизвестного содержимого точнее заявленный тип (text/csv).
func detectMime(head []byte, declared string) string {
	sniffed := "application/octet-stream"
	if len(head) > 0 {
		sniffed, _, _ = mime.ParseMediaType(http.DetectContentType(head))
	}
	if sniffed != "application/octet-stream" && sniffed != "text/plain" {
		return sniffed
	}
	if mt, _, err := mime.ParseMediaType(declared); err == nil && (sniffed == "application/octet-stream" || strings.HasPrefix(mt, "text/")) {
		return mt
	}
	return sniffed
}

// cleanFileName — имя для показа: без пути, управляющих символов и не длиннее 255 символов.
func cleanFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = name[strings.LastIndex(name, "/")+1:]
	name = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == ".." {
		return ""
	}
	if r := []rune(name); len(r) > maxFileNameRunes {
		name = string(r[:maxFileNameRunes])
	}
	return name
}

// AttachmentLink — подписанная ссылка на 10 минут после проверки прав (§3.9): участник проекта,
// вложение удалённой задачи — только администратор.
func (s *Service) AttachmentLink(ctx context.Context, u *user.User, id uuid.UUID) (*DownloadLink, error) {
	key, err := attachmentTaskKey(s.db.WithContext(ctx), id)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, ErrAttachmentNotFound
	}
	if _, _, err := s.Get(ctx, u, key); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrAttachmentNotFound
		}
		return nil, err
	}
	exp := s.now().Add(DownloadTTL).Truncate(time.Second)
	return &DownloadLink{URL: s.files.url(id, exp), ExpiresAt: exp.UTC()}, nil
}

// OpenAttachment — файл по подписанной ссылке: подпись (сравнение за постоянное время) и срок.
// Вызывающий закрывает файл.
func (s *Service) OpenAttachment(ctx context.Context, id uuid.UUID, exp, sig string) (*Attachment, *os.File, error) {
	e, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || !hmac.Equal([]byte(sig), []byte(s.files.sign(id, e))) || s.now().Unix() > e {
		return nil, nil, errBadDownloadLink
	}
	a, err := attachmentByID(s.db.WithContext(ctx), id)
	if err != nil {
		return nil, nil, err
	}
	if a == nil {
		return nil, nil, ErrAttachmentNotFound
	}
	f, err := os.Open(filepath.Join(s.files.dir, filepath.Base(a.StoragePath)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrAttachmentNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("task: attachment open: %w", err)
	}
	return a, f, nil
}

// DeleteAttachment — удаление автором или менеджером (§2: чужие — только менеджер). Порядок как
// у onComment: задача (openTask, FOR SHARE), вложение FOR UPDATE, права (403), затем архив (409).
// Строка удаляется в транзакции, файл — после коммита.
func (s *Service) DeleteAttachment(ctx context.Context, u *user.User, id uuid.UUID) error {
	var path string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		key, err := attachmentTaskKey(tx, id)
		if err != nil {
			return err
		}
		if key == "" {
			return ErrAttachmentNotFound
		}
		o, err := openTask(tx, u, key, preLock{}, "FOR SHARE")
		if err != nil {
			return err
		}
		a, err := lockAttachment(tx, id, o.task.ID)
		if err != nil {
			return err
		}
		if a == nil {
			return ErrAttachmentNotFound
		}
		if a.AuthorID != u.ID && !o.actor.manager {
			return errAttachmentForbidden
		}
		if err := project.EnsureWritable(&o.project); err != nil {
			return err
		}
		path = filepath.Join(s.files.dir, filepath.Base(a.StoragePath))
		return tx.Delete(&a).Error
	})
	if errors.Is(err, ErrNotFound) {
		return ErrAttachmentNotFound
	}
	if err != nil {
		return err
	}
	removeFile(ctx, path)
	return nil
}

// removeFile удаляет файл вложения; ошибка только в лог — строки в базе уже нет (или не было).
func removeFile(ctx context.Context, path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		logx.From(ctx).Error("attachment: remove file", "path", path, "err", err)
	}
}
