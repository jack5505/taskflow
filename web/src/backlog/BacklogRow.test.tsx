import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import type { TaskListItem } from "../api/client";
import { RowCells } from "./BacklogRow";

const base: TaskListItem = {
  id: "t1",
  key: "TM-1",
  title: "Задача",
  status: "in_progress",
  updated_at: "2026-10-26T09:05:00Z",
};

function render(task: TaskListItem) {
  return renderToStaticMarkup(
    <ul>
      <li>
        <RowCells task={task} linked={false} timeZone="Asia/Tashkent" />
      </li>
    </ul>,
  );
}

describe("RowCells", () => {
  it("значок статуса и время последнего изменения в поясе пользователя", () => {
    const html = render(base);
    expect(html).toContain('class="status-dot status-in_progress"');
    expect(html).toContain('<time class="row-updated muted" dateTime="2026-10-26T09:05:00Z" title="Последнее изменение">26.10.2026 14:05</time>');
    expect(html).not.toContain("row-done-at");
  });

  it("у завершённой задачи — время завершения под статусом", () => {
    const html = render({ ...base, status: "done", completed_at: "2026-10-26T20:30:00Z" });
    expect(html).toContain('class="status-dot status-done"');
    expect(html).toContain('<time class="row-done-at muted" dateTime="2026-10-26T20:30:00Z" title="Завершена">27.10.2026 01:30</time>');
  });
});
