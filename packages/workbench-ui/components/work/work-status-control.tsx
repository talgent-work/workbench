import { createPortal } from "react-dom";
import { Alert, Button, Dropdown, Label, Modal, Tooltip } from "@heroui/react";
import { Archive, Circle, Play, Power, RotateCcw } from "lucide-react";
import { useReadingArea } from "../shell/reading-context";
import { workPresentation, workStatusName } from "../../lib/work-state-presentation";
import type { useWorkLifecycleControls } from "../../hooks/use-work-lifecycle-controls";

interface WorkStatusControlProps {
  workLifecycle: string;
  allowedActions: string[];
  /** Why the Work stopped, when it is Stopped. */
  stateReason?: string;
  /** When the Work was disconnected; absent while observed. */
  disconnectedSince?: Date;
  disabled?: boolean;
  controls: ReturnType<typeof useWorkLifecycleControls>;
}

export function WorkStatusControl({ workLifecycle, allowedActions, stateReason, disconnectedSince, disabled = false, controls }: WorkStatusControlProps) {
  const { archiveOpen, setArchiveOpen, pending: actionPending, error: actionError, stop, resume, restart, archive } = controls;
  const controlsPending = disabled || actionPending;
  const { workStatusToolbar } = useReadingArea();
  const archiveDialog = <Modal><Modal.Backdrop isOpen={archiveOpen} onOpenChange={setArchiveOpen} isDismissable={!actionPending} isKeyboardDismissDisabled={actionPending}><Modal.Container placement="center" size="sm"><Modal.Dialog>
    <Modal.Header><Modal.Heading>Archive work?</Modal.Heading></Modal.Header>
    <Modal.Body><p>This closes the work and removes its working files. Published files and your home are kept.</p>{actionError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{actionError}</Alert.Description></Alert.Content></Alert>}</Modal.Body>
    <Modal.Footer><Button autoFocus variant="secondary" isDisabled={actionPending} onPress={() => setArchiveOpen(false)}>Cancel</Button><Button variant="danger" isPending={actionPending} onPress={() => void archive()}>Archive work</Button></Modal.Footer>
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;

  const status = workPresentation({ state: workLifecycle, stateReason, disconnectedSince });
  const StatusIcon = status?.icon ?? Circle;
  const label = status?.label ?? "Unknown state";
  const name = workStatusName("Work: ", { label, description: status?.description });
  const tooltip = status?.description ? <><p>{label}</p><p className="text-muted">{status.description}</p></> : label;
  const actions = [
    { id: "stop", label: "Stop work", icon: Power },
    { id: "resume", label: "Resume work", icon: Play },
    { id: "restart", label: "Restart work", icon: RotateCcw },
    { id: "archive", label: "Archive work", icon: Archive },
  ].filter(action => allowedActions.includes(action.id));
  const workStatus = workStatusToolbar && createPortal(actions.length ? <Dropdown>
    <Tooltip><Button size="sm" variant="ghost" isIconOnly aria-label={name} isDisabled={controlsPending}><StatusIcon size={17} aria-hidden="true" /></Button><Tooltip.Content className="max-w-72">{tooltip}</Tooltip.Content></Tooltip>
    <Dropdown.Popover placement="bottom end"><Dropdown.Menu aria-label="Work actions" onAction={key => {
      if (controlsPending || !allowedActions.includes(String(key))) return;
      if (key === "stop") void stop();
      else if (key === "resume") void resume();
      else if (key === "restart") void restart();
      else if (key === "archive") setArchiveOpen(true);
    }}>
      {actions.map(({ id, label, icon: Icon }) => <Dropdown.Item key={id} id={id} textValue={label} variant={id === "archive" ? "danger" : undefined} isDisabled={controlsPending}><Icon size={17} aria-hidden="true" /><Label>{label}</Label></Dropdown.Item>)}
    </Dropdown.Menu></Dropdown.Popover>
  </Dropdown> : <Tooltip><Tooltip.Trigger className="wm-work-status" role="status" tabIndex={0} aria-label={name}><StatusIcon size={17} aria-hidden="true" /></Tooltip.Trigger><Tooltip.Content className="max-w-72">{tooltip}</Tooltip.Content></Tooltip>, workStatusToolbar);

  return <>{workStatus}{archiveDialog}</>;
}
