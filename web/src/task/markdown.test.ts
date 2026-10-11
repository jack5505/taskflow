// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { renderMarkdown, sanitizeHtml } from "./markdown";

/** Разобрать HTML так, как его вставит браузер. */
function dom(html: string): HTMLElement {
  const el = document.createElement("div");
  el.innerHTML = html;
  return el;
}

/** Атрибуты-обработчики on* на любых элементах. */
function handlers(el: HTMLElement): string[] {
  return [...el.querySelectorAll("*")].flatMap((n) =>
    [...n.attributes].filter((a) => a.name.toLowerCase().startsWith("on")).map((a) => `${n.tagName}.${a.name}`),
  );
}

describe("renderMarkdown", () => {
  it("обычная разметка", () => {
    const html = renderMarkdown("# Заголовок\n\n**жирный** и `код`\n\n- раз\n- два");
    expect(html).toContain("<h1>Заголовок</h1>");
    expect(html).toContain("<strong>жирный</strong>");
    expect(html).toContain("<code>код</code>");
    expect(html).toContain("<li>раз</li>");
  });

  it("пустое — пустая строка", () => {
    expect(renderMarkdown("")).toBe("");
    expect(renderMarkdown(null)).toBe("");
  });

  it("<script> не попадает в разметку", () => {
    const el = dom(renderMarkdown("до <script>alert(1)</script> после\n\n<script>alert(2)</script>"));
    expect(el.querySelector("script")).toBeNull();
    expect(el.textContent).toContain("до");
    expect(el.textContent).toContain("после");
  });

  it("сырой HTML показывается текстом: ни тегов, ни обработчиков", () => {
    const el = dom(renderMarkdown('<img src="x" onerror="alert(1)">\n\nтекст <b onclick="x()">b</b>'));
    expect(el.querySelector("img")).toBeNull();
    expect(el.querySelector("b")).toBeNull();
    expect(handlers(el)).toEqual([]);
    expect(el.textContent).toContain('<img src="x" onerror="alert(1)">');
  });

  it("картинка: своя — картинкой, внешняя — ссылкой; без обработчиков", () => {
    const el = dom(renderMarkdown('![a](x" onerror="alert(1)) ![b](https://example.com/b.png) ![c](/files/c.png)'));
    expect(handlers(el)).toEqual([]);
    expect([...el.querySelectorAll("img")].map((i) => i.getAttribute("src"))).toEqual(["/files/c.png"]);
    expect(el.querySelector('a[href="https://example.com/b.png"]')?.textContent).toBe("b");
  });

  it("адреса, которые браузер считает чужим сервером, — ссылкой", () => {
    const el = dom(renderMarkdown("![a](//evil.example/p.png) ![b](/\\evil.example/p.png) ![c](/%5Cok.png)"));
    expect([...el.querySelectorAll("img")].map((i) => i.getAttribute("src"))).toEqual(["/%5Cok.png"]);
  });

  it("список задач GFM: флажки остаются, только для показа", () => {
    const boxes = [...dom(renderMarkdown("- [x] сделано\n- [ ] осталось")).querySelectorAll("input")];
    expect(boxes.map((b) => [b.type, b.checked, b.disabled])).toEqual([
      ["checkbox", true, true],
      ["checkbox", false, true],
    ]);
  });

  it("ссылка javascript: обезврежена, обычная открывается в новой вкладке", () => {
    const bad = dom(renderMarkdown("[жми](javascript:alert(1))"));
    for (const a of bad.querySelectorAll("a")) expect(a.getAttribute("href") ?? "").not.toMatch(/^javascript:/i);
    const good = dom(renderMarkdown("[сайт](https://example.com)")).querySelector("a")!;
    expect(good.getAttribute("href")).toBe("https://example.com");
    expect(good.getAttribute("target")).toBe("_blank");
    expect(good.getAttribute("rel")).toBe("noopener noreferrer");
  });
});

describe("sanitizeHtml", () => {
  it("вырезает script, onerror, javascript: и style", () => {
    const el = dom(
      sanitizeHtml(
        '<p style="color:red" onclick="x()">т</p><script>alert(1)</script><img src="x" onerror="alert(1)">' +
          '<a href="javascript:alert(1)">a</a><iframe src="https://e.com"></iframe>',
      ),
    );
    expect(el.querySelector("script, iframe")).toBeNull();
    expect(handlers(el)).toEqual([]);
    expect(el.querySelector("p")?.hasAttribute("style")).toBe(false);
    expect(el.querySelector("a")?.getAttribute("href") ?? "").not.toMatch(/^javascript:/i);
    expect(el.querySelector("img")).not.toBeNull(); // картинка остаётся, без обработчика
  });

  it("поля ввода, кроме флажков, вырезаются", () => {
    const el = dom(sanitizeHtml('<input type="text" value="x"><input type="checkbox" checked>'));
    expect([...el.querySelectorAll("input")].map((i) => [i.type, i.disabled])).toEqual([["checkbox", true]]);
  });
});
