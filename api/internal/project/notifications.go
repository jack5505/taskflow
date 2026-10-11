package project

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Notification* — события бота в группе проекта (ТЗ §8.6). В notification_settings хранятся
// только отличия от умолчаний; Effective накладывает их на таблицу §8.6.
const (
	NotifyTaskCreated    = "task_created"
	NotifyTaskTaken      = "task_taken"
	NotifyTaskReturned   = "task_returned"
	NotifyTaskReview     = "task_review"
	NotifyTaskRework     = "task_rework"
	NotifyTaskAccepted   = "task_accepted"
	NotifyTaskCancelled  = "task_cancelled"
	NotifySprintStarted  = "sprint_started"
	NotifySprintClosed   = "sprint_closed"
	NotifyDailySummary   = "daily_summary"
	NotifyCommentCreated = "comment_created"
)

// notificationDefaults — столбец «По умолчанию» таблицы §8.6.
var notificationDefaults = map[string]bool{
	NotifyTaskCreated: true, NotifyTaskTaken: true, NotifyTaskReturned: true, NotifyTaskReview: true,
	NotifyTaskRework: true, NotifyTaskAccepted: true, NotifyTaskCancelled: true,
	NotifySprintStarted: true, NotifySprintClosed: true,
	NotifyDailySummary: false, NotifyCommentCreated: false,
}

// EffectiveNotifications — настройки событий проекта с умолчаниями §8.6.
func EffectiveNotifications(stored events.JSON) map[string]bool {
	out := make(map[string]bool, len(notificationDefaults))
	for k, v := range notificationDefaults {
		out[k] = v
	}
	var overrides map[string]bool
	if len(stored) > 0 && json.Unmarshal(stored, &overrides) == nil {
		for k, v := range overrides {
			if _, ok := notificationDefaults[k]; ok {
				out[k] = v
			}
		}
	}
	return out
}

// MergeNotifications накладывает изменения на сохранённые настройки; неизвестный ключ — ошибка.
func MergeNotifications(stored events.JSON, changes map[string]bool) (events.JSON, error) {
	for k := range changes {
		if _, ok := notificationDefaults[k]; !ok {
			return nil, fmt.Errorf("неизвестное событие %q, допустимые: %v", k, NotificationKeys())
		}
	}
	merged := EffectiveNotifications(stored)
	for k, v := range changes {
		merged[k] = v
	}
	diff := map[string]bool{}
	for k, v := range merged {
		if notificationDefaults[k] != v {
			diff[k] = v
		}
	}
	b, err := json.Marshal(diff)
	if err != nil {
		return nil, err
	}
	return events.JSON(b), nil
}

// NotificationKeys — допустимые ключи по алфавиту.
func NotificationKeys() []string {
	keys := make([]string, 0, len(notificationDefaults))
	for k := range notificationDefaults {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
