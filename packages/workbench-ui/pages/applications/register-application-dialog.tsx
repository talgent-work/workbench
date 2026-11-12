import { useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { Alert, Button, Checkbox, FieldError, Input, Label, Modal, TextField } from "@heroui/react";
import { CopyButton } from "../../components/copy-button";
import { applicationNameProblem, maxApplicationName, useRegisterApplication, type RegisteredApplication } from "../../lib/application-service";

/** The one moment a Client Secret is readable: Workbench keeps only a hash. */
export function SecretOnce({ clientId, secret }: { clientId?: string; secret: string }) {
  return <div className="grid gap-3">
    {clientId && <div className="grid gap-1">
      <span className="text-[13px] font-medium">Client ID</span>
      <div className="flex items-center gap-2 rounded-xl bg-surface-secondary px-3 py-2"><code className="min-w-0 flex-1 break-all text-[13px]">{clientId}</code><CopyButton text={clientId} what="client ID" /></div>
    </div>}
    <div className="grid gap-1">
      <span className="text-[13px] font-medium">Client secret</span>
      <div className="flex items-center gap-2 rounded-xl border border-warning/40 bg-warning/5 px-3 py-2"><code className="min-w-0 flex-1 break-all text-[13px]">{secret}</code><CopyButton text={secret} what="secret" /></div>
    </div>
    <Alert status="warning"><Alert.Indicator /><Alert.Content><Alert.Description>Store the secret now. Workbench keeps only a hash and can’t show it again.</Alert.Description></Alert.Content></Alert>
  </div>;
}

/** Closing waits until the owner says they stored the secret. */
function StoredGate({ onDone }: { onDone: () => void }) {
  const [stored, setStored] = useState(false);
  return <>
    <div className="px-6 pb-2"><Checkbox isSelected={stored} onChange={setStored}><Checkbox.Content className="flex flex-row items-center gap-2"><Checkbox.Control><Checkbox.Indicator /></Checkbox.Control>I stored the secret</Checkbox.Content></Checkbox></div>
    <Modal.Footer><Button isDisabled={!stored} onPress={onDone}>Done</Button></Modal.Footer>
  </>;
}

/** Registers an Application owned by the signed-in person and shows its client
 * ID and first secret once. */
export function RegisterApplicationDialog({ onClose, onRegistered }: { onClose: () => void; onRegistered: (id: string) => void }) {
  const register = useRegisterApplication();
  const [name, setName] = useState("");
  const [touched, setTouched] = useState(false);
  const [result, setResult] = useState<RegisteredApplication | null>(null);
  const problem = applicationNameProblem(name);
  const submit = () => {
    setTouched(true);
    if (problem || register.isPending) return;
    register.mutate(name, { onSuccess: registered => { setResult(registered); onRegistered(registered.application.id); } });
  };
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open && !result) onClose(); }} isDismissable={!result && !register.isPending}><Modal.Container placement="center" size="md"><Modal.Dialog>
    <Modal.Header><Modal.Heading>{result ? `${result.application.name} is registered` : "Register an application"}</Modal.Heading></Modal.Header>
    {result ? <>
      <Modal.Body className="grid gap-4">
        <p className="text-[14px] text-muted">Your program exchanges these for a short-lived token with the OAuth 2.0 client credentials grant.</p>
        <SecretOnce clientId={result.application.id} secret={result.clientSecret} />
      </Modal.Body>
      <StoredGate onDone={onClose} />
    </> : <form onSubmit={event => { event.preventDefault(); submit(); }}>
      <Modal.Body className="grid gap-4">
        <p className="text-[14px] text-muted">An application is a program that uses Workbench through its APIs, as itself. It gets a client ID and a secret, and you manage it here.</p>
        <TextField value={name} onChange={setName} isInvalid={touched && Boolean(problem)} maxLength={maxApplicationName * 2} autoFocus>
          <Label>Name</Label>
          <Input placeholder="Support Desk" />
          <FieldError>{problem}</FieldError>
        </TextField>
        {register.isError && <Alert status="danger"><Alert.Indicator /><Alert.Content><Alert.Title>The application couldn’t be registered</Alert.Title><Alert.Description>{ConnectError.from(register.error).code === Code.InvalidArgument ? "Workbench didn’t accept this name. Use 1 to 60 characters." : "Try again in a moment."}</Alert.Description></Alert.Content></Alert>}
      </Modal.Body>
      <Modal.Footer>
        <Button variant="secondary" onPress={onClose} isDisabled={register.isPending}>Cancel</Button>
        <Button type="submit" isPending={register.isPending}>Register</Button>
      </Modal.Footer>
    </form>}
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}
