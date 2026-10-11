// Ключи кэша TanStack Query карточки задачи.

/** Ключ проекта из ключа задачи «TM-42» — «{ключ проекта}-{номер}» (§3.3). */
export const projectKeyOf = (taskKey: string) => taskKey.replace(/-\d+$/, "");
export const taskQueryKey = (key: string) => ["task", key] as const;
export const historyQueryKey = (key: string) => ["task-history", key] as const;
export const commentsQueryKey = (key: string) => ["task-comments", key] as const;
export const attachmentsQueryKey = (key: string) => ["task-attachments", key] as const;
export const worklogsQueryKey = (key: string) => ["task-worklogs", key] as const;
