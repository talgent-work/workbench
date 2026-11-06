import { Alert } from "@heroui/react";
import type { ToolOutputPreview as Preview } from "@talgent-work/workbench-contracts/dispatcher/public/v1/unified_message_pb";
import { DiffView, SourcePreview, TextPreview } from "../preview/text-preview";

/**
 * What one Read, Write or Edit call actually read, wrote or changed, from the
 * call's own result. The path only picks the rendering; the file itself is
 * never read, and nothing is rebuilt from the input.
 */
export function ToolOutputPreview({ preview, toolbar }: { preview: Preview; toolbar: HTMLElement | null }) {
  const notes: string[] = [];
  if (preview.truncated) notes.push(`Showing the first ${shownLength(preview).toLocaleString()} of ${preview.totalLength.toLocaleString()} characters.`);
  const empty = preview.toolName !== "Edit" && preview.content === "";
  if (preview.toolName === "Read") {
    const last = preview.startLine + preview.numLines - 1;
    const whole = preview.startLine <= 1 && last >= preview.totalLines && !preview.truncatedByTokenCap && !preview.truncated;
    // A partial read names its range as a file preview names its truncation.
    if (!whole && preview.numLines > 0) notes.unshift(`Showing lines ${preview.startLine}–${last} of ${preview.totalLines}.`);
    if (preview.truncatedByTokenCap) notes.push("The read stopped at the token limit.");
    return <section aria-label="Read content">
      <Notes notes={notes} />
      {empty ? <p>Empty file</p> : whole
        ? <TextPreview path={preview.filePath} content={preview.content} toolbar={toolbar} />
        : <SourcePreview path={preview.filePath} content={preview.content} startLine={Math.max(1, preview.startLine)} />}
    </section>;
  }
  if (preview.toolName === "Write") {
    return <section aria-label="Written content">
      <Notes notes={notes} />
      {/* A cut body is never rendered as a whole document. */}
      {empty ? <p>Empty file</p> : preview.truncated
        ? <SourcePreview path={preview.filePath} content={preview.content} />
        : <TextPreview path={preview.filePath} content={preview.content} toolbar={toolbar} />}
    </section>;
  }
  if (preview.toolName !== "Edit") return null;
  return <section aria-label="Edited lines">
    <Notes notes={notes} />
    <DiffView content={preview.hunks.map(hunk => [`@@ -${hunk.oldStart},${hunk.oldLines} +${hunk.newStart},${hunk.newLines} @@`, ...hunk.lines].join("\n")).join("\n")} />
  </section>;
}

// Code points shown, counted as the limit counts them: a patch line and its break.
function shownLength(preview: Preview): number {
  if (preview.toolName !== "Edit") return [...preview.content].length;
  return preview.hunks.reduce((sum, hunk) => sum + hunk.lines.reduce((lines, line) => lines + [...line].length + 1, 0), 0);
}

function Notes({ notes }: { notes: string[] }) {
  if (notes.length === 0) return null;
  return <Alert status="default" className="pv-alert"><Alert.Indicator /><Alert.Content>{notes.map(note => <Alert.Description key={note}>{note}</Alert.Description>)}</Alert.Content></Alert>;
}
