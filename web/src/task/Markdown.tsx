import { useId, useMemo, useState } from "react";
import { renderMarkdown } from "./markdown";
import { Tabs, tabPanel } from "../ui/Tabs";

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

const MD_TABS = [
  { id: "text", label: "Текст" },
  { id: "preview", label: "Предпросмотр" },
] as const;

/** Markdown-редактор с предпросмотром (§7.5): вкладки «Текст» и «Предпросмотр». */
export function MarkdownEditor(p: EditorProps) {
  const [preview, setPreview] = useState(false);
  const id = useId();
  const mode = preview ? "preview" : "text";
  return (
    <div className="md-editor">
      <Tabs label={p.label} idPrefix={id} tabs={MD_TABS} value={mode} onChange={(m) => setPreview(m === "preview")}>
        <span className="muted hint">Markdown</span>
      </Tabs>
      <div {...tabPanel(id, mode)}>
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
    </div>
  );
}
