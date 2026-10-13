import type { ReactNode } from "react";

import { PRIORITIES, TASK_TYPES, priorityLabels, typeLabels, type Priority, type TaskType } from "../board/filters";

// Иконки интерфейса — свои SVG одним набором (вместо эмодзи и символов, которые на разных ОС
// рисуются по-разному). Цвет — currentColor или токены из index.css; размер — 16 px по умолчанию.

const paths = {
  bell: (
    <>
      <path d="M4 11V7a4 4 0 0 1 8 0v4l1.2 1.2H2.8z" />
      <path d="M6.5 13.6a1.6 1.6 0 0 0 3 0" />
    </>
  ),
  timer: (
    <>
      <circle cx="8" cy="9" r="5.2" />
      <path d="M8 6.2V9l1.8 1.2M6.4 1.8h3.2" />
    </>
  ),
  plus: <path d="M8 3v10M3 8h10" />,
  close: <path d="m4 4 8 8M12 4l-8 8" />,
  play: <path d="M5 3.5v9l7-4.5z" fill="currentColor" />,
  stop: <rect x="4" y="4" width="8" height="8" rx="1" fill="currentColor" />,
  "arrow-left": <path d="M13 8H3m4-4L3 8l4 4" />,
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof paths;

/** Иконка по имени; без label — декоративная (скрыта от экранных чтецов). */
export function Icon({ name, label, size = 16 }: { name: IconName; label?: string; size?: number }) {
  return (
    <svg
      className="icon"
      viewBox="0 0 16 16"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      focusable="false"
      {...(label ? { role: "img", "aria-label": label } : { "aria-hidden": true })}
    >
      {label && <title>{label}</title>}
      {paths[name]}
    </svg>
  );
}

const MARKS: Record<TaskType, ReactNode> = {
  task: <path d="m5 8.2 2 2 4-4.2" fill="none" />,
  bug: <circle cx="8" cy="8" r="2.2" fill="currentColor" stroke="none" />,
  feature: <path d="M8 5v6M5 8h6" fill="none" />,
};

/** Тип задачи (§3.3): цветной квадрат со своим знаком — галочка, точка, плюс. Неизвестный и пустой — «Задача». */
export function TypeIcon({ type: raw, size = 16 }: { type?: string; size?: number }) {
  const type: TaskType = (TASK_TYPES as readonly string[]).includes(raw ?? "") ? (raw as TaskType) : "task";
  return (
    <svg className={`type-icon type-${type}`} viewBox="0 0 16 16" width={size} height={size} role="img" aria-label={typeLabels[type]} focusable="false">
      <title>{typeLabels[type]}</title>
      <rect x="1" y="1" width="14" height="14" rx={type === "bug" ? 7 : 3.5} className="type-icon-bg" />
      <g stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" className="type-icon-mark">
        {MARKS[type]}
      </g>
    </svg>
  );
}

const LEVEL: Record<Priority, number> = { low: 1, medium: 2, high: 3, critical: 4 };

/** Приоритет (§3.3): четыре столбика, закрашено по уровню. Неизвестный и пустой — «Средний». */
export function PriorityIcon({ priority: raw }: { priority?: string }) {
  const priority: Priority = (PRIORITIES as readonly string[]).includes(raw ?? "") ? (raw as Priority) : "medium";
  const n = LEVEL[priority];
  const label = `Приоритет: ${priorityLabels[priority]}`;
  return (
    <svg className={`priority-icon priority-${priority}`} viewBox="0 0 16 14" width="16" height="14" role="img" aria-label={label} focusable="false">
      <title>{label}</title>
      {[0, 1, 2, 3].map((i) => (
        <rect key={i} x={1 + i * 3.6} y={12 - (i + 1) * 2.6} width="2.6" height={(i + 1) * 2.6} rx="0.6" className={i < n ? "on" : "off"} />
      ))}
    </svg>
  );
}
