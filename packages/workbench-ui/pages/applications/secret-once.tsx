import { useState } from "react";
import { Alert, Button, Checkbox, Modal } from "@heroui/react";
import { CopyButton } from "../../components/copy-button";

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
export function StoredGate({ onDone }: { onDone: () => void }) {
  const [stored, setStored] = useState(false);
  return <>
    <div className="px-6 pb-2"><Checkbox isSelected={stored} onChange={setStored}><Checkbox.Content className="flex flex-row items-center gap-2"><Checkbox.Control><Checkbox.Indicator /></Checkbox.Control>I stored the secret</Checkbox.Content></Checkbox></div>
    <Modal.Footer><Button isDisabled={!stored} onPress={onDone}>Done</Button></Modal.Footer>
  </>;
}
