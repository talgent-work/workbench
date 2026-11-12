import { useState, type FormEvent } from "react";
import { Alert, Button, Card, FieldError, Label, TextArea, TextField } from "@heroui/react";
import { MAX_SOUL } from "../../lib/agent-soul-service";

/** Writes an Agent's Soul, the persona and working style its Works take up at
 * their next start. The caller owns the save: `saving` while it runs and
 * `error` when it failed, which `onDiscard` clears. */
export function AgentSoulEditor({ saved, placeholder, saving, error, onSave, onDiscard }: {
  saved: string; placeholder: string; saving: boolean; error: string;
  onSave: (soul: string) => void; onDiscard: () => void;
}) {
  const [draft, setDraft] = useState(saved);
  const soul = draft.trim();
  const problem = Array.from(soul).length > MAX_SOUL ? `Use at most ${MAX_SOUL} characters.` : "";
  const dirty = soul !== saved;
  const save = (event: FormEvent) => {
    event.preventDefault();
    if (!dirty || problem || saving) return;
    onSave(soul);
  };
  return (
    <Card className="mk-group">
      <form onSubmit={save} aria-label="Soul">
        <Card.Content className="grid gap-4 p-5">
          <TextField value={draft} onChange={setDraft} isInvalid={Boolean(problem)} isDisabled={saving}>
            <Label className="sr-only">Soul</Label>
            <TextArea rows={5} placeholder={placeholder} />
            <FieldError>{problem}</FieldError>
          </TextField>
          {error && (
            <Alert status="danger">
              <Alert.Indicator />
              <Alert.Content><Alert.Title>{error}</Alert.Title></Alert.Content>
            </Alert>
          )}
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" isDisabled={!dirty || saving} onPress={() => { onDiscard(); setDraft(saved); }}>Discard</Button>
            <Button size="sm" type="submit" isDisabled={!dirty || Boolean(problem)} isPending={saving}>{saving ? "Saving" : "Save"}</Button>
          </div>
        </Card.Content>
      </form>
    </Card>
  );
}
