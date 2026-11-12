import { useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { Alert, AlertDialog, Button } from "@heroui/react";
import { Trash2 } from "lucide-react";
import { useRemoveApplication, type Application } from "../../lib/application-service";

/** What an owner settles about an Application as a whole: for now, its
 * removal. */
export function SettingsSection({ application, onRemoved }: { application: Application; onRemoved: () => void }) {
  const [removing, setRemoving] = useState(false);
  return <section className="mk-section" aria-labelledby="ap-remove">
    <h3 id="ap-remove" className="mk-section-title">Remove application</h3>
    <p className="text-[14px] text-muted">Revokes every secret and closes its open Works. You can’t restore it.</p>
    <div><Button size="sm" variant="danger-soft" onPress={() => setRemoving(true)}><Trash2 size={15} aria-hidden="true" />Remove application</Button></div>
    {removing && <RemoveApplicationDialog application={application} onClose={() => setRemoving(false)} onRemoved={onRemoved} />}
  </section>;
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
