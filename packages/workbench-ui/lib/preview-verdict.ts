import { WorkFilePreviewDisplay as Display, WorkFilePreviewMode as Mode, WorkFilePreviewRefusal as Refusal } from "@talgent/workbench-contracts/agent/public/v1/work_file_pb";
import type { PreviewVerdict } from "../components/preview/preview-verdict";

// Storage's file preview as a context relays it. Agent's preview contract
// numbers its values as Storage does, and a context relaying it keeps that
// numbering, so one reading of Agent's enums serves every relay.
type RelayedPreview = { mode: number; display: number; refusal: number; limitBytes: bigint; accessUrl: string; rendering: boolean };

const previewModes: Partial<Record<number, PreviewVerdict["mode"]>> = {
  [Mode.REFUSED]: "refused", [Mode.PREVIEW_COPY]: "preview_copy", [Mode.RENDER]: "render", [Mode.DIRECT]: "direct", [Mode.TRUNCATED]: "truncated",
};
const previewDisplays: Partial<Record<number, PreviewVerdict["display"]>> = { [Display.TEXT]: "text", [Display.PDF]: "pdf", [Display.IMAGE]: "image" };
const previewRefusals: Partial<Record<number, NonNullable<PreviewVerdict["refusal"]>>> = {
  [Refusal.UNSUPPORTED]: "unsupported", [Refusal.STRUCTURE_MISMATCH]: "structure_mismatch", [Refusal.ENCRYPTED]: "encrypted", [Refusal.TOO_LARGE]: "too_large",
  [Refusal.CORRUPT]: "corrupt", [Refusal.CONVERTER_REFUSED]: "converter_refused", [Refusal.RENDERING_TOO_LARGE]: "rendering_too_large",
};

// An unknown mode from a newer server reads as unsupported: nothing unknown is shown.
export function previewVerdict(preview: RelayedPreview | undefined): PreviewVerdict {
  const mode = preview && previewModes[preview.mode];
  if (!preview || !mode) return { mode: "refused", display: "none", refusal: "unsupported", limitBytes: 0, rendering: false };
  return {
    mode, display: previewDisplays[preview.display] ?? "none", refusal: previewRefusals[preview.refusal],
    limitBytes: Number(preview.limitBytes), url: preview.accessUrl || undefined, rendering: preview.rendering,
  };
}
