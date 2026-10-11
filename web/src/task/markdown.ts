// Markdown описаний и комментариев (§3.3, §3.6) с санитайзингом: сырой HTML запрещён (§10.3).
// Две линии защиты: marked не пропускает HTML из текста (экранирует его как обычный текст),
// а DOMPurify вычищает итоговую разметку — на случай ошибки парсера или опасной ссылки
// (javascript:, обработчики on*).
import DOMPurify from "dompurify";
import { Marked } from "marked";

const marked = new Marked({
  gfm: true,
  breaks: true,
  async: false,
  renderer: {
    // Сырой HTML (блочный и строчный) показываем как текст, а не вставляем в страницу.
    html: ({ text }) => escapeHtml(text),
  },
});

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

let hooked = false;

function purifier() {
  if (!hooked) {
    // Внешние ссылки — в новой вкладке и без доступа к window.opener.
    DOMPurify.addHook("afterSanitizeAttributes", (node) => {
      if (node.tagName === "A" && node.hasAttribute("href")) {
        node.setAttribute("target", "_blank");
        node.setAttribute("rel", "noopener noreferrer");
      }
    });
    hooked = true;
  }
  return DOMPurify;
}

/** Markdown → безопасный HTML для dangerouslySetInnerHTML. */
export function renderMarkdown(src: string | null | undefined): string {
  if (!src) return "";
  return sanitizeHtml(marked.parse(src) as string);
}

/** Вторая линия: из HTML остаются только безопасные теги и атрибуты. */
export function sanitizeHtml(html: string): string {
  return purifier().sanitize(html, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ["style", "form", "input", "button", "textarea", "select", "iframe", "object", "embed"],
    FORBID_ATTR: ["style"],
    ADD_ATTR: ["target"],
  });
}
