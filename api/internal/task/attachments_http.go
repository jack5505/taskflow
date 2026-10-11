package task

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/jack5505/taskflow/api/internal/auth"
	"github.com/jack5505/taskflow/api/internal/httpserver"
)

// multipartOverhead — запас на заголовки multipart сверх лимита файла.
const multipartOverhead = 1 << 20

func (h *Handlers) registerAttachments(api *gin.RouterGroup) {
	api.GET("/tasks/:task_key/attachments", h.requireAuth, h.attachments)
	api.POST("/tasks/:task_key/attachments", h.requireAuth, h.addAttachment)
	api.GET("/attachments/:id", h.requireAuth, h.attachmentLink)
	api.DELETE("/attachments/:id", h.requireAuth, h.deleteAttachment)
	// Ссылку открывает браузер без Bearer-токена: доступ даёт подпись (§3.9).
	api.GET("/attachments/:id/download", h.downloadAttachment)
}

// attachments — вложения задачи.
//
// @Summary      Вложения задачи
// @Description  Новые сверху. Участник проекта; вложения удалённой задачи — администратор системы.
// @Tags         attachments
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string  true  "ключ задачи"
// @Success      200       {array}   AttachmentView
// @Failure      401       {object}  apierr.Response
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/tasks/{task_key}/attachments [get]
func (h *Handlers) attachments(c *gin.Context) {
	list, err := h.svc.Attachments(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}

// addAttachment — загрузка вложения.
//
// @Summary      Загрузить вложение
// @Description  Любой участник проекта (§2). multipart/form-data, поле file, до 50 МБ, тип любой (§3.9).
// @Description  Событие attachment.added.
// @Tags         attachments
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        task_key  path      string  true  "ключ задачи"
// @Param        file      formData  file    true  "файл"
// @Success      201       {object}  AttachmentView
// @Failure      400       {object}  apierr.Response  "VALIDATION_FAILED: нет поля file"
// @Failure      401       {object}  apierr.Response
// @Failure      404       {object}  apierr.Response  "NOT_FOUND"
// @Failure      409       {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Failure      413       {object}  apierr.Response  "PAYLOAD_TOO_LARGE"
// @Router       /api/v1/tasks/{task_key}/attachments [post]
func (h *Handlers) addAttachment(c *gin.Context) {
	limit := h.svc.MaxAttachmentBytes() + multipartOverhead
	if c.Request.ContentLength > limit {
		httpserver.Abort(c, ErrAttachmentTooLarge)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	mr, err := c.Request.MultipartReader()
	if err != nil {
		httpserver.Abort(c, errFileRequired)
		return
	}
	for {
		part, err := mr.NextPart()
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			httpserver.Abort(c, ErrAttachmentTooLarge)
			return
		case err != nil: // io.EOF — поля file нет; иначе тело оборвано
			httpserver.Abort(c, errFileRequired)
			return
		}
		if part.FormName() != "file" || part.FileName() == "" {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		v, err := h.svc.AddAttachment(c.Request.Context(), auth.CurrentUser(c), c.Param("task_key"), UploadInput{
			FileName: part.FileName(), ContentType: part.Header.Get("Content-Type"), Body: part,
		})
		if err != nil {
			// Тело дальше не читаем: соединение закроется, клиент не ждёт дочитывания 50 МБ.
			c.Header("Connection", "close")
			httpserver.Abort(c, err)
			return
		}
		c.JSON(http.StatusCreated, v)
		return
	}
}

func attachmentID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		httpserver.Abort(c, ErrAttachmentNotFound)
		return uuid.Nil, false
	}
	return id, true
}

// attachmentLink — временная ссылка на скачивание.
//
// @Summary      Ссылка на скачивание вложения
// @Description  После проверки прав выдаёт ссылку, подписанную на 10 минут (§3.9). Ссылка открывается без токена.
// @Tags         attachments
// @Produce      json
// @Security     BearerAuth
// @Param        id   path      string  true  "id вложения"
// @Success      200  {object}  DownloadLink
// @Failure      401  {object}  apierr.Response
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/attachments/{id} [get]
func (h *Handlers) attachmentLink(c *gin.Context) {
	id, ok := attachmentID(c)
	if !ok {
		return
	}
	l, err := h.svc.AttachmentLink(c.Request.Context(), auth.CurrentUser(c), id)
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.JSON(http.StatusOK, l)
}

// downloadAttachment — скачивание по подписанной ссылке.
//
// @Summary      Скачать вложение
// @Description  Без токена: доступ по подписи и сроку из ссылки GET /attachments/{id}. Файл отдаётся как
// @Description  application/octet-stream с Content-Disposition: attachment и X-Content-Type-Options: nosniff —
// @Description  браузер не открывает его как страницу (§3.9).
// @Tags         attachments
// @Produce      octet-stream
// @Param        id   path      string  true  "id вложения"
// @Param        exp  query     int     true  "срок ссылки, unix-время"
// @Param        sig  query     string  true  "подпись"
// @Success      200  {file}    file
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: подпись неверна или срок истёк"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Router       /api/v1/attachments/{id}/download [get]
func (h *Handlers) downloadAttachment(c *gin.Context) {
	id, ok := attachmentID(c)
	if !ok {
		return
	}
	a, f, err := h.svc.OpenAttachment(c.Request.Context(), id, c.Query("exp"), c.Query("sig"))
	if err != nil {
		httpserver.Abort(c, err)
		return
	}
	defer func() { _ = f.Close() }()
	hd := c.Writer.Header()
	hd.Set("Content-Type", "application/octet-stream")
	hd.Set("Content-Disposition", contentDisposition(a.FileName))
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "private, no-store")
	hd.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(c.Writer, c.Request, "", a.CreatedAt, f)
}

// deleteAttachment — удаление вложения.
//
// @Summary      Удалить вложение
// @Description  Автор файла или менеджер проекта (§2: чужие — только менеджер). Файл удаляется с диска.
// @Tags         attachments
// @Security     BearerAuth
// @Param        id  path  string  true  "id вложения"
// @Success      204
// @Failure      401  {object}  apierr.Response
// @Failure      403  {object}  apierr.Response  "FORBIDDEN: чужое вложение"
// @Failure      404  {object}  apierr.Response  "NOT_FOUND"
// @Failure      409  {object}  apierr.Response  "PROJECT_ARCHIVED"
// @Router       /api/v1/attachments/{id} [delete]
func (h *Handlers) deleteAttachment(c *gin.Context) {
	id, ok := attachmentID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteAttachment(c.Request.Context(), auth.CurrentUser(c), id); err != nil {
		httpserver.Abort(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// contentDisposition — attachment с ASCII-именем для старых клиентов и filename* (RFC 6266, RFC 5987).
func contentDisposition(name string) string {
	var ascii, ext strings.Builder
	for _, r := range name {
		if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
			ascii.WriteRune(r)
		} else {
			ascii.WriteByte('_')
		}
	}
	const hexd = "0123456789ABCDEF"
	for _, b := range []byte(name) {
		if isAttrChar(b) {
			ext.WriteByte(b)
		} else {
			ext.WriteByte('%')
			ext.WriteByte(hexd[b>>4])
			ext.WriteByte(hexd[b&0xf])
		}
	}
	return `attachment; filename="` + ascii.String() + `"; filename*=UTF-8''` + ext.String()
}

// isAttrChar — attr-char из RFC 5987: буквы, цифры и !#$&+-.^_`|~.
func isAttrChar(b byte) bool {
	switch {
	case 'a' <= b && b <= 'z', 'A' <= b && b <= 'Z', '0' <= b && b <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", b) >= 0
}
