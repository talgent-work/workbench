// PROTOTYPE (Applications): the Application dialogs every variant shares: register,
// show a secret once, rotate, revoke and remove.
import { useEffect, useState } from "react";
import { AlertDialog, Alert, Button, Checkbox, FieldError, Input, Label, Modal, TextField, Tooltip } from "@heroui/react";
import { Check, Copy } from "lucide-react";
import { overlapHours, useProtoStore, type ClientSecret, type ProtoApplication } from "./proto-data";

export function CopyValue({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const text = copied ? "Copied" : `Copy ${label}`;
  return <Tooltip delay={300}>
    <Button size="sm" variant="ghost" isIconOnly aria-label={text} onPress={() => { void navigator.clipboard?.writeText(value).then(() => setCopied(true)); }}>
      {copied ? <Check size={15} aria-hidden="true" /> : <Copy size={15} aria-hidden="true" />}
    </Button>
    <Tooltip.Content>{text}</Tooltip.Content>
  </Tooltip>;
}

/** The one moment a secret is readable: it cannot be shown again. */
function SecretOnce({ clientId, secret }: { clientId?: string; secret: string }) {
  return <div className="grid gap-3">
    {clientId && <div className="grid gap-1">
      <span className="text-[13px] font-medium">Client ID</span>
      <div className="flex items-center gap-2 rounded-xl bg-surface-secondary px-3 py-2"><code className="min-w-0 flex-1 break-all text-[13px]">{clientId}</code><CopyValue value={clientId} label="client ID" /></div>
    </div>}
    <div className="grid gap-1">
      <span className="text-[13px] font-medium">Client secret</span>
      <div className="flex items-center gap-2 rounded-xl border border-warning/40 bg-warning/5 px-3 py-2"><code className="min-w-0 flex-1 break-all text-[13px]">{secret}</code><CopyValue value={secret} label="secret" /></div>
    </div>
    <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Description>Store the secret now. Workbench keeps only a hash and can’t show it again.</Alert.Description></Alert.Content></Alert>
  </div>;
}

function StoredGate({ onDone }: { onDone: () => void }) {
  const [stored, setStored] = useState(false);
  return <>
    <div className="pb-2"><Checkbox isSelected={stored} onChange={setStored}><Checkbox.Content className="flex flex-row items-center gap-2"><Checkbox.Control><Checkbox.Indicator /></Checkbox.Control>I stored the secret</Checkbox.Content></Checkbox></div>
    <Modal.Footer><Button isDisabled={!stored} onPress={onDone}>Done</Button></Modal.Footer>
  </>;
}

export function RegisterApplicationDialog({ onClose, onRegistered }: { onClose: () => void; onRegistered?: (id: string) => void }) {
  const store = useProtoStore();
  const [name, setName] = useState("");
  const [result, setResult] = useState<{ application: ProtoApplication; secret: string } | null>(null);
  const trimmed = name.trim();
  const problem = trimmed.length > 60 ? "Use at most 60 characters." : "";
  const submit = () => { if (!trimmed || problem) return; const created = store.register(trimmed); setResult(created); onRegistered?.(created.application.id); };
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open && !result) onClose(); }} isDismissable={!result}><Modal.Container placement="center" size="md"><Modal.Dialog>
    <Modal.Header><Modal.Heading>{result ? `${result.application.name} is registered` : "Register an application"}</Modal.Heading></Modal.Header>
    {result ? <>
      <Modal.Body className="grid gap-4"><p className="text-[14px] text-muted">Your program exchanges these for a short-lived token with the OAuth 2.0 client credentials grant.</p><SecretOnce clientId={result.application.clientId} secret={result.secret} /></Modal.Body>
      <StoredGate onDone={onClose} />
    </> : <>
      <Modal.Body>
        <p className="text-[14px] text-muted">An application is a program that uses Workbench through its APIs. It gets its own Agent, and you manage it here.</p>
        <TextField value={name} onChange={setName} isInvalid={Boolean(problem)} autoFocus>
          <Label>Name</Label>
          <Input placeholder="Support Desk" onKeyDown={event => { if (event.key === "Enter") submit(); }} />
          <FieldError>{problem}</FieldError>
        </TextField>
      </Modal.Body>
      <Modal.Footer><Button variant="secondary" onPress={onClose}>Cancel</Button><Button isDisabled={!trimmed || Boolean(problem)} onPress={submit}>Register</Button></Modal.Footer>
    </>}
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}

export function RotateSecretDialog({ application, onClose }: { application: ProtoApplication; onClose: () => void }) {
  const store = useProtoStore();
  const [secret, setSecret] = useState<string | null>(null);
  const overlapping = application.secrets.find(item => item.state === "overlapping");
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open && !secret) onClose(); }} isDismissable={!secret}><Modal.Container placement="center" size="md"><Modal.Dialog>
    <Modal.Header><Modal.Heading>{secret ? "New secret issued" : "Rotate secret"}</Modal.Heading></Modal.Header>
    {secret ? <>
      <Modal.Body className="grid gap-4"><p className="text-[14px] text-muted">The previous secret keeps working for {overlapHours} hours, so you can deploy the new one without downtime.</p><SecretOnce secret={secret} /></Modal.Body>
      <StoredGate onDone={onClose} />
    </> : <>
      <Modal.Body>
        <p className="text-[14px]">Workbench issues a new secret. The current one keeps working for {overlapHours} hours, then ends.</p>
        {overlapping && <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Description>Secret {overlapping.hint} is still in its overlap. Rotating again ends it now.</Alert.Description></Alert.Content></Alert>}
      </Modal.Body>
      <Modal.Footer><Button variant="secondary" onPress={onClose}>Cancel</Button><Button onPress={() => setSecret(store.rotate(application.id))}>Rotate secret</Button></Modal.Footer>
    </>}
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}

export function RevokeSecretDialog({ application, secret, onClose }: { application: ProtoApplication; secret: ClientSecret; onClose: () => void }) {
  const store = useProtoStore();
  const last = application.secrets.length === 1;
  return <AlertDialog><AlertDialog.Backdrop isOpen onOpenChange={open => { if (!open) onClose(); }}><AlertDialog.Container><AlertDialog.Dialog className="sm:max-w-[440px]">
    <AlertDialog.Header><AlertDialog.Icon status="danger" /><AlertDialog.Heading>Revoke secret {secret.hint}?</AlertDialog.Heading></AlertDialog.Header>
    <AlertDialog.Body>
      <p>Requests with this secret fail at once. Tokens it already got expire within the hour.</p>
      {last && <p className="mt-2 font-medium">It’s {application.name}’s only secret: the application can’t sign in until you rotate.</p>}
    </AlertDialog.Body>
    <AlertDialog.Footer>
      <Button slot="close" variant="tertiary" autoFocus>Cancel</Button>
      <Button variant="danger" onPress={() => { store.revoke(application.id, secret.id); onClose(); }}>Revoke secret</Button>
    </AlertDialog.Footer>
  </AlertDialog.Dialog></AlertDialog.Container></AlertDialog.Backdrop></AlertDialog>;
}

export function RemoveApplicationDialog({ application, onClose, onRemoved }: { application: ProtoApplication; onClose: () => void; onRemoved?: () => void }) {
  const store = useProtoStore();
  const open = application.works.filter(work => work.state !== "closed").length;
  return <AlertDialog><AlertDialog.Backdrop isOpen onOpenChange={value => { if (!value) onClose(); }}><AlertDialog.Container><AlertDialog.Dialog className="sm:max-w-[460px]">
    <AlertDialog.Header><AlertDialog.Icon status="danger" /><AlertDialog.Heading>Remove {application.name}?</AlertDialog.Heading></AlertDialog.Header>
    <AlertDialog.Body>
      <ul className="grid list-disc gap-1 pl-5">
        <li>Its secrets stop working now.</li>
        <li>{open > 0 ? `Its ${open} open ${open === 1 ? "Work closes" : "Works close"}.` : "It has no open Works."}</li>
        <li>You can’t restore it. Register a new application to start again.</li>
      </ul>
    </AlertDialog.Body>
    <AlertDialog.Footer>
      <Button slot="close" variant="tertiary" autoFocus>Cancel</Button>
      <Button variant="danger" onPress={() => { store.remove(application.id); onClose(); onRemoved?.(); }}>Remove application</Button>
    </AlertDialog.Footer>
  </AlertDialog.Dialog></AlertDialog.Container></AlertDialog.Backdrop></AlertDialog>;
}
