import { useMemo, useState } from "react";
import { renderMarkdown } from "./markdown";

/** Показ Markdown: разметка уже очищена санитайзером (markdown.ts). */
export function MarkdownView({ source, className = "markdown" }: { source: string | undefined; className?: string }) {
  const html = useMemo(() => renderMarkdown(source), [source]);
  return <div className={className} dangerouslySetInnerHTML={{ __html: html }} />;
}

interface EditorProps {
  value: string;
  onChange: (v: string) => void;
  maxLength: number;
  rows?: number;
  label: string;
  autoFocus?: boolean;
  disabled?: boolean;
}

/** Markdown-редактор с предпросмотром (§7.5): вкладки «Текст» и «Предпросмотр». */
export function MarkdownEditor(p: EditorProps) {
  const [preview, setPreview] = useState(false);
  return (
    <div className="md-editor">
      <div className="tabs" role="tablist" aria-label={p.label}>
        <button type="button" role="tab" aria-selected={!preview} className={preview ? "tab" : "tab active"} onClick={() => setPreview(false)}>
          Текст
        </button>
        <button type="button" role="tab" aria-selected={preview} className={preview ? "tab active" : "tab"} onClick={() => setPreview(true)}>
          Предпросмотр
        </button>
        <span className="muted hint">Markdown</span>
      </div>
      {preview ? (
        p.value.trim() ? (
          <MarkdownView source={p.value} className="markdown md-preview" />
        ) : (
          <p className="muted md-preview">Нечего показать</p>
        )
      ) : (
        <textarea
          aria-label={p.label}
          autoFocus={p.autoFocus}
          rows={p.rows ?? 8}
          maxLength={p.maxLength}
          value={p.value}
          disabled={p.disabled}
          onChange={(e) => p.onChange(e.target.value)}
        />
      )}
    </div>
  );
}
