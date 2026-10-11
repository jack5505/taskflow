// Ключи кэша TanStack Query карточки задачи.
export const taskQueryKey = (key: string) => ["task", key] as const;
export const historyQueryKey = (key: string) => ["task-history", key] as const;
export const commentsQueryKey = (key: string) => ["task-comments", key] as const;
export const attachmentsQueryKey = (key: string) => ["task-attachments", key] as const;
