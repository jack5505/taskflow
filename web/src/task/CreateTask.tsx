import { lazy, Suspense, useEffect, useState } from "react";
import { Link, useLocation } from "react-router";
import type { TaskTask } from "../api/client";
import { hasOpenDialog, isCreateHotkey, isTypingTarget, projectKeyFromPath } from "./create";

// Окно с Markdown-редактором (marked + DOMPurify) — отдельным чанком, грузится при открытии.
const CreateTaskDialog = lazy(async () => ({ default: (await import("./CreateTaskDialog")).CreateTaskDialog }));

/**
 * Кнопка «Создать задачу» в шапке и горячая клавиша C на страницах проекта (ТЗ §7.6).
 * После создания — ссылка на новую задачу; запросы доски и бэклога окно обновляет само.
 */
export function CreateTask() {
  const { pathname } = useLocation();
  const current = projectKeyFromPath(pathname);
  // null — окно закрыто, строка — предвыбранный проект ("" — без предвыбора).
  const [open, setOpen] = useState<string | null>(null);
  const [created, setCreated] = useState<TaskTask | null>(null);

  useEffect(() => {
    if (current === null || open !== null) return;
    const onKey = (e: KeyboardEvent) => {
      if (!isCreateHotkey(e) || isTypingTarget(e.target) || hasOpenDialog(document)) return;
      e.preventDefault(); // иначе «c» попадёт в автофокусное поле названия
      setCreated(null);
      setOpen(current);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [current, open]);

  return (
    <>
      {created?.key && (
        <span className="notice create-notice" role="status">
          Создана <Link to={`/tasks/${created.key}`}>{created.key}</Link>{" "}
          <button type="button" className="link" aria-label="Скрыть" onClick={() => setCreated(null)}>
            ×
          </button>
        </span>
      )}
      <button
        type="button"
        className="primary"
        title="Горячая клавиша C на странице проекта"
        onClick={() => {
          setCreated(null);
          setOpen(current ?? "");
        }}
      >
        Создать задачу
      </button>
      {open !== null && (
        <Suspense fallback={null}>
          <CreateTaskDialog
            projectKey={open}
            onCancel={() => setOpen(null)}
            onCreated={(t) => {
              setOpen(null);
              setCreated(t);
            }}
          />
        </Suspense>
      )}
    </>
  );
}
