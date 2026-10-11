package task

import (
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Правила полей §3.3 — одни для создания и PATCH (и для бота, который вызывает сервис).

func validTitle(s string) (string, error) {
	s = strings.TrimSpace(s)
	if n := utf8.RuneCountInString(s); n < 1 || n > 200 {
		return "", fieldError("title", "от 1 до 200 символов")
	}
	return s, nil
}

// validDescription — пустое или из пробелов описание — его отсутствие.
func validDescription(d *string) (*string, error) {
	if d == nil || strings.TrimSpace(*d) == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(*d) > 20000 {
		return nil, fieldError("description", "не длиннее 20 000 символов")
	}
	return d, nil
}

func validType(s string) error {
	if !slices.Contains([]string{TypeTask, TypeBug, TypeFeature}, s) {
		return fieldError("type", "task, bug или feature")
	}
	return nil
}

func validPriority(s string) error {
	if !slices.Contains([]string{PriorityLow, PriorityMedium, PriorityHigh, PriorityCritical}, s) {
		return fieldError("priority", "low, medium, high или critical")
	}
	return nil
}

func validEstimate(v *int) error {
	if v != nil && (*v < 1 || *v > 60000) {
		return fieldError("estimate_minutes", "от 1 до 60 000 минут")
	}
	return nil
}

func parseDue(s *string) (*Date, error) {
	if s == nil {
		return nil, nil
	}
	d, err := time.Parse(time.DateOnly, *s)
	if err != nil {
		return nil, fieldError("due_date", "дата ГГГГ-ММ-ДД")
	}
	return &Date{d}, nil
}

// samePtr — оба nil или равные значения.
func samePtr[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func sameDate(a, b *Date) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(b.Time))
}

// isNil — nil, в том числе типизированный nil-указатель внутри interface.
func isNil(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	return rv.Kind() == reflect.Pointer && rv.IsNil()
}
