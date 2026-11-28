// PROTOTYPE ONLY: three ways Profile could manage Personal Variables,
// switchable with ?variant=A|B|C on /profile. State lives in memory; nothing
// reaches a backend. Throw away once a variant wins.
import { useState } from "react";
import { Alert, Button, Card, Chip, Description, FieldError, Input, Label, Modal, TextArea, TextField, Tooltip } from "@heroui/react";
import { Eye, EyeOff, Info, Pencil, Plus, Trash2 } from "lucide-react";
import { ConfirmDialog } from "../marketplace/marketplace-shared";
import { PrototypeSwitcher, usePrototypeVariant } from "../prototype/prototype-switcher";
import { setPrototypeVariables, usePrototypeVariables } from "../prototype/prototype-variables-store";

type Variable = { name: string; value: string };

const MAX_VARIABLES = 100;
const MAX_VALUE = 32 * 1024;
const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;

// Mirrors the accepted rules: a valid name, no TALGENT_ prefix, one value per
// name, and the delivery limits.
function nameProblem(name: string, variables: Variable[], editing?: string): string {
  if (!name) return "Enter a name.";
  if (!NAME.test(name)) return "Use letters, digits and underscores, not starting with a digit.";
  if (name.toUpperCase().startsWith("TALGENT_")) return "Names starting with TALGENT_ are reserved for the platform.";
  if (name !== editing && variables.some(variable => variable.name === name)) return `${name} already exists.`;
  if (!editing && variables.length >= MAX_VARIABLES) return `You can keep at most ${MAX_VARIABLES} variables.`;
  return "";
}
const valueProblem = (value: string) => value.length > MAX_VALUE ? "A value can be at most 32 KiB." : "";

const mask = (value: string) => "•".repeat(Math.min(12, Math.max(6, value.length)));

function SectionTitle() {
  return (
    <div className="flex items-center gap-2">
      <h3 id="pf-variables" className="mk-section-title">Variables</h3>
      <Tooltip delay={200}>
        <Tooltip.Trigger aria-label="About variables" className="rounded-full text-muted"><Info size={15} aria-hidden="true" /></Tooltip.Trigger>
        <Tooltip.Content>Environment variables for every Work your Work Agent does for you, replacing the platform's of the same name. The agent and its tools can read them.</Tooltip.Content>
      </Tooltip>
    </div>
  );
}

const NextStart = () => <p className="text-[13px] text-muted">Changes reach a Work the next time it starts or resumes.</p>;

/** The section Profile mounts while the prototype runs. */
export function PersonalVariablesPrototype() {
  const variables = usePrototypeVariables();
  const setVariables = setPrototypeVariables;
  const variant = usePrototypeVariant(["A", "B", "C"]);
  const props = { variables, setVariables };
  return (
    <section className="mk-section" aria-labelledby="pf-variables">
      <SectionTitle />
      {variant === "A" && <VariantA {...props} />}
      {variant === "B" && <VariantB {...props} />}
      {variant === "C" && <VariantC {...props} />}
      <pre className="mt-2 max-h-32 overflow-auto rounded-md bg-surface-secondary p-2 text-[11px] text-muted">{"PROTOTYPE STATE\n" + JSON.stringify(variables.map(v => `${v.name} (${v.value.length} chars)`), null, 0)}</pre>
      <PrototypeSwitcher variants={[{ key: "A", name: "Inline rows" }, { key: "B", name: ".env editor" }, { key: "C", name: "Summary + dialog" }]} />
    </section>
  );
}

type Props = { variables: Variable[]; setVariables: (update: (current: Variable[]) => Variable[]) => void };

// A: every variable is a row of the section, edited in place.
function VariantA({ variables, setVariables }: Props) {
  const [shown, setShown] = useState<string>("");
  const [editing, setEditing] = useState<string | null>(null);
  const [deleting, setDeleting] = useState("");
  return (
    <>
    <ConfirmDialog isOpen={deleting !== ""} onOpenChange={open => { if (!open) setDeleting(""); }} title={`Delete ${deleting}?`} confirmLabel="Delete"
      onConfirm={() => { setVariables(current => current.filter(item => item.name !== deleting)); setDeleting(""); }}>
      <p>Works lose it the next time they start or resume.</p>
    </ConfirmDialog>
    <Card className="mk-group">
      <Card.Content className="grid gap-1 p-3">
        {variables.length === 0 && editing === null && <p className="p-3 text-[14px] text-muted">No variables yet.</p>}
        {variables.map(variable => editing === variable.name
          ? <RowEditor key={variable.name} initial={variable} variables={variables} onCancel={() => setEditing(null)}
              onSave={next => { setVariables(current => current.map(item => item.name === variable.name ? next : item)); setEditing(null); }} />
          : (
            <div key={variable.name} className="flex items-center gap-3 rounded-lg px-2 py-2 hover:bg-surface-secondary">
              <code className="min-w-0 flex-1 truncate text-[13px] font-medium">{variable.name}</code>
              <code className="min-w-0 flex-[1.4] truncate text-[13px] text-muted">{shown === variable.name ? variable.value.split("\n")[0] : mask(variable.value)}</code>
              <Button size="sm" variant="ghost" isIconOnly aria-label={shown === variable.name ? `Hide ${variable.name}` : `Show ${variable.name}`} onPress={() => setShown(shown === variable.name ? "" : variable.name)}>
                {shown === variable.name ? <EyeOff size={15} /> : <Eye size={15} />}
              </Button>
              <Button size="sm" variant="ghost" isIconOnly aria-label={`Edit ${variable.name}`} onPress={() => setEditing(variable.name)}><Pencil size={15} /></Button>
              <Button size="sm" variant="ghost" isIconOnly aria-label={`Delete ${variable.name}`} onPress={() => setDeleting(variable.name)}><Trash2 size={15} /></Button>
            </div>
          ))}
        {editing === "" && <RowEditor initial={{ name: "", value: "" }} variables={variables} onCancel={() => setEditing(null)}
          onSave={next => { setVariables(current => [...current, next]); setEditing(null); }} />}
        {editing === null && (
          <div className="flex items-center justify-between px-2 pt-2">
            <NextStart />
            <Button size="sm" variant="secondary" onPress={() => setEditing("")}><Plus size={15} />Add variable</Button>
          </div>
        )}
      </Card.Content>
    </Card>
    </>
  );
}

function RowEditor({ initial, variables, onSave, onCancel }: { initial: Variable; variables: Variable[]; onSave: (next: Variable) => void; onCancel: () => void }) {
  const [draft, setDraft] = useState(initial);
  const [touched, setTouched] = useState(false);
  const problem = nameProblem(draft.name, variables, initial.name || undefined);
  const tooLong = valueProblem(draft.value);
  const save = () => { setTouched(true); if (!problem && !tooLong) onSave(draft); };
  return (
    <div className="grid gap-3 rounded-lg border border-border p-3">
      <div className="grid grid-cols-[1fr_1.4fr] gap-3">
        <TextField value={draft.name} onChange={name => setDraft({ ...draft, name: name.trim() })} isInvalid={touched && Boolean(problem)} autoFocus={!initial.name}>
          <Label>Name</Label><Input className="font-mono" placeholder="OPENAI_API_KEY" autoComplete="off" />
          <FieldError>{problem}</FieldError>
        </TextField>
        <TextField value={draft.value} onChange={value => setDraft({ ...draft, value })} isInvalid={Boolean(tooLong)}>
          <Label>Value</Label><TextArea rows={draft.value.includes("\n") ? 4 : 1} className="font-mono text-[13px]" autoComplete="off" spellCheck={false} />
          {tooLong ? <FieldError>{tooLong}</FieldError> : <Description>Multi-line values are kept as they are.</Description>}
        </TextField>
      </div>
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="secondary" onPress={onCancel}>Cancel</Button>
        <Button size="sm" onPress={save}>Save</Button>
      </div>
    </div>
  );
}

// B: the whole set as one .env text, masked until the person edits it.
function VariantB({ variables, setVariables }: Props) {
  const [draft, setDraft] = useState<string | null>(null);
  const [errors, setErrors] = useState<string[]>([]);
  const text = (list: Variable[]) => list.map(variable => `${variable.name}=${variable.value.includes("\n") ? JSON.stringify(variable.value) : variable.value}`).join("\n");
  const save = () => {
    const parsed: Variable[] = [];
    const problems: string[] = [];
    (draft ?? "").split("\n").forEach((line, index) => {
      if (!line.trim() || line.trim().startsWith("#")) return;
      const at = line.indexOf("=");
      if (at < 1) { problems.push(`Line ${index + 1}: write NAME=value.`); return; }
      const name = line.slice(0, at).trim();
      let value = line.slice(at + 1);
      if (value.startsWith("\"") && value.endsWith("\"")) { try { value = JSON.parse(value); } catch { /* kept as written */ } }
      const problem = nameProblem(name, parsed) || valueProblem(value);
      if (problem) problems.push(`Line ${index + 1}: ${problem}`); else parsed.push({ name, value });
    });
    setErrors(problems);
    if (problems.length === 0) { setVariables(() => parsed); setDraft(null); }
  };
  return (
    <Card className="mk-group">
      <Card.Content className="grid gap-3 p-5">
        {draft === null ? (
          <>
            <pre className="min-h-24 overflow-auto rounded-lg bg-surface-secondary p-3 font-mono text-[13px] leading-relaxed">
              {variables.length ? variables.map(variable => `${variable.name}=${mask(variable.value)}`).join("\n") : "# No variables yet"}
            </pre>
            <div className="flex items-center justify-between"><NextStart /><Button size="sm" variant="secondary" onPress={() => { setErrors([]); setDraft(text(variables)); }}><Pencil size={15} />Edit as text</Button></div>
          </>
        ) : (
          <>
            <TextField value={draft} onChange={setDraft}>
              <Label className="sr-only">Variables</Label>
              <TextArea rows={10} className="font-mono text-[13px] leading-relaxed" spellCheck={false} placeholder={"OPENAI_API_KEY=sk-...\n# Paste a .env file; one NAME=value per line"} />
              <Description>One NAME=value per line. Values show in full while you edit. Quote a multi-line value as a JSON string.</Description>
            </TextField>
            {errors.length > 0 && <Alert status="danger"><Alert.Indicator /><Alert.Content><Alert.Title>Nothing was saved</Alert.Title><Alert.Description>{errors.map(error => <span key={error} className="block">{error}</span>)}</Alert.Description></Alert.Content></Alert>}
            <div className="flex justify-end gap-2"><Button size="sm" variant="secondary" onPress={() => setDraft(null)}>Cancel</Button><Button size="sm" onPress={save}>Save all</Button></div>
          </>
        )}
      </Card.Content>
    </Card>
  );
}

// C: Profile keeps one summary line; a dialog holds the management.
function VariantC({ variables, setVariables }: Props) {
  const [open, setOpen] = useState(false);
  const [shown, setShown] = useState("");
  const [draft, setDraft] = useState<Variable>({ name: "", value: "" });
  const [touched, setTouched] = useState(false);
  const problem = nameProblem(draft.name, variables);
  const add = () => { setTouched(true); if (problem || valueProblem(draft.value)) return; setVariables(current => [...current, draft]); setDraft({ name: "", value: "" }); setTouched(false); };
  return (
    <>
      <Card className="mk-group">
        <Card.Content className="flex items-center gap-4 p-5">
          <div className="grid min-w-0 flex-1 gap-2">
            <strong className="text-[15px] font-medium">{variables.length ? `${variables.length} variable${variables.length > 1 ? "s" : ""}` : "No variables"}</strong>
            <div className="flex flex-wrap gap-1.5">{variables.map(variable => <Chip key={variable.name} size="sm" variant="soft"><Chip.Label className="font-mono">{variable.name}</Chip.Label></Chip>)}</div>
          </div>
          <Button size="sm" variant="secondary" onPress={() => setOpen(true)}>Manage</Button>
        </Card.Content>
      </Card>
      <Modal><Modal.Backdrop isOpen={open} onOpenChange={setOpen}><Modal.Container placement="center" size="lg"><Modal.Dialog>
        <Modal.Header><Modal.Heading>Variables</Modal.Heading></Modal.Header>
        <Modal.Body className="grid gap-4">
          <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)] items-start gap-2">
            <TextField value={draft.name} onChange={name => setDraft({ ...draft, name: name.trim() })} isInvalid={touched && Boolean(problem)}>
              <Label>Name</Label><Input className="font-mono" placeholder="OPENAI_API_KEY" autoComplete="off" /><FieldError>{problem}</FieldError>
            </TextField>
            <TextField value={draft.value} onChange={value => setDraft({ ...draft, value })}>
              <Label>Value</Label><TextArea rows={1} className="font-mono text-[13px]" spellCheck={false} />
            </TextField>
          </div>
          <div className="flex justify-end"><Button size="sm" onPress={add}><Plus size={15} />Add</Button></div>
          <ul className="grid min-w-0 divide-y divide-border rounded-lg border border-border">
            {variables.map(variable => (
              <li key={variable.name} className="flex items-center gap-3 px-3 py-2">
                <code className="w-40 min-w-0 shrink-0 truncate text-[13px] font-medium">{variable.name}</code>
                <code className="min-w-0 flex-1 truncate text-[13px] text-muted">{shown === variable.name ? variable.value.split("\n")[0] : mask(variable.value)}</code>
                <Button size="sm" variant="ghost" isIconOnly aria-label={`Show ${variable.name}`} onPress={() => setShown(shown === variable.name ? "" : variable.name)}>{shown === variable.name ? <EyeOff size={15} /> : <Eye size={15} />}</Button>
                <Button size="sm" variant="ghost" isIconOnly aria-label={`Delete ${variable.name}`} onPress={() => setVariables(current => current.filter(item => item.name !== variable.name))}><Trash2 size={15} /></Button>
              </li>
            ))}
            {variables.length === 0 && <li className="px-3 py-4 text-[14px] text-muted">No variables yet.</li>}
          </ul>
          <NextStart />
        </Modal.Body>
        <Modal.Footer><Button variant="secondary" onPress={() => setOpen(false)}>Done</Button></Modal.Footer>
      </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>
    </>
  );
}
