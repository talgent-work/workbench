import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button, Popover } from "@heroui/react";
import { ChevronDown, Lock, Shield } from "lucide-react";
import type { PermissionMode } from "@talgent-work/workbench-contracts/agent/public/v1/harness_pb";
import type { GetWorkResponse } from "@talgent-work/workbench-contracts/agent/public/v1/work_pb";
import { PermissionModeList } from "../composer/permission-mode-list";
import { PopoverFootnote } from "../composer/prompt-bar-controls";
import { agentWorkClient } from "../../lib/client";
import { agentWorkQueryKey } from "../../lib/agent-work-service";
import { permissionModeOption } from "../../lib/permission-modes";
import "../composer/work-start.css";

// A running Managed Work's permission mode, which only its starter may switch
// while it runs. A change shows at once and returns to the previous mode if
// the Harness refuses it; the Work then follows the mode its Harness reports,
// also one the Harness changed to by itself. Whether the Work runs in Claude
// Code's sandbox is fixed at start, so it is only stated.
export function WorkPermissionControl({ workId, mode, sandbox, canAdjust, starterOnly }: { workId: string; mode: PermissionMode; sandbox: boolean; canAdjust: boolean; starterOnly: boolean }) {
  const [open, setOpen] = useState(false);
  const cache = useQueryClient();
  // The accepted mode until the Work itself reports a newer one.
  const [accepted, setAccepted] = useState<{ mode: PermissionMode; basis: PermissionMode }>();
  const change = useMutation({
    mutationFn: async (next: PermissionMode) => (await agentWorkClient.setPermissionMode({ workId, permissionMode: next })).permissionMode,
    onSuccess: next => {
      setAccepted({ mode: next, basis: mode });
      cache.setQueryData<GetWorkResponse>(agentWorkQueryKey(workId), data => data?.work ? { ...data, work: { ...data.work, permissionMode: next } } : data);
      void cache.invalidateQueries({ queryKey: agentWorkQueryKey(workId) });
    },
  });
  const shown = change.isPending ? change.variables ?? mode : accepted && accepted.basis === mode ? accepted.mode : mode;
  const option = permissionModeOption(shown);
  return <Popover isOpen={open} onOpenChange={value => { setOpen(value); if (value && !change.isPending) change.reset(); }}>
    <Button size="sm" variant="ghost" aria-label="Permission mode" className="wpb-mode-trigger">
      <Shield size={14} aria-hidden="true" /><span className="wpb-mode-name">{option?.short ?? "Permissions"}</span><ChevronDown size={13} />
    </Button>
    <Popover.Content placement="top end" className="wpb-popover wpb-popover-narrow"><Popover.Dialog aria-label="Permission mode">
      <PermissionModeList value={shown} disabled={!canAdjust || change.isPending} onChange={next => change.mutate(next)} />
      {change.error && <p className="wpb-popover-error" role="alert">Permission mode wasn’t changed: {change.error.message}</p>}
      {starterOnly && <PopoverFootnote>Only the person who started this Work can change it</PopoverFootnote>}
      {sandbox && <PopoverFootnote><span className="wpb-sandbox-note"><Lock size={12} aria-hidden="true" />Runs in Claude Code’s sandbox</span></PopoverFootnote>}
    </Popover.Dialog></Popover.Content>
  </Popover>;
}
