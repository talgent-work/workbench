import { code } from "@streamdown/code";
import type { CSSProperties } from "react";
import type { DiagramPlugin as OfficialMermaidPlugin } from "@streamdown/mermaid";
import { Block, defaultRehypePlugins, defaultRemarkPlugins, defaultUrlTransform, Streamdown, type Components, type DiagramPlugin } from "streamdown";

// Streamdown's official Mermaid plugin imports mermaid eagerly; this wrapper
// keeps that (large) module out of the first load until a diagram renders.
// Each instance carries the configuration Streamdown gives it.
type MermaidConfig = Parameters<OfficialMermaidPlugin["getMermaid"]>[0];
const mermaid: DiagramPlugin = {
  name: "mermaid", type: "diagram", language: "mermaid",
  getMermaid(initial) {
    let config = initial as MermaidConfig;
    return {
      initialize(next) { config = next as MermaidConfig; },
      async render(id, source) {
        const { mermaid: plugin } = await import("@streamdown/mermaid");
        return plugin.getMermaid(config).render(id, source);
      },
    };
  },
};

const plugins = { code, mermaid };
const controls = { code: { copy: true, download: false }, table: false, mermaid: { copy: true, download: false, fullscreen: true, panZoom: false } };
const components: Components = {
  strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
  // HAST supplies defaultChecked; controlled state also follows edits to a task.
  input: ({ checked, defaultChecked, disabled, type }) => (
    <input type={type} checked={checked ?? defaultChecked ?? false} disabled={disabled} readOnly />
  ),
};

// Inline Markdown reads within a line of surrounding content, such as a cited
// statement: paragraphs do not break the line, and without links it can sit
// inside a control. It renders one block without Streamdown's block root, so
// it is phrasing content that inherits the surrounding type.
// Streamdown's own code and link components bring block behaviour (copy
// controls, link-safety modal), so inline mirrors only their markup and hooks;
// the shared app-markdown styles then apply alike.
const inlineComponents: Components = {
  ...components,
  p: ({ children }) => <>{children}{" "}</>,
  code: ({ children }) => <code className="rounded px-1.5 py-0.5 font-mono" data-streamdown="inline-code">{children}</code>,
  a: ({ children, href }) => <a className="font-medium underline" data-streamdown="link" href={href} target="_blank" rel="noopener noreferrer">{children}</a>,
};
const inlineTextComponents: Components = { ...inlineComponents, a: ({ children }) => <>{children}</> };
// A preview reads as one run of text, such as a clamped summary: blocks become
// phrasing, so headings and lists keep their words without their structure.
const previewComponents: Components = {
  ...inlineTextComponents,
  h1: ({ children }) => <>{children}{" "}</>, h2: ({ children }) => <>{children}{" "}</>, h3: ({ children }) => <>{children}{" "}</>,
  h4: ({ children }) => <>{children}{" "}</>, h5: ({ children }) => <>{children}{" "}</>, h6: ({ children }) => <>{children}{" "}</>,
  ul: ({ children }) => <>{children}</>, ol: ({ children }) => <>{children}</>, li: ({ children }) => <>{children}{" "}</>,
  blockquote: ({ children }) => <>{children}</>, pre: ({ children }) => <>{children}{" "}</>, hr: () => null,
  table: () => null, img: () => null,
};

// The same sanitization and URL policy Streamdown applies to its blocks.
const remarkPlugins = Object.values(defaultRemarkPlugins);
const rehypePlugins = Object.values(defaultRehypePlugins);

// Block is Streamdown's per-block renderer and its props are streaming
// internals: recheck them, and the markdown tests, when upgrading Streamdown.
export function InlineMarkdown({ children, className = "", links = true }: { children: string; className?: string; links?: boolean }) {
  return (
    <span className={`app-markdown-inline ${className}`} dir="auto">
      <Block
        content={children}
        index={0}
        isIncomplete={false}
        shouldParseIncompleteMarkdown={false}
        shouldNormalizeHtmlIndentation={false}
        remarkPlugins={remarkPlugins}
        rehypePlugins={rehypePlugins}
        urlTransform={defaultUrlTransform}
        components={links ? inlineComponents : inlineTextComponents}
      />
    </span>
  );
}

/** Markdown flattened to one run of phrasing content, for clamped previews. */
export function PreviewMarkdown({ children, className = "" }: { children: string; className?: string }) {
  return (
    <Streamdown className={`app-markdown ${className}`} mode="static" components={previewComponents} controls={false} lineNumbers={false} linkSafety={{ enabled: false }} dir="auto">
      {children}
    </Streamdown>
  );
}

/** One renderer for stored documents and incrementally arriving Agent text. */
export function Markdown({ children, className = "", streaming = false, showControls = true }: {
  children: string;
  className?: string;
  streaming?: boolean;
  showControls?: boolean;
}) {
  return (
    <Streamdown
      className={`app-markdown ${className}`}
      mode={streaming ? "streaming" : "static"}
      parseIncompleteMarkdown={streaming}
      plugins={plugins}
      components={components}
      controls={showControls ? controls : false}
      lineNumbers={false}
      linkSafety={{ enabled: false }}
      // A table grows to its full height in the reading flow; Streamdown's
      // default 300px cap would nest a second scroller inside it.
      tableMaxHeight={0}
      dir="auto"
    >
      {children}
    </Streamdown>
  );
}

/** The longest backtick run decides the fence, so source never closes it early. */
function fenced(source: string, info: string): string {
  let longest = 0;
  for (const match of source.matchAll(/`+/g)) longest = Math.max(longest, match[0].length);
  const fence = "`".repeat(Math.max(3, longest + 1));
  return `${fence}${info}\n${source}${source.endsWith("\n") ? "" : "\n"}${fence}`;
}

/**
 * A source listing in the same highlighting as Markdown code blocks, numbered
 * from `startLine` by logical line. `language` is a Shiki language or "text".
 * The listing is the whole reading, so it grows to its full height; 0 lifts
 * Streamdown's default 400px cap and its inner scroll.
 */
export function CodeListing({ code: source, language, startLine = 1 }: { code: string; language: string; startLine?: number }) {
  const lastLine = startLine + source.split("\n").length - 1;
  return (
    <div className="app-code-listing" style={{ "--line-gutter": `${String(lastLine).length}ch` } as CSSProperties}>
      <Streamdown className="app-markdown" mode="static" plugins={{ code }} components={components}
        controls={{ code: { copy: true, download: false } }} lineNumbers linkSafety={{ enabled: false }}
        codeBlockMaxHeight={0}>
        {fenced(source, `${language} startLine=${startLine}`)}
      </Streamdown>
    </div>
  );
}
