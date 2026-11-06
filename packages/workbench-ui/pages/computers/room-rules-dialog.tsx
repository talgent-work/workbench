import { useState } from "react";
import { Alert, Button, Description, FieldError, Label, Modal, NumberField, Radio, RadioGroup, Tooltip } from "@heroui/react";
import { SlidersHorizontal } from "lucide-react";
import { capacityProblem, maxCapacity, roomRulesErrorMessage, useSetRoomRules, type AdmissionPolicy, type ManagedComputer } from "../../lib/computer-service";

/** Sets how many Works one of the person's Managed Computers runs at once and
 * what it does with a run while full. */
export function RoomRulesButton({ computer }: { computer: ManagedComputer }) {
  const [open, setOpen] = useState(false);
  return <>
    <Tooltip delay={300}><Button size="sm" variant="ghost" isIconOnly aria-label={`Capacity of ${computer.name}`} onPress={() => setOpen(true)}><SlidersHorizontal size={15} aria-hidden="true" /></Button><Tooltip.Content>Capacity</Tooltip.Content></Tooltip>
    {open && <RoomRulesDialog computer={computer} onClose={() => setOpen(false)} />}
  </>;
}

const policies: { value: AdmissionPolicy; title: string; description: string }[] = [
  { value: "reject", title: "Reject", description: "A new run is refused until a running Work ends." },
  { value: "queue", title: "Queue", description: "A new run waits as Starting and begins once a running Work ends." },
];

function RoomRulesDialog({ computer, onClose }: { computer: ManagedComputer; onClose: () => void }) {
  const [capacity, setCapacity] = useState(computer.capacity);
  const [policy, setPolicy] = useState<AdmissionPolicy>(computer.policy);
  const rules = useSetRoomRules(computer.id);
  const problem = capacityProblem(capacity, computer.running);
  const unchanged = capacity === computer.capacity && policy === computer.policy;
  const submit = () => {
    if (problem || unchanged || rules.isPending) return;
    rules.mutate({ capacity, policy }, { onSuccess: onClose });
  };
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open && !rules.isPending) onClose(); }} isDismissable={!rules.isPending}><Modal.Container placement="center" size="sm"><Modal.Dialog>
    <Modal.Header><Modal.Heading>Capacity of {computer.name}</Modal.Heading></Modal.Header>
    <Modal.Body className="grid gap-5">
      <NumberField value={capacity} onChange={setCapacity} minValue={1} maxValue={maxCapacity} step={1} isInvalid={Boolean(problem)} isDisabled={rules.isPending} autoFocus>
        <Label>Works at once</Label>
        <NumberField.Group>
          <NumberField.DecrementButton />
          <NumberField.Input className="w-16" />
          <NumberField.IncrementButton />
        </NumberField.Group>
        <Description>{computer.running} running now</Description>
        <FieldError>{problem}</FieldError>
      </NumberField>
      <RadioGroup value={policy} onChange={value => setPolicy(value as AdmissionPolicy)} isDisabled={rules.isPending}>
        <Label>When it’s full</Label>
        {policies.map(option => <Radio key={option.value} value={option.value}>
          <Radio.Control><Radio.Indicator /></Radio.Control>
          <Radio.Content><Label>{option.title}</Label><Description>{option.description}</Description></Radio.Content>
        </Radio>)}
      </RadioGroup>
      {rules.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{roomRulesErrorMessage(rules.error)}</Alert.Description></Alert.Content></Alert>}
    </Modal.Body>
    <Modal.Footer><Button variant="secondary" isDisabled={rules.isPending} onPress={onClose}>Cancel</Button><Button isPending={rules.isPending} isDisabled={Boolean(problem) || unchanged} onPress={submit}>Save</Button></Modal.Footer>
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}
