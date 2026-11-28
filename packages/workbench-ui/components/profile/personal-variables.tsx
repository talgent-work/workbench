import { useEffect, useRef, useState } from "react";
import { Button, Card, Tooltip } from "@heroui/react";
import { Eye, EyeOff, Info, Pencil, Plus, Trash2 } from "lucide-react";
import { ConfirmDialog, LoadState } from "../marketplace/marketplace-shared";
import { PersonalVariableEditor } from "./personal-variable-editor";
import { dismiss, notify } from "../../lib/notifications";
import { personalVariableErrorMessage, revealPersonalVariable, useDeletePersonalVariable, usePersonalVariables } from "../../lib/personal-variable-service";

// The list gives names only, so every value is masked to one length.
const MASK = "••••••••";

type Editor = { name: string; value: string; isNew: boolean };
type Revealed = { name: string; value: string };

// Profile › Variables: the person's Personal Variables as rows of name and
// masked value. A value is read only when the person shows or edits it, and
// one at a time; one row is edited at a time, in place.
export function PersonalVariables() {
  const variables = usePersonalVariables();
  return (
    <section className="mk-section" aria-labelledby="pf-variables">
      <div className="flex items-center gap-2">
        <h3 id="pf-variables" className="mk-section-title">Variables</h3>
        <Tooltip delay={200}>
          <Tooltip.Trigger aria-label="About variables" className="rounded-full text-muted"><Info size={15} aria-hidden="true" /></Tooltip.Trigger>
          <Tooltip.Content>Environment variables for every Work your Work Agent does for you, replacing the platform's of the same name. The agent and its tools can read them.</Tooltip.Content>
        </Tooltip>
      </div>
      <LoadState noun="variables" isPending={variables.isPending} isError={variables.isError} hasData={variables.data !== undefined}
        isFetching={variables.isFetching} onRetry={() => void variables.refetch()} />
      {variables.data && <VariableRows names={variables.data} />}
    </section>
  );
}

function VariableRows({ names }: { names: string[] }) {
  const reader = useValueReader();
  const remove = useDeletePersonalVariable();
  const [revealed, setRevealed] = useState<Revealed | null>(null);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState<string | null>(null);
  // Names whose deletion is in flight; each deletion reports its own outcome.
  const [removing, setRemoving] = useState<ReadonlySet<string>>(new Set());
  const container = useRef<HTMLDivElement>(null);
  const focusTarget = useRef("");

  // Closing an editor would drop focus to the page; it returns to the control
  // that opened the editor once that control is back on screen.
  useEffect(() => {
    if (editor || !focusTarget.current) return;
    const target = focusTarget.current;
    focusTarget.current = "";
    [...(container.current?.querySelectorAll<HTMLElement>("[data-focus]") ?? [])].find(element => element.dataset.focus === target)?.focus();
  }, [editor]);

  const toggle = (name: string) => {
    const hiding = revealed?.name === name;
    setRevealed(null);
    if (hiding) reader.cancel();
    else reader.read(name, "show", value => setRevealed({ name, value }));
  };
  const edit = (name: string) => {
    setRevealed(null);
    reader.read(name, "edit", value => setEditor({ name, value, isNew: false }));
  };
  const add = () => {
    setRevealed(null);
    reader.cancel();
    setEditor({ name: "", value: "", isNew: true });
  };
  const close = () => {
    focusTarget.current = editor?.isNew ? "add" : `edit:${editor?.name}`;
    setEditor(null);
  };
  const confirmDelete = () => {
    const name = confirmingDelete;
    if (name === null) return;
    if (revealed?.name === name) setRevealed(null);
    setRemoving(current => new Set(current).add(name));
    remove.mutateAsync(name).then(
      () => dismiss(`personal-variable-delete:${name}`),
      error => notify(`personal-variable-delete:${name}`, "danger", `${name} couldn’t be deleted`, personalVariableErrorMessage(error, "Try again.")),
    ).finally(() => setRemoving(current => {
      const next = new Set(current);
      next.delete(name);
      return next;
    }));
  };

  return (
    <>
      <ConfirmDialog isOpen={confirmingDelete !== null} onOpenChange={open => { if (!open) setConfirmingDelete(null); }} title={`Delete ${confirmingDelete ?? ""}?`} confirmLabel="Delete" onConfirm={confirmDelete}>
        <p>Works lose it the next time they start or resume.</p>
      </ConfirmDialog>
      <Card className="mk-group">
        <Card.Content ref={container} className="grid gap-1 p-3">
          {names.length === 0 && !editor && <p className="p-3 text-[14px] text-muted">No variables yet.</p>}
          {names.length > 0 && (
            <ul aria-label="Variables" className="grid gap-1">
              {names.map(name => editor && !editor.isNew && editor.name === name
                ? <li key={name}><PersonalVariableEditor initial={editor} kept={names} onDone={close} /></li>
                : <VariableRow key={name} name={name} revealed={revealed?.name === name ? revealed.value : undefined}
                    reading={reader.pending?.name === name ? reader.pending.purpose : undefined} editLocked={editor !== null}
                    deleting={removing.has(name)}
                    onToggle={() => toggle(name)} onEdit={() => edit(name)} onDelete={() => setConfirmingDelete(name)} />)}
            </ul>
          )}
          {editor?.isNew && <PersonalVariableEditor initial={editor} kept={names} onDone={close} />}
          {!editor && (
            <div className="flex flex-wrap items-center justify-between gap-2 px-2 pt-2">
              <p className="text-[13px] text-muted">Changes reach a Work the next time it starts or resumes.</p>
              <Button size="sm" variant="secondary" data-focus="add" onPress={add}><Plus size={15} aria-hidden="true" />Add variable</Button>
            </div>
          )}
        </Card.Content>
      </Card>
    </>
  );
}

function VariableRow({ name, revealed, reading, editLocked, deleting, onToggle, onEdit, onDelete }: {
  name: string; revealed: string | undefined; reading: "show" | "edit" | undefined; editLocked: boolean; deleting: boolean;
  onToggle: () => void; onEdit: () => void; onDelete: () => void;
}) {
  const shown = revealed !== undefined;
  const busy = deleting || reading === "edit";
  return (
    // Grid tracks, not flex items, so a long name or value truncates instead of widening the page.
    <li className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto_auto_auto] items-center gap-3 rounded-lg px-2 py-2 hover:bg-surface-secondary">
      <code className="truncate text-[13px] font-medium">{name}</code>
      <span className="truncate text-[13px] text-muted">{shown ? <ShownValue value={revealed} /> : <MaskedValue />}</span>
      <Button size="sm" variant="ghost" isIconOnly aria-label={shown ? `Hide ${name}` : `Show ${name}`} isPending={reading === "show"} isDisabled={busy} onPress={onToggle}>
        {shown ? <EyeOff size={15} aria-hidden="true" /> : <Eye size={15} aria-hidden="true" />}
      </Button>
      <Button size="sm" variant="ghost" isIconOnly aria-label={`Edit ${name}`} data-focus={`edit:${name}`} isPending={reading === "edit"} isDisabled={editLocked || deleting} onPress={onEdit}>
        <Pencil size={15} aria-hidden="true" />
      </Button>
      <Button size="sm" variant="ghost" isIconOnly aria-label={`Delete ${name}`} isPending={deleting} isDisabled={reading === "edit"} onPress={onDelete}>
        <Trash2 size={15} aria-hidden="true" />
      </Button>
    </li>
  );
}

const MaskedValue = () => <code><span aria-hidden="true">{MASK}</span><span className="sr-only">Value hidden</span></code>;

// A row is one line: a multi-line value shows its first line and how many follow.
function ShownValue({ value }: { value: string }) {
  if (value === "") return <span className="italic">Empty</span>;
  const [first, ...rest] = value.split("\n");
  return <><code className="text-foreground">{first}</code>{rest.length > 0 && <span className="ml-2">+{rest.length} {rest.length === 1 ? "line" : "lines"}</span>}</>;
}

// Reads one value at a time. A newer read, or a cancel, aborts the one in
// flight, so a late answer never shows a value the person moved away from.
function useValueReader() {
  const current = useRef<AbortController | null>(null);
  const [pending, setPending] = useState<{ name: string; purpose: "show" | "edit" } | null>(null);
  useEffect(() => () => current.current?.abort(), []);
  const cancel = () => {
    current.current?.abort();
    current.current = null;
    setPending(null);
  };
  const read = (name: string, purpose: "show" | "edit", onValue: (value: string) => void) => {
    cancel();
    const controller = new AbortController();
    current.current = controller;
    setPending({ name, purpose });
    revealPersonalVariable(name, controller.signal).then(
      value => {
        if (controller.signal.aborted) return;
        dismiss(`personal-variable-read:${name}`);
        onValue(value);
      },
      error => { if (!controller.signal.aborted) notify(`personal-variable-read:${name}`, "danger", `${name} couldn’t be read`, personalVariableErrorMessage(error, "Try again.")); },
    ).finally(() => {
      if (current.current !== controller) return;
      current.current = null;
      setPending(null);
    });
  };
  return { pending, read, cancel };
}
