import { useState } from "react";
import { Alert, Button, FieldError, Input, Label, Modal, TextField, Tooltip } from "@heroui/react";
import { Pencil } from "lucide-react";
import { computerNameProblem, useRenameComputer, type ManagedComputer } from "../../lib/computer-service";

/** Renames one of the person's Managed Computers. */
export function RenameComputerButton({ computer }: { computer: ManagedComputer }) {
  const [open, setOpen] = useState(false);
  return <>
    <Tooltip delay={300}><Button size="sm" variant="ghost" isIconOnly aria-label={`Rename ${computer.name}`} onPress={() => setOpen(true)}><Pencil size={15} aria-hidden="true" /></Button><Tooltip.Content>Rename</Tooltip.Content></Tooltip>
    {open && <RenameComputerDialog computer={computer} onClose={() => setOpen(false)} />}
  </>;
}

function RenameComputerDialog({ computer, onClose }: { computer: ManagedComputer; onClose: () => void }) {
  const [value, setValue] = useState(computer.name);
  const rename = useRenameComputer(computer.id);
  const trimmed = value.trim();
  const problem = computerNameProblem(value);
  const unchanged = trimmed === computer.name;
  const submit = () => {
    if (problem || unchanged || rename.isPending) return;
    rename.mutate(trimmed, { onSuccess: onClose });
  };
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open && !rename.isPending) onClose(); }} isDismissable={!rename.isPending}><Modal.Container placement="center" size="sm"><Modal.Dialog>
    <Modal.Header><Modal.Heading>Rename computer</Modal.Heading></Modal.Header>
    <Modal.Body>
      <TextField value={value} onChange={setValue} isInvalid={Boolean(problem)} isDisabled={rename.isPending} autoFocus>
        <Label>Name</Label>
        <Input onKeyDown={event => { if (event.key === "Enter") submit(); }} />
        <FieldError>{problem}</FieldError>
      </TextField>
      {rename.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>The computer couldn’t be renamed. Try again.</Alert.Description></Alert.Content></Alert>}
    </Modal.Body>
    <Modal.Footer><Button variant="secondary" isDisabled={rename.isPending} onPress={onClose}>Cancel</Button><Button isPending={rename.isPending} isDisabled={Boolean(problem) || unchanged} onPress={submit}>Rename</Button></Modal.Footer>
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}
