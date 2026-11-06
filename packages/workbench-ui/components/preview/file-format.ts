import { code } from "@streamdown/code";

/**
 * How text content is shown, decided by the file extension alone:
 * content is never sniffed, so a `.log` holding `<div>` stays plain text.
 */
export type TextFormat =
  | { kind: "markdown" }
  | { kind: "html" }
  | { kind: "svg" }
  | { kind: "diff" }
  | { kind: "code"; language: string }
  | { kind: "text" };

// Extensions whose Shiki language id differs from the extension itself; any
// other extension that is a Shiki language id or alias highlights as that.
const LANGUAGE_BY_EXT: Record<string, string> = {
  ts: "typescript", mts: "typescript", cts: "typescript", js: "javascript", mjs: "javascript", cjs: "javascript",
  py: "python", pyi: "python", rs: "rust", rb: "ruby", kt: "kotlin", kts: "kotlin", cs: "csharp",
  h: "c", hpp: "cpp", cc: "cpp", cxx: "cpp", hh: "cpp", sh: "bash", zsh: "bash", yml: "yaml",
  proto: "proto", tf: "terraform", ex: "elixir", exs: "elixir", erl: "erlang", hs: "haskell",
  ml: "ocaml", fs: "fsharp", pl: "perl", ps1: "powershell", psm1: "powershell", bat: "bat", cmd: "bat",
  sol: "solidity", m: "objective-c", mm: "objective-cpp", vue: "vue", svelte: "svelte", astro: "astro",
  gql: "graphql", jsonc: "jsonc", json5: "json5", ndjson: "json", jsonl: "json", ipynb: "json",
  xml: "xml", xsd: "xml", xsl: "xml", plist: "xml", ini: "ini", cfg: "ini", conf: "ini", env: "dotenv",
  dockerfile: "docker", mk: "make", cmake: "cmake", gradle: "groovy", sbt: "scala", sql: "sql",
};
// File names without an extension that still name a language.
const LANGUAGE_BY_NAME: Record<string, string> = {
  dockerfile: "docker", containerfile: "docker", makefile: "make", gnumakefile: "make",
  jenkinsfile: "groovy", gemfile: "ruby", rakefile: "ruby", ".bashrc": "bash", ".zshrc": "bash",
};
// Shiki knows these, but they read as the text they are.
const PLAIN_EXTENSIONS = new Set(["txt", "text", "log", "csv", "tsv", "out", "err"]);

export function fileName(path?: string): string {
  return path?.split(/[?#]/, 1)[0]?.split("/").pop() ?? "";
}

export function fileExtension(path?: string): string | undefined {
  return fileName(path).match(/.\.([^.]+)$/)?.[1]?.toLowerCase();
}

/** The Shiki language for a path, or undefined when it is not code. */
export function codeLanguage(path?: string): string | undefined {
  const name = fileName(path).toLowerCase();
  const ext = fileExtension(path);
  if (!ext) return LANGUAGE_BY_NAME[name];
  if (PLAIN_EXTENSIONS.has(ext)) return undefined;
  const language = LANGUAGE_BY_EXT[ext] ?? ext;
  return code.supportsLanguage(language as Parameters<typeof code.supportsLanguage>[0]) ? language : undefined;
}

export function textFormat(path?: string): TextFormat {
  switch (fileExtension(path)) {
    case "md": case "markdown": return { kind: "markdown" };
    case "html": case "htm": return { kind: "html" };
    case "svg": return { kind: "svg" };
    case "diff": case "patch": return { kind: "diff" };
  }
  const language = codeLanguage(path);
  return language ? { kind: "code", language } : { kind: "text" };
}

/** The Shiki language a format's source reads in, for source views. */
export function sourceLanguage(format: TextFormat): string {
  switch (format.kind) {
    case "markdown": return "markdown";
    case "html": return "html";
    case "svg": return "xml";
    case "diff": return "diff";
    case "code": return format.language;
    case "text": return "text";
  }
}
