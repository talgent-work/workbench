import { useState } from "react";
import { Alert, AlertDialog, Button, Tooltip } from "@heroui/react";
import { Trash2 } from "lucide-react";
import { removeComputerErrorMessage, useRemoveComputer, type ManagedComputer } from "../../lib/computer-service";

/** Removes one of the person's Managed Computers for good, after saying what
 * happens to its Works. Cancel has focus; a failure keeps the dialog open. */
export function RemoveComputerButton({ computer }: { computer: ManagedComputer }) {
  const [open, setOpen] = useState(false);
  return <>
    <Tooltip delay={300}><Button size="sm" variant="ghost" isIconOnly aria-label={`Remove ${computer.name}`} onPress={() => setOpen(true)}><Trash2 size={15} aria-hidden="true" /></Button><Tooltip.Content>Remove</Tooltip.Content></Tooltip>
    {open && <RemoveComputerDialog computer={computer} onClose={() => setOpen(false)} />}
  </>;
}

function RemoveComputerDialog({ computer, onClose }: { computer: ManagedComputer; onClose: () => void }) {
  const remove = useRemoveComputer(computer.id);
  return <AlertDialog><AlertDialog.Backdrop isOpen onOpenChange={open => { if (!open && !remove.isPending) onClose(); }}><AlertDialog.Container><AlertDialog.Dialog className="sm:max-w-[440px]">
    <AlertDialog.Header><AlertDialog.Icon status="danger" /><AlertDialog.Heading>Remove {computer.name}?</AlertDialog.Heading></AlertDialog.Header>
    <AlertDialog.Body>
      <div className="flex flex-col gap-3 text-sm">
        <p>It can no longer connect or run anything, and Talgent Computer ends every Work process on it.</p>
        <p className="text-muted">Its running Works fail and closing ones close. Its stopped and failed Works can only be closed. Their files stay on the machine.</p>
        <p className="text-xs text-muted">To use this machine again, register it as a new computer.</p>
        {remove.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{removeComputerErrorMessage(remove.error)}</Alert.Description></Alert.Content></Alert>}
      </div>
    </AlertDialog.Body>
    <AlertDialog.Footer>
      <Button slot="close" variant="tertiary" autoFocus isDisabled={remove.isPending}>Cancel</Button>
      <Button variant="danger" isPending={remove.isPending} onPress={() => remove.mutate(undefined, { onSuccess: onClose })}>Remove</Button>
    </AlertDialog.Footer>
  </AlertDialog.Dialog></AlertDialog.Container></AlertDialog.Backdrop></AlertDialog>;
}
