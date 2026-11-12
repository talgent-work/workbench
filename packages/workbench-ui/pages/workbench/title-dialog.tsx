import { useState } from "react";
import { Alert, Button, Input, Label, Modal, TextField, Tooltip } from "@heroui/react";
import { Pencil } from "lucide-react";
import { useChangeWorkTitle } from "../../lib/standalone-work-service";

/** Renames a Standalone Work: its display title starts from its first input
 * and is the owner's to change. */
export function TitleButton({ workId, title }: { workId: string; title: string }) {
  const [open, setOpen] = useState(false);
  return <>
    <Tooltip><Button size="sm" variant="ghost" isIconOnly aria-label="Rename work" onPress={() => setOpen(true)}><Pencil size={15} aria-hidden="true" /></Button><Tooltip.Content>Rename work</Tooltip.Content></Tooltip>
    {open && <TitleDialog workId={workId} title={title} onClose={() => setOpen(false)} />}
  </>;
}

function TitleDialog({ workId, title, onClose }: { workId: string; title: string; onClose: () => void }) {
  const [value, setValue] = useState(title);
  const change = useChangeWorkTitle(workId);
  const trimmed = value.trim();
  const submit = () => {
    if (!trimmed || trimmed === title || change.isPending) return;
    change.mutate(trimmed, { onSuccess: onClose });
  };
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open && !change.isPending) onClose(); }} isDismissable={!change.isPending}><Modal.Container placement="center" size="sm"><Modal.Dialog>
    <Modal.Header><Modal.Heading>Rename work</Modal.Heading></Modal.Header>
    <Modal.Body>
      <TextField value={value} onChange={setValue} isInvalid={change.isError} autoFocus>
        <Label>Title</Label>
        <Input onKeyDown={event => { if (event.key === "Enter") submit(); }} />
      </TextField>
      {change.error && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{change.error.message}</Alert.Description></Alert.Content></Alert>}
    </Modal.Body>
    <Modal.Footer><Button variant="secondary" isDisabled={change.isPending} onPress={onClose}>Cancel</Button><Button isPending={change.isPending} isDisabled={!trimmed || trimmed === title} onPress={submit}>Rename</Button></Modal.Footer>
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}
