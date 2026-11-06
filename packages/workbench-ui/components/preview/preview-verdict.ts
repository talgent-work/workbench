/** How Storage shows a file; the reading area follows it and nothing else. */
export type PreviewVerdict = {
  mode: "refused" | "preview_copy" | "render" | "direct" | "truncated";
  display: "text" | "pdf" | "image" | "none";
  refusal?: "unsupported" | "structure_mismatch" | "encrypted" | "too_large" | "corrupt" | "converter_refused" | "rendering_too_large";
  /** Shown length of a truncated file, or the limit a too large file exceeds. */
  limitBytes: number;
  /** One inline read of the shown bytes. */
  url?: string;
  /** With mode "render": a Preview Copy of the content is queued or being made. */
  rendering: boolean;
};

export function formatBytes(bytes: number): string {
  const mib = bytes / (1 << 20);
  if (mib >= 1) return `${Number.isInteger(mib) ? mib : mib.toFixed(1)} MiB`;
  if (bytes >= 1024) return `${Math.round(bytes / 1024)} KiB`;
  return `${bytes} bytes`;
}

export function refusalText(verdict: PreviewVerdict): string {
  switch (verdict.refusal) {
    case "structure_mismatch": return "The content doesn't match the file type.";
    case "encrypted": return "This file is encrypted.";
    case "too_large": return `This file is over ${formatBytes(verdict.limitBytes)}, too large to preview.`;
    case "corrupt": return "This file is damaged and can't be previewed.";
    case "converter_refused": return "This file can't be converted for preview.";
    case "rendering_too_large": return "The preview of this file came out too large to show.";
    default: return "Preview isn't available for this kind of file.";
  }
}

/** Cuts bytes at the last whole UTF-8 character, so a truncated read decodes cleanly. */
export function decodeUtf8Prefix(bytes: Uint8Array): string {
  let end = bytes.length;
  for (let back = 1; back <= 3 && back <= bytes.length; back++) {
    const byte = bytes[bytes.length - back];
    if ((byte & 0xc0) === 0x80) continue;
    const length = byte >= 0xf0 ? 4 : byte >= 0xe0 ? 3 : byte >= 0xc0 ? 2 : 1;
    if (length > back) end = bytes.length - back;
    break;
  }
  return new TextDecoder().decode(bytes.subarray(0, end));
}
