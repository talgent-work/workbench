import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Popover, Tooltip } from "@heroui/react";
import { Lock } from "lucide-react";
import type { WorkModelOption } from "@talgent-work/workbench-contracts/agent/public/v1/model_pb";
import { ModelTrigger, ModelVendorIcon, ThinkingEffort } from "../composer/prompt-bar-controls";
import { agentWorkClient } from "../../lib/client";
import { modelVendors } from "../../lib/model-vendors";
import { thinkingLevelName } from "../../lib/thinking-levels";
import "../composer/work-start.css";

type ThinkingLevel = { level: string };

// A running Work keeps its model, so the card presents it read
// only and adjusts only the thinking effort. The model comes from the Work
// itself; a withdrawn configuration has no option and offers no levels. The
// level is read from the Harness once and cached: a change shows at once and
// returns to the previous level if the Harness refuses it.
export function WorkThinkingControl({ sessionId, option, initialLevel = "", canAdjust }: { sessionId: string; option?: WorkModelOption; initialLevel?: string; canAdjust: boolean }) {
  const [open, setOpen] = useState(false);
  const cache = useQueryClient();
  const key = ["work-thinking-level", sessionId];
  const current = useQuery({ queryKey: key, queryFn: ({ signal }) => agentWorkClient.getThinkingLevel({ workId: sessionId }, { signal }), retry: false, staleTime: 60_000 });
  const change = useMutation({
    mutationFn: (level: string) => agentWorkClient.setThinkingLevel({ workId: sessionId, level }),
    onMutate: async level => {
      // A read still in flight must not overwrite the level just chosen.
      await cache.cancelQueries({ queryKey: key });
      const previous = cache.getQueryData<ThinkingLevel>(key);
      cache.setQueryData<ThinkingLevel>(key, { level });
      return { previous };
    },
    onError: (_error, _level, context) => cache.setQueryData(key, context?.previous),
  });
  const levels = option?.thinkingLevels ?? [];
  const level = current.data?.level ?? (initialLevel || option?.defaultThinkingLevel || "");
  const effort = current.isError ? "Unavailable" : option ? thinkingLevelName(level) : "";
  // Opening refreshes a stale level in the background; the card never waits on it.
  return <Popover isOpen={open} onOpenChange={value => { setOpen(value); if (!value) return; change.reset(); if (current.isStale && !change.isPending) void current.refetch(); }}>
    <ModelTrigger name={option?.name ?? "Unavailable model"} effort={effort} tentative={current.isPending} />
    <Popover.Content placement="top end" className="wpb-popover wpb-popover-narrow"><Popover.Dialog aria-label="Model settings">
      <div className="wpb-model-head">
        {option && <ModelVendorIcon vendor={option.vendor} />}
        <span className="wpb-choice-copy"><span className="wpb-model-head-name">{option?.name ?? "Unavailable model"}</span>{option && <span className="wpb-muted">{modelVendors[option.vendor]?.label}</span>}</span>
        <Tooltip delay={200}><Tooltip.Trigger className="wpb-lock" tabIndex={0} aria-label="Model is fixed for this Work"><Lock size={13} aria-hidden="true" /></Tooltip.Trigger><Tooltip.Content>Fixed for this Work</Tooltip.Content></Tooltip>
      </div>
      {levels.length > 0 && <ThinkingEffort levels={levels} value={level} disabled={!canAdjust || current.isError || change.isPending} onChange={next => change.mutate(next)} />}
      {change.error && <p className="wpb-popover-error" role="alert">Thinking effort wasn’t changed: {change.error.message}</p>}
      {current.isError && <p className="wpb-popover-error" role="alert">Thinking effort couldn’t be read. <Button size="sm" variant="ghost" onPress={() => void current.refetch()}>Retry</Button></p>}
    </Popover.Dialog></Popover.Content>
  </Popover>;
}
