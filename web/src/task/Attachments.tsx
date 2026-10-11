import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { TaskAttachmentView } from "../api/client";
import { errorText } from "../api/errors";
import type { Actor } from "../board/transitions";
import { formatBytes, formatDateTime } from "../lib/format";
import { attachmentsQueryKey } from "./keys";
import { MAX_ATTACHMENT_BYTES, canManageAttachment, isPreviewable } from "./perms";

/** Ссылка на файл, подписанная на 10 минут (§3.9): берём заново, если старше 8 минут. */
function useDownloadLink(id: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: ["attachment-link", id],
    queryFn: async ({ signal }) => (await api.api.v1AttachmentsDetail(id!, { signal })).data,
    enabled: enabled && !!id,
    staleTime: 8 * 60_000,
    gcTime: 8 * 60_000,
  });
}

function AttachmentItem({
  a,
  manage,
  taskKey,
  timeZone,
}: {
  a: TaskAttachmentView;
  manage: boolean;
  taskKey: string;
  timeZone?: string;
}) {
  const queryClient = useQueryClient();
  const preview = isPreviewable(a.mime_type);
  const link = useDownloadLink(a.id, preview);
  const [error, setError] = useState("");

  // Скачивание: свежая ссылка после проверки прав; файл отдаётся с Content-Disposition: attachment.
  const download = useMutation({
    mutationFn: async () => (await api.api.v1AttachmentsDetail(a.id!)).data,
    onSuccess: (l) => {
      if (l.url) window.location.assign(l.url);
    },
    onError: (e) => setError(errorText(e)),
  });
  const remove = useMutation({
    mutationFn: () => api.api.v1AttachmentsDelete(a.id!),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: attachmentsQueryKey(taskKey) }),
    onError: (e) => setError(errorText(e)),
  });

  return (
    <li className="attachment">
      <div className="attachment-thumb">
        {preview && link.data?.url ? (
          <img src={link.data.url} alt={a.file_name} loading="lazy" />
        ) : (
          <span className="muted" aria-hidden="true">
            {(a.file_name?.split(".").pop() ?? "").slice(0, 4).toUpperCase() || "ФАЙЛ"}
          </span>
        )}
      </div>
      <div className="attachment-info">
        <strong className="attachment-name">{a.file_name}</strong>
        <span className="muted">
          {formatBytes(a.size_bytes)} · {a.author_name || "Пользователь"} · {formatDateTime(a.created_at, timeZone)}
        </span>
        <span className="comment-actions">
          <button
            type="button"
            className="link"
            disabled={download.isPending}
            onClick={() => {
              setError("");
              download.mutate();
            }}
          >
            Скачать
          </button>
          {manage && (
            <button
              type="button"
              className="link danger"
              disabled={remove.isPending}
              onClick={() => {
                setError("");
                if (window.confirm(`Удалить файл «${a.file_name}»?`)) remove.mutate();
              }}
            >
              Удалить
            </button>
          )}
        </span>
        {(error || link.isError) && <span role="alert">{error || errorText(link.error)}</span>}
      </div>
    </li>
  );
}

interface Props {
  taskKey: string;
  actor: Actor | null;
  /** Можно ли загружать (участник, проект не в архиве, задача не удалена). */
  canWrite: boolean;
  archived: boolean;
  timeZone?: string;
}

/** Вкладка «Вложения» (§7.5, §3.9): загрузка до 50 МБ, превью картинок, скачивание. */
export function Attachments({ taskKey, actor, canWrite, archived, timeZone }: Props) {
  const queryClient = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [localError, setLocalError] = useState("");
  const list = useQuery({
    queryKey: attachmentsQueryKey(taskKey),
    queryFn: async ({ signal }) => (await api.api.v1TasksAttachmentsList(taskKey, { signal })).data ?? [],
  });
  const upload = useMutation({
    mutationFn: (file: File) => api.api.v1TasksAttachmentsCreate(taskKey, { file }),
    onSettled: () => {
      if (input.current) input.current.value = "";
      return queryClient.invalidateQueries({ queryKey: attachmentsQueryKey(taskKey) });
    },
  });

  const onFile = (file: File | undefined) => {
    upload.reset();
    setLocalError("");
    if (!file) return;
    if (file.size > MAX_ATTACHMENT_BYTES) {
      setLocalError("Файл больше 50 МБ");
      if (input.current) input.current.value = "";
      return;
    }
    upload.mutate(file);
  };

  return (
    <div className="attachments">
      {canWrite && (
        <div className="attachment-upload">
          <label className="button-like">
            {upload.isPending ? "Загружаем…" : "Загрузить файл"}
            <input
              ref={input}
              type="file"
              className="visually-hidden"
              disabled={upload.isPending}
              onChange={(e) => onFile(e.target.files?.[0])}
            />
          </label>
          <span className="muted">до 50 МБ, любой тип</span>
        </div>
      )}
      {(localError || upload.isError) && <p role="alert">{localError || errorText(upload.error)}</p>}
      {list.isPending && <p className="muted">Загрузка…</p>}
      {list.isError && <p role="alert">{errorText(list.error)}</p>}
      {list.data?.length === 0 && <p className="muted">Вложений нет.</p>}
      <ul className="attachment-list">
        {list.data?.map((a) => (
          <AttachmentItem
            key={a.id}
            a={a}
            taskKey={taskKey}
            timeZone={timeZone}
            manage={canWrite && canManageAttachment(a.author_id, actor, archived)}
          />
        ))}
      </ul>
    </div>
  );
}
