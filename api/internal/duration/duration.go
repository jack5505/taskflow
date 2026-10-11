// Package duration — формат длительности ТЗ §6.1: ввод «2h 30m», «45m», «3h», «1d» (1d = 8 часов),
// отображение «2h 30m» и в десятичных часах «2.50» (отчёты и таблицы).
package duration

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MinutesPerDay — 1d = 8 часов (§6.1).
const MinutesPerDay = 8 * 60

// ErrInvalid — строка не подходит под формат §6.1 (HTTP-слой отвечает 400 INVALID_DURATION).
var ErrInvalid = errors.New("duration: неверный формат")

// re — части d, h, m строго от большей к меньшей, каждая не более одного раза; пробелы между
// частями необязательны. Не больше 5 цифр в части — без переполнения.
var re = regexp.MustCompile(`^(?:(\d{1,5})d)?\s*(?:(\d{1,5})h)?\s*(?:(\d{1,5})m)?$`)

// Parse переводит строку §6.1 в минуты. Пустая строка, единицы не по порядку, повтор единицы,
// дробные числа, пробел между числом и единицей — ErrInvalid. Ноль («0m») разбирается: допустимый
// диапазон значений проверяет тот, кто знает правило (запись времени — от 1 минуты).
func Parse(s string) (int, error) {
	s = strings.TrimSpace(s)
	m := re.FindStringSubmatch(s)
	if s == "" || m == nil {
		return 0, ErrInvalid
	}
	total := 0
	for i, mul := range []int{MinutesPerDay, 60, 1} {
		if m[i+1] == "" {
			continue
		}
		n, _ := strconv.Atoi(m[i+1]) // \d{1,5} — всегда число
		total += n * mul
	}
	return total, nil
}

// Format — отображение §6.1: «2h 30m», «45m», «3h»; 0 — «0m». Дни не выводятся: 8h читается
// однозначно, а «1d» в отчёте путают с сутками.
func Format(minutes int) string {
	if minutes < 0 {
		return "-" + Format(-minutes)
	}
	h, m := minutes/60, minutes%60
	switch {
	case h == 0:
		return fmt.Sprintf("%dm", m)
	case m == 0:
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// Hours — десятичные часы с двумя знаками (§6.1): 150 → «2.50», 135 → «2.25».
func Hours(minutes int) string {
	return strconv.FormatFloat(float64(minutes)/60, 'f', 2, 64)
}
