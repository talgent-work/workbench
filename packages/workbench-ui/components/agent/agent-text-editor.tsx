import type { FormEvent } from "react";
import { Alert, Button, FieldError, Label, TextArea, TextField } from "@heroui/react";

/** Edits one text an Agent's Owners write, such as its Soul, which its Works
 * take up at their next start. The caller owns the draft, so a draft survives
 * the editor leaving the screen, and the save: `saving` while it runs and
 * `error` when it failed, which `onDiscard` clears. A `document` is a file's
 * Markdown, edited in a monospace face with its length shown against `max`.
 * A `height` sets the box's height whatever its face, so editors set side by
 * side match; without one it is `rows` lines tall. */
export function AgentTextEditor({ label, draft, saved, max, placeholder, rows = 5, height, document = false, saving, error, onChange, onSave, onDiscard }: {
  label: string; draft: string; saved: string; max: number; placeholder: string; rows?: number; height?: string; document?: boolean;
  saving: boolean; error: string; onChange: (draft: string) => void; onSave: (text: string) => void; onDiscard: () => void;
}) {
  const text = draft.trim();
  const length = Array.from(text).length;
  const problem = length > max ? `Use at most ${max.toLocaleString("en-US")} characters.` : "";
  const dirty = text !== saved;
  const save = (event: FormEvent) => {
    event.preventDefault();
    if (!dirty || problem || saving) return;
    onSave(text);
  };
  return (
    <form onSubmit={save} aria-label={label} className="grid gap-4">
      <TextField value={draft} onChange={onChange} isInvalid={Boolean(problem)} isDisabled={saving}>
        <Label className="sr-only">{label}</Label>
        <TextArea rows={rows} placeholder={placeholder} spellCheck={!document} style={height ? { height } : undefined} className={document ? "font-mono text-[13px] leading-relaxed" : undefined} />
        <FieldError>{problem}</FieldError>
      </TextField>
      {error && (
        <Alert status="danger">
          <Alert.Indicator />
          <Alert.Content><Alert.Title>{error}</Alert.Title></Alert.Content>
        </Alert>
      )}
      <div className="flex items-center justify-end gap-2">
        {document && <p className={`mr-auto text-[13px] tabular-nums ${problem ? "text-danger" : "text-muted"}`}>{length.toLocaleString("en-US")} / {max.toLocaleString("en-US")}</p>}
        <Button size="sm" variant="ghost" isDisabled={!dirty || saving} onPress={onDiscard}>Discard</Button>
        <Button size="sm" type="submit" isDisabled={!dirty || Boolean(problem)} isPending={saving}>{saving ? "Saving" : "Save"}</Button>
      </div>
    </form>
  );
}
