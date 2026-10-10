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
)
