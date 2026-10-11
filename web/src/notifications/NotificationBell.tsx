import { useEffect, useRef, useState } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { api } from "../api";
import type { NotificationNotification, NotificationPage } from "../api/client";
import { errorText } from "../api/errors";
import { useTimeZone } from "../auth/hooks";
import { formatDateTime } from "../lib/format";
import {
  badgeText,
  describeNotification,
  listQueryKey,
  markReadInCache,
  notificationsQueryKey,
  unreadQueryKey,
  type NotificationsData,
} from "./notifications";

const PAGE_SIZE = 20;
// Пушей в браузер нет — новые события подтягиваем опросом.
const POLL_MS = 60_000;
const markKey = ["notifications-read"];

/**
 * Колокольчик в шапке (ТЗ §7.7): счётчик непрочитанных, список событий за 30 дней, новые первыми,
 * с подгрузкой по курсору; отметка прочитанным по одному (или переходом по ссылке) и всех сразу.
 */
export function NotificationBell() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const timeZone = useTimeZone();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);

  // Закрытый колокольчик опрашивает только счётчик — один короткий запрос, сколько бы страниц
  // списка ни было загружено.
  const counter = useQuery({
    queryKey: unreadQueryKey,
    queryFn: async ({ signal }) => (await api.api.v1MeNotificationsList({ limit: 1 }, { signal })).data,
    refetchInterval: POLL_MS,
  });
  // Список грузится при открытии (и перечитывается при каждом открытии) и опрашивается, пока открыт.
  const list = useInfiniteQuery({
    queryKey: listQueryKey,
    queryFn: async ({ pageParam, signal }) =>
      (await api.api.v1MeNotificationsList({ limit: PAGE_SIZE, cursor: pageParam || undefined }, { signal })).data,
    initialPageParam: "",
    getNextPageParam: (last) => last.next_cursor || undefined,
    enabled: open,
    refetchInterval: open ? POLL_MS : false,
  });

  const markRead = useMutation({
    mutationKey: markKey,
    // ids null — все.
    mutationFn: (ids: string[] | null) => api.api.v1MeNotificationsReadCreate(ids ? { ids } : {}),
    onMutate: async (ids) => {
      await queryClient.cancelQueries({ queryKey: notificationsQueryKey });
      const readAt = new Date().toISOString();
      queryClient.setQueryData<NotificationsData>(listQueryKey, (d) => markReadInCache(d, ids, readAt));
      // Счётчик — та же страница, только одна и без курсора.
      queryClient.setQueryData<NotificationPage>(unreadQueryKey, (p) =>
        p ? markReadInCache({ pages: [p], pageParams: [""] }, ids, readAt)?.pages[0] : p,
      );
    },
    // Перечитываем с сервера, когда закончились все отметки: ответ на первую вернул бы
    // непрочитанным то, что отмечено второй. При ошибке отметка вернётся к серверной.
    onSettled: () => {
      if (queryClient.isMutating({ mutationKey: markKey }) <= 1)
        void queryClient.invalidateQueries({ queryKey: notificationsQueryKey });
    },
  });

  // Закрыть по щелчку мимо и по Escape.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (root.current && !root.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const unread = counter.data?.unread_count ?? 0;
  const items = list.data?.pages.flatMap((p) => p.items ?? []) ?? [];
  const badge = badgeText(unread);

  const toggle = () => {
    // При открытии сверяем и счётчик: список и счётчик должны совпадать.
    if (!open) void queryClient.invalidateQueries({ queryKey: notificationsQueryKey });
    setOpen(!open);
  };

  const follow = (n: NotificationNotification, link: string) => {
    if (!n.read_at && n.id) markRead.mutate([n.id]);
    setOpen(false);
    void navigate(link);
  };

  return (
    <div className="bell" ref={root}>
      <button
        type="button"
        className="bell-button"
        aria-haspopup="true"
        aria-expanded={open}
        aria-label={unread ? `Уведомления, непрочитанных: ${unread}` : "Уведомления"}
        title="Уведомления"
        onClick={toggle}
      >
        <span aria-hidden="true">🔔</span>
        {badge && (
          <span className="bell-count" aria-hidden="true">
            {badge}
          </span>
        )}
      </button>
      {open && (
        <div className="bell-panel" role="dialog" aria-label="Уведомления">
          <div className="bell-head">
            <strong>Уведомления</strong>
            {unread > 0 && (
              <button type="button" className="link" disabled={markRead.isPending} onClick={() => markRead.mutate(null)}>
                Отметить все прочитанными
              </button>
            )}
          </div>
          {list.isPending && <p className="muted bell-empty">Загрузка…</p>}
          {list.isError && (
            <p className="bell-empty" role="alert">
              {errorText(list.error)}
            </p>
          )}
          {list.isSuccess && items.length === 0 && <p className="muted bell-empty">Уведомлений нет.</p>}
          {items.length > 0 && (
            <ul className="bell-list">
              {items.map((n) => {
                const d = describeNotification(n);
                return (
                  <li key={n.id} className={n.read_at ? "bell-item" : "bell-item unread"}>
                    <div className="bell-text">
                      {d.link ? (
                        <a
                          href={d.link}
                          onClick={(e) => {
                            // Обычный щелчок — переход внутри приложения с отметкой прочитанным;
                            // с модификаторами (новая вкладка) — как у обычной ссылки.
                            if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                            e.preventDefault();
                            follow(n, d.link!);
                          }}
                        >
                          {d.text}
                        </a>
                      ) : (
                        <span>{d.text}</span>
                      )}
                      {d.detail && <div className="muted bell-detail">{d.detail}</div>}
                      <div className="muted bell-time">{formatDateTime(n.created_at, timeZone)}</div>
                    </div>
                    {!n.read_at && n.id && (
                      <button
                        type="button"
                        className="link bell-read"
                        title="Отметить прочитанным"
                        onClick={() => markRead.mutate([n.id!])}
                      >
                        Прочитано
                      </button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
          {list.hasNextPage && (
            <div className="bell-more">
              <button type="button" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
                {list.isFetchingNextPage ? "Загрузка…" : "Показать ещё"}
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
