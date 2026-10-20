import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { StatusDot } from "./StatusDot";

describe("StatusDot", () => {
  it("класс по статусу", () => {
    expect(renderToStaticMarkup(<StatusDot status="in_progress" />)).toBe(
      '<span class="status-dot status-in_progress" aria-hidden="true"></span>',
    );
  });

  it("пустой или неизвестный статус — круг без модификатора, а не пустое место", () => {
    for (const status of [undefined, null, ""])
      expect(renderToStaticMarkup(<StatusDot status={status} />)).toBe('<span class="status-dot" aria-hidden="true"></span>');
  });
});
