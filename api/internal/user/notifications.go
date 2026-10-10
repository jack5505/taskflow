package user

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jack5505/taskflow/api/internal/events"
)

// Личные уведомления в Telegram (ТЗ §8.7). В notification_settings хранятся только отличия
// от умолчаний (все включены); Effective накладывает их на умолчания.
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

var notificationKeys = []string{
	NotifyAssigned, NotifyRework, NotifyAcceptedCancelled, NotifyComment, NotifyReviewRequested,
	NotifyDue, NotifyTimerAutoStopped, NotifyWorklogTrimmed, NotifyTaskDeleted, NotifyMembership,
}

// NotificationKeys — допустимые ключи по алфавиту.
func NotificationKeys() []string {
	keys := append([]string(nil), notificationKeys...)
	sort.Strings(keys)
	return keys
}

func knownNotification(k string) bool {
	for _, n := range notificationKeys {
		if n == k {
			return true
		}
	}
	return false
}

// EffectiveNotifications — настройки §8.7 с умолчаниями (все включены).
func EffectiveNotifications(stored events.JSON) map[string]bool {
	out := make(map[string]bool, len(notificationKeys))
	for _, k := range notificationKeys {
		out[k] = true
	}
	var overrides map[string]bool
	if len(stored) > 0 && json.Unmarshal(stored, &overrides) == nil {
		for k, v := range overrides {
			if knownNotification(k) {
				out[k] = v
			}
		}
	}
	return out
}

// MergeNotifications накладывает изменения; хранит только отключённые. Неизвестный ключ — ошибка.
func MergeNotifications(stored events.JSON, changes map[string]bool) (events.JSON, error) {
	for k := range changes {
		if !knownNotification(k) {
			return nil, fmt.Errorf("неизвестное уведомление %q, допустимые: %v", k, NotificationKeys())
		}
	}
	merged := EffectiveNotifications(stored)
	for k, v := range changes {
		merged[k] = v
	}
	off := map[string]bool{}
	for k, v := range merged {
		if !v {
			off[k] = false
		}
	}
	b, err := json.Marshal(off)
	if err != nil {
		return nil, err
	}
	return events.JSON(b), nil
}
