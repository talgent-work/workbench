import { Label, ListBox } from "@heroui/react";
import type { PermissionMode } from "@talgent-work/workbench-contracts/agent/public/v1/harness_pb";
import { permissionModes } from "../../lib/permission-modes";

/** The permission modes as one single choice, each with what it does. Both
 * composers use it: Start Work on a Managed Computer and a running Managed Work. */
export function PermissionModeList({ value, disabled, onChange }: { value: PermissionMode; disabled?: boolean; onChange: (mode: PermissionMode) => void }) {
  return <ListBox aria-label="Permission mode" selectionMode="single" disallowEmptySelection selectedKeys={[String(value)]}
    disabledKeys={disabled ? permissionModes.map(option => String(option.mode)) : []}
    onSelectionChange={keys => { if (keys === "all" || !keys.size) return; const next = Number([...keys][0]) as PermissionMode; if (next !== value) onChange(next); }}>
    {permissionModes.map(option => <ListBox.Item key={option.mode} id={String(option.mode)} textValue={option.name} className="wpb-choice">
      <span className="wpb-choice-copy wpb-choice-stack"><Label>{option.name}</Label><span className="wpb-muted">{option.description}</span></span><ListBox.ItemIndicator />
    </ListBox.Item>)}
  </ListBox>;
}
