// Package events — внутренние события системы (ТЗ §9.4): запись в outbox в одной транзакции
// с данными и доставка обработчикам через очередь asynq.
package events

// Типы событий из ТЗ §9.4. Новое событие сначала добавляется в ТЗ, потом сюда.
const (
	TaskCreated      = "task.created"
	TaskUpdated      = "task.updated"
	TaskTransitioned = "task.transitioned"
	TaskAssigned     = "task.assigned"
	TaskDeleted      = "task.deleted"
	TaskRestored     = "task.restored"

	CommentCreated  = "comment.created"
	AttachmentAdded = "attachment.added"

	WorklogCreated = "worklog.created"
	WorklogUpdated = "worklog.updated"
	WorklogDeleted = "worklog.deleted"

	SprintStarted     = "sprint.started"
	SprintTaskAdded   = "sprint.task_added"
	SprintTaskRemoved = "sprint.task_removed"
	SprintClosed      = "sprint.closed"

	TimerAutoStopped = "timer.auto_stopped"
	TimerTrimmed     = "timer.trimmed"

	MemberAdded       = "member.added"
	MemberRemoved     = "member.removed"
	MemberRoleChanged = "member.role_changed"
	UserDeactivated   = "user.deactivated"

	ProjectArchived = "project.archived"
	ProjectDeleted  = "project.deleted"

	// GitHubDelivery — служебное: принятая доставка вебхука GitHub App, обработку делает воркер (§12.2).
	GitHubDelivery = "github.delivery"
	// GitHubLinked — у задачи появилась ветка или PR (§12.6); GitHubPRChanged — сменилось состояние PR.
	GitHubLinked    = "github.linked"
	GitHubPRChanged = "github.pr_changed"
)

var known = map[string]struct{}{
	TaskCreated: {}, TaskUpdated: {}, TaskTransitioned: {}, TaskAssigned: {}, TaskDeleted: {}, TaskRestored: {},
	CommentCreated: {}, AttachmentAdded: {},
	WorklogCreated: {}, WorklogUpdated: {}, WorklogDeleted: {},
	SprintStarted: {}, SprintTaskAdded: {}, SprintTaskRemoved: {}, SprintClosed: {},
	TimerAutoStopped: {}, TimerTrimmed: {},
	MemberAdded: {}, MemberRemoved: {}, MemberRoleChanged: {}, UserDeactivated: {},
	ProjectArchived: {}, ProjectDeleted: {},
	GitHubDelivery: {}, GitHubLinked: {}, GitHubPRChanged: {},
}

// Known сообщает, описан ли тип события в ТЗ §9.4. Publish отклоняет неизвестные типы:
// опечатка в имени не должна превращаться в молча потерянное уведомление.
func Known(eventType string) bool {
	_, ok := known[eventType]
	return ok
}
