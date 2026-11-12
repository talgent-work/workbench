import { useState, type FormEvent } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { Alert, AlertDialog, Button, Card, FieldError, Input, Label, TextField } from "@heroui/react";
import { Trash2 } from "lucide-react";
import { applicationNameProblem, useRemoveApplication, useRenameApplication, type Application } from "../../lib/application-service";
import { notify } from "../../lib/notifications";

/** What an owner settles about an Application as a whole: the name they
 * manage it by, and its removal. The name names no Agent: the Application's
 * Agent keeps the name set on the Agent tab. */
export function SettingsSection({ application, onRemoved }: { application: Application; onRemoved: () => void }) {
  const [removing, setRemoving] = useState(false);
  return <>
    <section className="mk-section" aria-labelledby="ap-name">
      <h3 id="ap-name" className="mk-section-title">Name</h3>
      <RenameForm key={application.name} application={application} />
    </section>
    <section className="mk-section" aria-labelledby="ap-remove">
      <h3 id="ap-remove" className="mk-section-title">Remove application</h3>
      <p className="text-[14px] text-muted">Revokes every secret and closes its open Works. You can’t restore it.</p>
      <div><Button size="sm" variant="danger-soft" onPress={() => setRemoving(true)}><Trash2 size={15} aria-hidden="true" />Remove application</Button></div>
      {removing && <RemoveApplicationDialog application={application} onClose={() => setRemoving(false)} onRemoved={onRemoved} />}
    </section>
  </>;
}

function renameErrorMessage(error: unknown) {
  const code = error instanceof ConnectError ? error.code : undefined;
  if (code === Code.InvalidArgument) return "Use a name of 1–60 characters.";
  if (code === Code.NotFound) return "This application no longer exists.";
  return "The name could not be saved. Try again.";
}

function RenameForm({ application }: { application: Application }) {
  const rename = useRenameApplication();
  const [draft, setDraft] = useState(application.name);
  const name = draft.trim();
  const problem = applicationNameProblem(draft);
  const dirty = name !== application.name;
  const save = (event: FormEvent) => {
    event.preventDefault();
    if (!dirty || problem || rename.isPending) return;
    rename.mutate({ applicationId: application.id, name }, { onSuccess: () => notify("application-renamed", "success", "Application renamed") });
  };
  return <Card className="mk-group">
    <form onSubmit={save} aria-label="Application name">
      <Card.Content className="grid gap-4 p-5">
        <TextField value={draft} onChange={setDraft} isInvalid={Boolean(problem)} isDisabled={rename.isPending}>
          <Label>Name</Label>
          <Input />
          <FieldError>{problem}</FieldError>
        </TextField>
        {rename.isError && <Alert status="danger">
          <Alert.Indicator />
          <Alert.Content><Alert.Title>{renameErrorMessage(rename.error)}</Alert.Title></Alert.Content>
        </Alert>}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="mk-muted min-w-[16rem] flex-1 text-[13px]">Shown in your applications list. The Agent keeps its own name.</p>
          <div className="ml-auto flex gap-2">
            <Button size="sm" variant="ghost" isDisabled={!dirty || rename.isPending} onPress={() => { rename.reset(); setDraft(application.name); }}>Discard</Button>
            <Button size="sm" type="submit" isDisabled={!dirty || Boolean(problem)} isPending={rename.isPending}>{rename.isPending ? "Saving" : "Save"}</Button>
          </div>
        </div>
      </Card.Content>
    </form>
  </Card>;
}

/** Removes the Application for good, confirmed first. */
function RemoveApplicationDialog({ application, onClose, onRemoved }: { application: Application; onClose: () => void; onRemoved: () => void }) {
  const remove = useRemoveApplication();
  // Removal drops the Application from the list, which unmounts this dialog
  // before a callback given to mutate would run; the promise still settles,
  // so the page moves on. One removed meanwhile answers NotFound and leaves
  // the same way; any other failure shows in the dialog.
  const done = () => { onClose(); onRemoved(); };
  const confirm = () => remove.mutateAsync(application.id).then(done, (error: unknown) => { if (ConnectError.from(error).code === Code.NotFound) done(); });
  return <AlertDialog><AlertDialog.Backdrop isOpen onOpenChange={open => { if (!open && !remove.isPending) onClose(); }}><AlertDialog.Container><AlertDialog.Dialog className="sm:max-w-[460px]">
    <AlertDialog.Header><AlertDialog.Icon status="danger" /><AlertDialog.Heading>Remove {application.name}?</AlertDialog.Heading></AlertDialog.Header>
    <AlertDialog.Body className="grid gap-3">
      <ul className="grid list-disc gap-1 pl-5">
        <li>Its secrets stop working now. Tokens it already obtained stop when they expire, within minutes.</li>
        <li>Its open Works close.</li>
        <li>You can’t restore it. Register a new application to start again.</li>
      </ul>
      {remove.isError && <Alert status="danger"><Alert.Indicator /><Alert.Content><Alert.Title>The application couldn’t be removed</Alert.Title><Alert.Description>Try again in a moment.</Alert.Description></Alert.Content></Alert>}
    </AlertDialog.Body>
    <AlertDialog.Footer>
      <Button slot="close" variant="tertiary" autoFocus isDisabled={remove.isPending}>Cancel</Button>
      <Button variant="danger" isPending={remove.isPending} onPress={() => void confirm()}>Remove application</Button>
    </AlertDialog.Footer>
  </AlertDialog.Dialog></AlertDialog.Container></AlertDialog.Backdrop></AlertDialog>;
}
