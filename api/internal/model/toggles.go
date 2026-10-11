package model

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Toggles — набор включаемых настроек с умолчаниями (события бота проекта §8.6, личные
// уведомления §8.7). В базе хранятся только отличия от умолчаний.
type Toggles map[string]bool

// Effective — все ключи с умолчаниями и сохранёнными отличиями; битые или чужие ключи игнорируются.
func (d Toggles) Effective(stored events.JSON) map[string]bool {
	out := make(map[string]bool, len(d))
	for k, v := range d {
		out[k] = v
	}
	var overrides map[string]bool
	if len(stored) > 0 && json.Unmarshal(stored, &overrides) == nil {
		for k, v := range overrides {
			if _, ok := d[k]; ok {
				out[k] = v
			}
		}
	}
	return out
}

// Merge накладывает изменения и возвращает отличия от умолчаний для хранения.
// Неизвестный ключ — ошибка с перечнем допустимых.
func (d Toggles) Merge(stored events.JSON, changes map[string]bool) (events.JSON, error) {
	for k := range changes {
		if _, ok := d[k]; !ok {
			return nil, fmt.Errorf("неизвестный ключ %q, допустимые: %v", k, d.Keys())
		}
	}
	merged := d.Effective(stored)
	for k, v := range changes {
		merged[k] = v
	}
	diff := map[string]bool{}
	for k, v := range merged {
		if d[k] != v {
			diff[k] = v
		}
	}
	b, err := json.Marshal(diff)
	if err != nil {
		return nil, err
	}
	return events.JSON(b), nil
}

// Keys — допустимые ключи по алфавиту.
func (d Toggles) Keys() []string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
