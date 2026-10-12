// Package github — интеграция с GitHub (ТЗ §12): приём вебхуков GitHub App, связь задач с
// ветками и pull request'ами, привязка репозитория к проекту и связка аккаунтов.
package github

// WebhookPath — приём вебхуков GitHub App (§12.2), без JWT: проверяется подпись.
const WebhookPath = "/github/webhook"
