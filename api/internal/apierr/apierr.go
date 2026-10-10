// Package apierr — единый формат ошибок API (ТЗ §9.3):
//
//	{"error": {"code": "INVALID_TRANSITION", "message": "…", "details": {"request_id": "…"}}}
//
// Доменный слой возвращает *Error с кодом из этого пакета; HTTP-слой пишет его как есть.
// Любая другая ошибка превращается в 500 INTERNAL без подробностей наружу.
package apierr

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
)

// Коды ошибок из ТЗ §9.3. Новый код сначала добавляется в ТЗ, потом сюда.
const (
	CodeValidation       = "VALIDATION_FAILED"
	CodeInvalidJSON      = "INVALID_JSON"
	CodeInvalidDuration  = "INVALID_DURATION" // §6.1
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeMethodNotAllowed = "METHOD_NOT_ALLOWED"
	CodePayloadTooLarge  = "PAYLOAD_TOO_LARGE" // §3.9, 413
	CodeRateLimited      = "RATE_LIMITED"      // 429
	CodeInternal         = "INTERNAL"

	// 409 — конфликты состояния.
	CodeInvalidTransition   = "INVALID_TRANSITION"
	CodeTaskAlreadyTaken    = "TASK_ALREADY_TAKEN"
	CodeSprintAlreadyActive = "SPRINT_ALREADY_ACTIVE"
	CodeDailyLimitExceeded  = "DAILY_LIMIT_EXCEEDED"
	CodeTimerAlreadyRunning = "TIMER_ALREADY_RUNNING"
	CodeVersionConflict     = "VERSION_CONFLICT"
	CodeLastManager         = "LAST_MANAGER"
	CodeProjectArchived     = "PROJECT_ARCHIVED"
)

// Error — ошибка с HTTP-статусом, кодом и деталями для клиента.
// Значение неизменяемое: With и Wrap возвращают копию, поэтому доменные пакеты могут
// держать sentinel-ошибки (var ErrInvalidTransition = apierr.Conflict(…)) и дополнять их
// деталями в каждом запросе, не трогая общий экземпляр.
type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap отдаёт исходную ошибку для errors.Is/As.
func (e *Error) Unwrap() error { return e.cause }

// Is позволяет сравнивать копии с исходной sentinel-ошибкой: errors.Is(err, ErrInvalidTransition).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Status == e.Status && t.Code == e.Code
}

func (e *Error) clone() *Error {
	c := *e
	c.Details = maps.Clone(e.Details)
	return &c
}

// With возвращает копию ошибки с добавленным полем в details.
func (e *Error) With(key string, value any) *Error {
	c := e.clone()
	if c.Details == nil {
		c.Details = map[string]any{}
	}
	c.Details[key] = value
	return c
}

// Wrap возвращает копию с исходной ошибкой для логов; клиенту она не показывается.
func (e *Error) Wrap(cause error) *Error {
	c := e.clone()
	c.cause = cause
	return c
}

// New — произвольная ошибка с явным статусом и кодом.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(code, message string) *Error { return New(http.StatusBadRequest, code, message) }
func Unauthorized(message string) *Error {
	return New(http.StatusUnauthorized, CodeUnauthorized, message)
}
func Forbidden(message string) *Error      { return New(http.StatusForbidden, CodeForbidden, message) }
func NotFound(message string) *Error       { return New(http.StatusNotFound, CodeNotFound, message) }
func Conflict(code, message string) *Error { return New(http.StatusConflict, code, message) }
func PayloadTooLarge(message string) *Error {
	return New(http.StatusRequestEntityTooLarge, CodePayloadTooLarge, message)
}
func RateLimited(message string) *Error {
	return New(http.StatusTooManyRequests, CodeRateLimited, message)
}

// Internal — 500. Сообщение фиксированное: подробности остаются в логе (cause).
func Internal(cause error) *Error {
	return New(http.StatusInternalServerError, CodeInternal, "Внутренняя ошибка сервера").Wrap(cause)
}

// From приводит любую ошибку к *Error: доменные ошибки — как есть, остальные — 500.
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err)
}

// Response — тело ответа с ошибкой (для Swagger и тестов).
type Response struct {
	Error Body `json:"error"`
}

// Body — содержимое поля error.
type Body struct {
	Code    string         `json:"code" example:"INVALID_TRANSITION"`
	Message string         `json:"message" example:"Переход done → todo не разрешён"`
	Details map[string]any `json:"details"`
}
