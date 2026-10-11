// Markdown описаний и комментариев (§3.3, §3.6) с санитайзингом: сырой HTML запрещён (§10.3).
// Две линии защиты: marked не пропускает HTML из текста (экранирует его как обычный текст),
// а DOMPurify вычищает итоговую разметку — на случай ошибки парсера или опасной ссылки
// (javascript:, обработчики on*).
import DOMPurify, { type DOMPurify as Purifier } from "dompurify";
import { Marked } from "marked";

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

/** Картинка со своего сервера: путь от корня, не «//host». */
function sameOrigin(href: string): boolean {
  return href.startsWith("/") && !href.startsWith("//");
}

const marked = new Marked({
  gfm: true,
  breaks: true,
  async: false,
  renderer: {
    // Сырой HTML (блочный и строчный) показываем как текст, а не вставляем в страницу.
    html: ({ text }) => escapeHtml(text),
    // Внешние картинки не грузим сами (иначе это пиксель-трекер для каждого, кто открыл
    // задачу) — показываем ссылкой. Свои (вложения) — картинкой.
    image: ({ href, text }) =>
      sameOrigin(href)
        ? `<img src="${escapeHtml(href)}" alt="${escapeHtml(text)}">`
        : `<a href="${escapeHtml(href)}">${escapeHtml(text || href)}</a>`,
  },
});

let instance: Purifier | null = null;

/** Свой экземпляр DOMPurify: хуки ниже не действуют на другие вызовы DOMPurify в приложении. */
function purifier(): Purifier {
  if (!instance) {
    const p = DOMPurify(window);
    p.addHook("afterSanitizeAttributes", (node) => {
      // Ссылки — в новой вкладке и без доступа к window.opener.
      if (node.tagName === "A" && node.hasAttribute("href")) {
        node.setAttribute("target", "_blank");
        node.setAttribute("rel", "noopener noreferrer");
      }
      // Из полей ввода остаются только флажки списков задач GFM, и те только для показа.
      if (node.tagName === "INPUT") {
        if (node.getAttribute("type") !== "checkbox") node.remove();
        else node.setAttribute("disabled", "");
      }
    });
    instance = p;
  }
  return instance;
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
    FORBID_TAGS: ["style", "form", "button", "textarea", "select", "iframe", "object", "embed"],
    FORBID_ATTR: ["style", "name", "value"],
    ADD_ATTR: ["target"],
  });
}
