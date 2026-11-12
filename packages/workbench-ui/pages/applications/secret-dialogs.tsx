import { useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { Alert, AlertDialog, Button, Modal } from "@heroui/react";
import { secretLabel, useRevokeClientSecret, useRotateClientSecret, type Application, type ClientSecret, type RotatedSecret } from "../../lib/application-service";
import { SecretOnce, StoredGate } from "./secret-once";

const failure = (error: unknown) => ConnectError.from(error).code === Code.NotFound
  ? "The application or secret no longer exists. Reload the page."
  : "Try again in a moment.";

/** Issues a new current secret and shows it once; the replaced one keeps
 * working until its overlap ends. */
export function RotateSecretDialog({ application, onClose }: { application: Application; onClose: () => void }) {
  const rotate = useRotateClientSecret();
  const [result, setResult] = useState<RotatedSecret | null>(null);
  const overlapping = application.secrets.find(secret => secret.state === "overlapping");
  const replaced = result?.application.secrets.find(secret => secret.state === "overlapping");
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open && !result) onClose(); }} isDismissable={!result && !rotate.isPending}><Modal.Container placement="center" size="md"><Modal.Dialog>
    <Modal.Header><Modal.Heading>{result ? "New secret issued" : "Rotate secret"}</Modal.Heading></Modal.Header>
    {result ? <>
      <Modal.Body className="grid gap-4">
        <p className="text-[14px] text-muted">{replaced?.overlapEndsAt
          ? <>The previous secret keeps working until {replaced.overlapEndsAt.toLocaleString()}, so you can deploy the new one without downtime.</>
          : <>Deploy the new secret to {application.name}.</>}</p>
        <SecretOnce secret={result.clientSecret} />
      </Modal.Body>
      <StoredGate onDone={onClose} />
    </> : <>
      <Modal.Body className="grid gap-3">
        <p className="text-[14px]">Workbench issues a new secret. The current one keeps working for a limited overlap, then ends.</p>
        {overlapping && <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Description>Secret {secretLabel(overlapping)} is still in its overlap. Rotating again ends it now.</Alert.Description></Alert.Content></Alert>}
        {rotate.isError && <Alert status="danger"><Alert.Indicator /><Alert.Content><Alert.Title>The secret couldn’t be rotated</Alert.Title><Alert.Description>{failure(rotate.error)}</Alert.Description></Alert.Content></Alert>}
      </Modal.Body>
      <Modal.Footer>
        <Button variant="secondary" onPress={onClose} isDisabled={rotate.isPending}>Cancel</Button>
        <Button isPending={rotate.isPending} onPress={() => rotate.mutate(application.id, { onSuccess: setResult })}>Rotate secret</Button>
      </Modal.Footer>
    </>}
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}

/** Ends one secret at once, confirmed first. */
export function RevokeSecretDialog({ application, secret, onClose }: { application: Application; secret: ClientSecret; onClose: () => void }) {
  const revoke = useRevokeClientSecret();
  const last = application.secrets.length === 1;
  return <AlertDialog><AlertDialog.Backdrop isOpen onOpenChange={open => { if (!open && !revoke.isPending) onClose(); }}><AlertDialog.Container><AlertDialog.Dialog className="sm:max-w-[440px]">
    <AlertDialog.Header><AlertDialog.Icon status="danger" /><AlertDialog.Heading>Revoke secret {secretLabel(secret)}?</AlertDialog.Heading></AlertDialog.Header>
    <AlertDialog.Body className="grid gap-2">
      <p>Token requests with this secret fail at once. Tokens it already obtained stop when they expire, within minutes.</p>
      {last && <p className="font-medium">It’s {application.name}’s only secret: the application can’t sign in until you rotate.</p>}
      {revoke.isError && <Alert status="danger"><Alert.Indicator /><Alert.Content><Alert.Title>The secret couldn’t be revoked</Alert.Title><Alert.Description>{failure(revoke.error)}</Alert.Description></Alert.Content></Alert>}
    </AlertDialog.Body>
    <AlertDialog.Footer>
      <Button slot="close" variant="tertiary" autoFocus isDisabled={revoke.isPending}>Cancel</Button>
      <Button variant="danger" isPending={revoke.isPending} onPress={() => revoke.mutate({ applicationId: application.id, clientSecretId: secret.id }, { onSuccess: onClose })}>Revoke secret</Button>
    </AlertDialog.Footer>
  </AlertDialog.Dialog></AlertDialog.Container></AlertDialog.Backdrop></AlertDialog>;
}
