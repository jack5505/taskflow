package project

import (
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
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
var notificationDefaults = model.Toggles{
	NotifyTaskCreated: true, NotifyTaskTaken: true, NotifyTaskReturned: true, NotifyTaskReview: true,
	NotifyTaskRework: true, NotifyTaskAccepted: true, NotifyTaskCancelled: true,
	NotifySprintStarted: true, NotifySprintClosed: true,
	NotifyDailySummary: false, NotifyCommentCreated: false,
}

// EffectiveNotifications — настройки событий проекта с умолчаниями §8.6.
func EffectiveNotifications(stored events.JSON) map[string]bool {
	return notificationDefaults.Effective(stored)
}

// MergeNotifications накладывает изменения на сохранённые настройки; неизвестный ключ — ошибка.
func MergeNotifications(stored events.JSON, changes map[string]bool) (events.JSON, error) {
	return notificationDefaults.Merge(stored, changes)
}

// NotificationKeys — допустимые ключи по алфавиту.
func NotificationKeys() []string { return notificationDefaults.Keys() }
