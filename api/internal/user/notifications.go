package user

import (
	"github.com/jack5505/taskflow/api/internal/events"
	"github.com/jack5505/taskflow/api/internal/model"
)

// Личные уведомления в Telegram (ТЗ §8.7).
const (
	NotifyAssigned          = "assigned"           // вас назначили исполнителем
	NotifyRework            = "rework"             // задачу вернули на доработку
	NotifyAcceptedCancelled = "accepted_cancelled" // задачу приняли или отменили
	NotifyComment           = "comment"            // новый комментарий
	NotifyReviewRequested   = "review_requested"   // задача автора отправлена на проверку
	NotifyDue               = "due"                // срок завтра и срок просрочен
	NotifyTimerAutoStopped  = "timer_auto_stopped" // таймер остановлен через 12 часов
	NotifyWorklogTrimmed    = "worklog_trimmed"    // запись обрезана по дневному лимиту
	NotifyTaskDeleted       = "task_deleted"       // задача удалена
	NotifyMembership        = "membership"         // добавили в проект, исключили, сменили роль
)

// notificationDefaults — §8.7: по умолчанию все включены.
var notificationDefaults = model.Toggles{
	NotifyAssigned: true, NotifyRework: true, NotifyAcceptedCancelled: true, NotifyComment: true,
	NotifyReviewRequested: true, NotifyDue: true, NotifyTimerAutoStopped: true, NotifyWorklogTrimmed: true,
	NotifyTaskDeleted: true, NotifyMembership: true,
}

// NotificationKeys — допустимые ключи по алфавиту.
func NotificationKeys() []string { return notificationDefaults.Keys() }

// EffectiveNotifications — настройки §8.7 с умолчаниями.
func EffectiveNotifications(stored events.JSON) map[string]bool {
	return notificationDefaults.Effective(stored)
}

// MergeNotifications накладывает изменения; хранятся только отличия от умолчаний (то есть
// выключенные). Неизвестный ключ — ошибка.
func MergeNotifications(stored events.JSON, changes map[string]bool) (events.JSON, error) {
	return notificationDefaults.Merge(stored, changes)
}
