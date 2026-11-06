import { Button, Disclosure, ToggleButton, ToggleButtonGroup, Tooltip } from "@heroui/react";
import { Maximize2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { CodeListing, Markdown } from "../markdown";
import { sourceLanguage, textFormat } from "./file-format";
import "./preview.css";

/**
 * Above this many characters a listing is shown unhighlighted: Shiki's
 * JavaScript engine tokenizes on the main thread and React renders one span
 * per token, so a large file would freeze the page.
 */
export const HIGHLIGHT_LIMIT = 100_000;

const PREVIEW_CSP = "default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'; font-src data:; script-src 'unsafe-inline'; base-uri 'none'; form-action 'none';";
const PREVIEW_RESIZE_MESSAGE_TYPE = "talgent:artifact-preview:resize";
const PREVIEW_RESIZE_BRIDGE = `<script>
(() => {
  const messageType = "${PREVIEW_RESIZE_MESSAGE_TYPE}";
  const report = () => {
    const body = document.body;
    const root = document.documentElement;
    const height = Math.max(
      body?.scrollHeight || 0,
      body?.offsetHeight || 0,
      root?.scrollHeight || 0,
      root?.offsetHeight || 0
    );
    if (height > 0) {
      window.parent.postMessage({ type: messageType, height: Math.ceil(height) }, "*");
    }
  };
  window.addEventListener("load", report);
  document.addEventListener("DOMContentLoaded", report);
  try {
    new ResizeObserver(report).observe(document.documentElement);
  } catch {
    window.setInterval(report, 1000);
  }
  window.setTimeout(report, 0);
})();
</script>`;

/**
 * Text content shown by its path's extension. `toolbar` is the reading area's
 * context-action slot, where a Markdown document puts its Rendered/Source switch.
 */
export function TextPreview({ content, path, toolbar = null }: { content: string; path?: string; toolbar?: HTMLElement | null }) {
  const format = textFormat(path);
  switch (format.kind) {
    case "markdown": return <MarkdownDocument content={content} toolbar={toolbar} />;
    case "html": return <HtmlPreview content={content} />;
    case "svg": return <SvgPreview content={content} name={path} />;
    case "diff": return <DiffView content={content} />;
    default: return <CodeView code={content} language={sourceLanguage(format)} />;
  }
}

/** Source in the format's highlighting, as a partial read or a refused render shows it. */
export function SourcePreview({ content, path, startLine }: { content: string; path?: string; startLine?: number }) {
  return <CodeView code={content} language={sourceLanguage(textFormat(path))} startLine={startLine} />;
}

/** Numbered, highlighted source; too long to highlight, it falls back to plain text. */
export function CodeView({ code, language, startLine = 1 }: { code: string; language: string; startLine?: number }) {
  if (code.length > HIGHLIGHT_LIMIT) return <pre className="pv-plain" tabIndex={0} aria-label="Source">{code}</pre>;
  return <CodeListing code={code} language={language} startLine={startLine} />;
}

type MarkdownMode = "rendered" | "source";

// A previewed Markdown file reads as a document, not a card.
function MarkdownDocument({ content, toolbar }: { content: string; toolbar: HTMLElement | null }) {
  const [mode, setMode] = useState<MarkdownMode>("rendered");
  const toggle = (
    <ToggleButtonGroup aria-label="Markdown view" size="sm" selectionMode="single" disallowEmptySelection
      selectedKeys={[mode]} onSelectionChange={keys => { const [next] = keys; if (next) setMode(next as MarkdownMode); }}>
      <ToggleButton id="rendered">Rendered</ToggleButton>
      <ToggleButton id="source"><ToggleButtonGroup.Separator />Source</ToggleButton>
    </ToggleButtonGroup>
  );
  return <>
    {toolbar ? createPortal(toggle, toolbar) : <div className="pv-inline-tools">{toggle}</div>}
    {mode === "rendered"
      ? <div className="pv-markdown-document"><Markdown>{content}</Markdown></div>
      : <CodeView code={content} language="markdown" />}
  </>;
}

/** An SVG shows as an image at its own proportions: as an image it runs no script and loads nothing. */
function SvgPreview({ content, name }: { content: string; name?: string }) {
  const src = useMemo(() => `data:image/svg+xml;charset=utf-8,${encodeURIComponent(content)}`, [content]);
  return <div className="pv-svg">
    <img src={src} alt={name ?? "SVG image"} />
    <Disclosure>
      <Disclosure.Heading>
        <Button slot="trigger" size="sm" variant="ghost">View source<Disclosure.Indicator /></Button>
      </Disclosure.Heading>
      <Disclosure.Content><Disclosure.Body><CodeView code={content} language="xml" /></Disclosure.Body></Disclosure.Content>
    </Disclosure>
  </div>;
}

// The frame is sandboxed without same-origin, so only its own bridge script
// can report the document height.
function HtmlPreview({ content }: { content: string }) {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const [previewHeight, setPreviewHeight] = useState<number | null>(null);

  useEffect(() => {
    const onMessage = (event: MessageEvent) => {
      if (event.source !== iframeRef.current?.contentWindow) return;
      const data = event.data as { type?: unknown; height?: unknown } | null;
      if (!data || typeof data !== "object" || data.type !== PREVIEW_RESIZE_MESSAGE_TYPE) return;
      const height = Number(data.height);
      if (Number.isFinite(height) && height > 0) setPreviewHeight(Math.ceil(height));
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  return (
    <div className="pv-html">
      <iframe
        allow="fullscreen"
        allowFullScreen
        ref={iframeRef}
        referrerPolicy="no-referrer"
        sandbox="allow-scripts"
        srcDoc={previewDocument(content)}
        style={previewHeight === null ? undefined : { height: previewHeight }}
        title="HTML preview"
      />
      <Tooltip>
        <Button isIconOnly size="sm" variant="secondary" aria-label="Open HTML preview fullscreen" onPress={() => void iframeRef.current?.requestFullscreen?.()}>
          <Maximize2 size={16} />
        </Button>
        <Tooltip.Content>Open fullscreen</Tooltip.Content>
      </Tooltip>
    </div>
  );
}

/** Unified diff lines, added and removed lines marked. */
export function DiffView({ content }: { content: string }) {
  return (
    <pre className="pv-diff">
      {content.split("\n").map((line, index) => (
        <span data-diff-line={diffLineType(line)} key={index}>{line || " "}</span>
      ))}
    </pre>
  );
}

function diffLineType(line: string): "added" | "removed" | "context" {
  if (line.startsWith("+") && !line.startsWith("+++")) {
    return "added";
  }
  if (line.startsWith("-") && !line.startsWith("---")) {
    return "removed";
  }
  return "context";
}

function previewDocument(content: string): string {
  const meta = `<meta http-equiv="Content-Security-Policy" content="${escapeAttribute(PREVIEW_CSP)}">`;

  return `<!doctype html><html><head>${meta}${PREVIEW_RESIZE_BRIDGE}</head><body>${content}</body></html>`;
}

function escapeAttribute(value: string): string {
  return value.replace(/&/g, "&amp;").replace(/"/g, "&quot;");
}
