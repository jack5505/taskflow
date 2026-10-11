import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { TaskCommentView } from "../api/client";
import { errorText } from "../api/errors";
import type { Actor } from "../board/transitions";
import { formatDateTime } from "../lib/format";
import { MarkdownEditor, MarkdownView } from "./Markdown";
import { commentsQueryKey } from "./keys";
import { canManageComment } from "./perms";

const MAX_COMMENT = 5000;
// Поле ввода ограничено, как тело запроса на сервере; правило 1–5000 символов проверяет bodyError.
const INPUT_LIMIT = 20000;

/** Ошибка длины комментария до отправки (§3.6: 1–5000 символов после обрезки). */
function bodyError(body: string): string {
  const n = [...body.trim()].length;
  if (n === 0) return "Комментарий пустой";
  if (n > MAX_COMMENT) return "Не длиннее 5000 символов";
  return "";
}

interface Props {
  taskKey: string;
  actor: Actor | null;
  /** Можно ли писать (участник, проект не в архиве, задача не удалена). */
  canWrite: boolean;
  archived: boolean;
  timeZone?: string;
}

function CommentItem({
  c,
  manage,
  taskKey,
  timeZone,
}: {
  c: TaskCommentView;
  manage: boolean;
  taskKey: string;
  timeZone?: string;
}) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<string | null>(null);
  const [localError, setLocalError] = useState("");
  const done = () => queryClient.invalidateQueries({ queryKey: commentsQueryKey(taskKey) });
  const update = useMutation({
    mutationFn: (body: string) => api.api.v1CommentsPartialUpdate(c.id!, { body }),
    onSuccess: () => {
      setDraft(null);
      return done();
    },
  });
  const remove = useMutation({
    mutationFn: () => api.api.v1CommentsDelete(c.id!),
    onSuccess: done,
  });
  const error = localError || (update.isError ? errorText(update.error) : remove.isError ? errorText(remove.error) : "");

  return (
    <li className="comment">
      <div className="comment-head">
        <strong>{c.author_name || "Пользователь"}</strong>
        <span className="muted">{formatDateTime(c.created_at, timeZone)}</span>
        {c.source === "telegram" && <span className="badge">из Telegram</span>}
        {c.edited && <span className="muted">(изменён)</span>}
        {manage && draft === null && (
          <span className="comment-actions">
            <button
              type="button"
              className="link"
              onClick={() => {
                update.reset();
                setLocalError("");
                setDraft(c.body ?? "");
              }}
            >
              Изменить
            </button>
            <button
              type="button"
              className="link danger"
              disabled={remove.isPending}
              onClick={() => window.confirm("Удалить комментарий?") && remove.mutate()}
            >
              Удалить
            </button>
          </span>
        )}
      </div>
      {draft === null ? (
        <MarkdownView source={c.body} />
      ) : (
        <form
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            const err = bodyError(draft);
            setLocalError(err);
            if (!err) update.mutate(draft.trim());
          }}
        >
          <MarkdownEditor label="Текст комментария" value={draft} onChange={setDraft} maxLength={INPUT_LIMIT} rows={4} autoFocus />
          <div className="actions">
            <button type="submit" className="primary" disabled={update.isPending}>
              {update.isPending ? "Сохраняем…" : "Сохранить"}
            </button>
            <button
              type="button"
              onClick={() => {
                setDraft(null);
                setLocalError("");
                update.reset();
              }}
            >
              Отмена
            </button>
          </div>
        </form>
      )}
      {error && <p role="alert">{error}</p>}
    </li>
  );
}

/** Вкладка «Комментарии» (§7.5, §3.6): правка и удаление — автору и менеджеру. */
export function Comments({ taskKey, actor, canWrite, archived, timeZone }: Props) {
  const queryClient = useQueryClient();
  const comments = useQuery({
    queryKey: commentsQueryKey(taskKey),
    queryFn: async ({ signal }) => (await api.api.v1TasksCommentsList(taskKey, { signal })).data ?? [],
  });
  const [body, setBody] = useState("");
  const [localError, setLocalError] = useState("");
  const create = useMutation({
    mutationFn: (text: string) => api.api.v1TasksCommentsCreate(taskKey, { body: text }),
    onSuccess: () => {
      setBody("");
      return queryClient.invalidateQueries({ queryKey: commentsQueryKey(taskKey) });
    },
  });

  return (
    <div className="comments">
      {comments.isPending && <p className="muted">Загрузка…</p>}
      {comments.isError && <p role="alert">{errorText(comments.error)}</p>}
      {comments.data?.length === 0 && <p className="muted">Комментариев пока нет.</p>}
      <ul className="comment-list">
        {comments.data?.map((c) => (
          <CommentItem
            key={c.id}
            c={c}
            taskKey={taskKey}
            timeZone={timeZone}
            manage={canWrite && canManageComment(c.author_id, actor, archived)}
          />
        ))}
      </ul>
      {canWrite && (
        <form
          className="form"
          onSubmit={(e) => {
            e.preventDefault();
            const err = bodyError(body);
            setLocalError(err);
            if (!err) create.mutate(body.trim());
          }}
        >
          <MarkdownEditor
            label="Новый комментарий"
            value={body}
            onChange={(v) => {
              setBody(v);
              setLocalError("");
            }}
            maxLength={INPUT_LIMIT}
            rows={4}
          />
          {(localError || create.isError) && <p role="alert">{localError || errorText(create.error)}</p>}
          <div className="actions">
            <button type="submit" className="primary" disabled={create.isPending}>
              {create.isPending ? "Отправляем…" : "Отправить"}
            </button>
          </div>
        </form>
      )}
    </div>
  );
}
