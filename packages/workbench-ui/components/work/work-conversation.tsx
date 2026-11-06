import type { StartupProgress } from "@talgent-work/workbench-contracts/agent/public/v1/startup_pb";
import type { WorkStartupRequest } from "@talgent-work/workbench-contracts/agent/public/v1/work_pb";
import type { WorkModelOption, WorkModelSelection } from "@talgent-work/workbench-contracts/agent/public/v1/model_pb";
import { PermissionMode } from "@talgent-work/workbench-contracts/agent/public/v1/harness_pb";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import { Alert, Button } from "@heroui/react";
import { useCallback, useRef, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { StartupProgressPanel } from "../session/startup-progress";
import type { WorkComputer } from "../../lib/computer-service";
import { WorkPermissionControl } from "../session/work-permission-control";
import { WorkThinkingControl } from "../session/work-thinking-control";
import { useReadingArea } from "../shell/reading-context";
import { useWorkLifecycleControls } from "../../hooks/use-work-lifecycle-controls";
import { useWorkConversation } from "../../hooks/use-work-conversation";
import { agentWorkClient } from "../../lib/client";
import type { WorkStartInput } from "../../lib/start-work-request";
import { newestStartupProgress } from "../../lib/startup-progress";
import { adaptSubAgents, type SubAgent } from "../../lib/subagents";
import { workModelOption as presentedModel } from "../../lib/work-model";
import { stopReasonNotice } from "../../lib/work-state-presentation";
import { awaitingReconnectionNotice, computerOfflineMessage } from "../../lib/work-projection";
import { WorkMessageFlow, type ToolPreview } from "./work-message-flow";
import { WorkInteraction } from "./work-interaction";
import { WorkPromptBar } from "./work-prompt-bar";
import { WorkStatusControl } from "./work-status-control";

/** The Work's live Turn and requests are not observed now: Agent answers
 * Unavailable during a Disconnection or while Dispatcher reattaches. */
const unobserved = (error: unknown) => Boolean(error) && ConnectError.from(error).code === Code.Unavailable;

export interface WorkConversationProps {
  /** The Computer the Work runs on. */
  computer?: WorkComputer;
  agentWorkId: string;
  /** What the Work is for, the first crumb of what it opens in the reading area. */
  label: string;
  /** Names what the Work is for while it starts. */
  startupTitle?: string;
  workLifecycle?: string;
  /** Why the Work stopped, when it is Stopped. */
  stateReason?: string;
  /** Why a Failed Work ended, as Agent recorded it. */
  failureReason?: string;
  /** When the Work was disconnected; absent while observed. */
  disconnectedSince?: Date;
  /** The Stopped Work keeps its messages for its Managed Computer's return
   * and resumes with them when it reconnects. */
  resumeOnReconnection?: boolean;
  workControlPending?: boolean;
  startupProgress?: StartupProgress;
  startupRequest?: WorkStartupRequest;
  workModel?: WorkModelSelection;
  /** The retained Model Configuration as currently provided; absent when withdrawn. */
  workModelOption?: WorkModelOption;
  allowedActions?: string[];
  /** A Managed Work's permission mode; unspecified for a Cloud Work. */
  permissionMode?: PermissionMode;
  /** The Work runs in Claude Code's sandbox for its life. */
  sandbox?: boolean;
  /** The Work's start waits in its Computer's line for room. */
  deskWaiting?: boolean;
  workVersion?: bigint;
  onWorkControlChange?: () => void;
  onWorkLifecycleChange?: (lifecycle: string, updatedAt?: Date, startupProgress?: StartupProgress) => void;
  /** The Work's SubAgents from each state frame of this stream. */
  onSubAgentsChange?: (agents: SubAgent[]) => void;
  /** Starts the Work again with its first startup request after admission failed. */
  onRetryStart?: (input: WorkStartInput) => Promise<void>;
  /** Adds a file to the Work; absent where the Work takes none. */
  onUploadAttachment?: (file: File) => Promise<unknown>;
  /** The Work's snapshot is still loading and no live state has arrived. */
  isWorkLifecyclePending?: boolean;
  /** A reader who is not the owner: the conversation renders without the
   * prompt bar, responses or controls. */
  readOnly?: boolean;
  /** The reader's access was withdrawn while reading. */
  onAccessWithdrawn?: () => void;
  /** Says the Work's state shown is the last one read, when refreshing fails. */
  snapshotNotice?: ReactNode;
  /** Replaces the Work's own status control with one its page supplies. */
  statusControl?: ReactNode;
  /** Shown in place of the prompt bar once the Work is closed. */
  closedText?: string;
}

/** A started Work's conversation: its startup until it is ready, then its
 * messages, the request awaiting the owner and the prompt bar. */
export function WorkConversation({ computer, agentWorkId, label, startupTitle, workLifecycle = "", stateReason, failureReason, disconnectedSince, resumeOnReconnection = false, workControlPending = false, startupProgress, startupRequest, workModel, workModelOption, workVersion, allowedActions = [], permissionMode = PermissionMode.UNSPECIFIED, sandbox = false, deskWaiting = false, onWorkControlChange, onWorkLifecycleChange, onSubAgentsChange, onRetryStart, onUploadAttachment, isWorkLifecyclePending = false, readOnly = false, onAccessWithdrawn, snapshotNotice, statusControl, closedText = "This Work has ended" }: WorkConversationProps) {
  const cache = useQueryClient();
  const { open: openReader } = useReadingArea();
  const pendingInput = useRef<{ text: string; id: string } | null>(null);
  const [liveStartup, setLiveStartup] = useState<StartupProgress>();
  const processing = workLifecycle === "working";
  const model = presentedModel(workModel, workModelOption);
  // Turn and request controls are the owner's; a reader never queries them.
  // During a Disconnection neither is observed, so neither is read; the
  // Disconnection shown on the Work explains it.
  const disconnected = Boolean(disconnectedSince);
  const controlling = processing && !readOnly;
  const observing = controlling && !disconnected;
  const turn = useQuery({
    queryKey: ["work-current-turn", agentWorkId], enabled: Boolean(agentWorkId && observing),
    queryFn: ({ signal }) => agentWorkClient.getCurrentTurn({ workId: agentWorkId! }, { signal }),
    retry: false, refetchInterval: observing ? 2000 : false, refetchOnWindowFocus: false,
  });
  const interactions = useQuery({
    queryKey: ["work-pending-interactions", agentWorkId], enabled: Boolean(agentWorkId && observing),
    queryFn: ({ signal }) => agentWorkClient.listPendingInteractions({ workId: agentWorkId! }, { signal }),
    retry: false, refetchInterval: observing ? 2000 : false, refetchOnWindowFocus: false, gcTime: 0,
  });
  // What was last read is not current while unobserved, so it is not shown.
  const turnObserved = processing && !disconnected && !unobserved(turn.error);
  const requestsObserved = processing && !disconnected && !unobserved(interactions.error);
  const refreshControls = useCallback(() => {
    void cache.invalidateQueries({ queryKey: ["work-current-turn", agentWorkId] });
    void cache.invalidateQueries({ queryKey: ["work-pending-interactions", agentWorkId] });
  }, [cache, agentWorkId]);
  const conversation = useWorkConversation(agentWorkId ?? undefined, state => {
    if (state.preparationProgress) setLiveStartup(state.preparationProgress);
    onWorkLifecycleChange?.(state.state, state.updatedAt ? timestampDate(state.updatedAt) : undefined, state.preparationProgress);
    onSubAgentsChange?.(adaptSubAgents(state.subagents));
    if (state.workProgress) onWorkControlChange?.();
    refreshControls();
  }, () => { refreshControls(); onWorkControlChange?.(); }, refreshControls, onAccessWithdrawn);
  const currentStartup = newestStartupProgress(liveStartup, startupProgress);
  const currentTurnId = turnObserved ? turn.data?.turnId : undefined;
  const currentRequest = requestsObserved ? interactions.data?.interactions.find(item => !item.replySubmitted) ?? interactions.data?.interactions[0] : undefined;
  const requiredResponse = Boolean(requestsObserved && interactions.data?.interactions.some(item => !item.replySubmitted));
  const applyWork = (work?: { state: string; updatedAt?: Parameters<typeof timestampDate>[0]; startupProgress?: StartupProgress }) => {
    if (!work) return;
    onWorkLifecycleChange?.(work.state, work.updatedAt ? timestampDate(work.updatedAt) : undefined, work.startupProgress);
  };
  const controls = useWorkLifecycleControls(agentWorkId, applyWork, () => { onWorkControlChange?.(); refreshControls(); }, workVersion);
  const { pending: actionPending, error: controlError, run: runAction, resume, continueWithInput, restart, setArchiveOpen } = controls;
  // A Work awaiting its Computer's return says so itself, so its refused
  // Resume adds no error of its own; once the Work is no longer stopped, the
  // refusal no longer describes it.
  const awaitingReconnection = resumeOnReconnection && workLifecycle === "stopped";
  const actionError = controlError === computerOfflineMessage && (awaitingReconnection || workLifecycle !== "stopped") ? "" : controlError;
  const controlsPending = workControlPending || actionPending;
  // Input sent while a Turn runs is Steer. A required response holds the
  // composer; its reply card stays usable. A Work not closed takes input:
  // the owner's message to a stopped Work also resumes it.
  // An unobserved Work takes it too: its buffer holds it for the Harness.
  const resumable = workLifecycle === "stopped" && allowedActions.includes("resume") && !controlsPending;
  const stopNotice = stopReasonNotice(workLifecycle, stateReason);
  const canSend = (processing && (!requestsObserved || (interactions.isSuccess && !requiredResponse)) && !controlsPending) || resumable;
  const openTool = useCallback((tool: ToolPreview, trigger: HTMLElement) => openReader({ kind: "tool", label, tool }, trigger), [openReader, label]);
  // A running Work's settings change only while it runs and its controls are free.
  const canAdjust = processing && allowedActions.includes("stop") && !controlsPending;
  const thinkingControl = workModel ? <WorkThinkingControl sessionId={agentWorkId} option={model} initialLevel={startupRequest?.initialThinkingLevel} canAdjust={canAdjust} /> : undefined;
  const workStatus = !readOnly && (statusControl ?? <WorkStatusControl workLifecycle={workLifecycle} stateReason={stateReason} disconnectedSince={disconnectedSince} allowedActions={allowedActions} disabled={workControlPending} controls={controls} />);

  if (workLifecycle !== "closed" && currentStartup && currentStartup.status !== "completed") {
    const retryInitial = !readOnly && workLifecycle === "created" && currentStartup.status === "failed" && !currentStartup.admitted && allowedActions.includes("retry_start") && startupRequest && onRetryStart;
    const canRestart = !readOnly && workLifecycle === "failed" && allowedActions.includes("restart");
    const retry = retryInitial ? () => void runAction(() => onRetryStart!({ initialMessage: startupRequest!.initialMessage, model: workModel, thinkingLevel: startupRequest!.initialThinkingLevel, repositorySelections: startupRequest!.repositories.map(repository => ({ repositoryId: repository.repositoryId, baseBranch: repository.baseBranch })) }))
      : canRestart ? () => void restart() : !readOnly && workLifecycle === "stopped" && allowedActions.includes("resume") ? () => void resume() : undefined;
    return <section data-testid="agent-stream-panel" className="stream-panel stream-panel-starting" aria-label="Agent Stream">{workStatus}{snapshotNotice}<StartupProgressPanel computer={computer} progress={currentStartup} waitingForRoom={deskWaiting && workLifecycle === "starting"} workTitle={startupTitle} request={startupRequest} model={model} onRetry={retry} retryLabel={retryInitial ? "Retry startup" : canRestart ? "Restart Work" : "Resume Work"} retryDescription={canRestart ? "Restart continues this Work using its saved conversation when available." : undefined} retryPending={controlsPending} retryError={actionError} onArchive={!readOnly && allowedActions.includes("archive") && ["failed", "stopped"].includes(workLifecycle) && !workControlPending ? () => setArchiveOpen(true) : undefined} /></section>;
  }
  // Until the Work's snapshot arrives, a newly started Work shows startup, never an empty conversation.
  if (!currentStartup && (isWorkLifecyclePending || ["created", "starting"].includes(workLifecycle))) return <section data-testid="agent-stream-panel" className="stream-panel stream-panel-starting" aria-label="Agent Stream">{workStatus}{snapshotNotice}<p className="sr-only" role="status">Loading startup progress…</p></section>;

  return <section data-testid="agent-stream-panel" className="stream-panel" aria-label="Agent Stream">
    {workStatus}
    {snapshotNotice}
    <WorkMessageFlow conversation={conversation} currentTurnId={currentTurnId} waiting={requiredResponse} onOpenTool={openTool} />
    {readOnly ? conversation.error && <div className="wi-notices"><Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Message history could not be loaded.</Alert.Description></Alert.Content><Button size="sm" variant="ghost" onPress={conversation.retry}>Retry</Button></Alert></div> : <div className="wi-dock">
      {currentRequest && <WorkInteraction key={currentRequest.id} workId={agentWorkId} request={currentRequest} disabled={controlsPending} onResponded={refreshControls} />}
      {workLifecycle === "failed" && failureReason && <p className="wpb-feedback" role="status">This Work failed. {failureReason}</p>}
      {stopNotice && <p className="wpb-feedback" role="status">{stopNotice}{allowedActions.includes("resume") && !awaitingReconnection && " Resume continues the conversation."}</p>}
      {awaitingReconnection && <p className="wpb-feedback" role="status">{awaitingReconnectionNotice(computer?.name)}</p>}
      {turnObserved && turn.data?.active && !currentTurnId && <p className="wpb-feedback" role="status">Starting turn…</p>}
      {turnObserved && turn.data?.cancelRequested && <p className="wpb-feedback" role="status">Cancelling turn…</p>}
      {(conversation.error || (turnObserved && turn.isError) || (requestsObserved && interactions.isError)) && <div className="wi-notices">
        {turnObserved && turn.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Current turn unavailable.</Alert.Description></Alert.Content><Button size="sm" variant="ghost" onPress={() => void turn.refetch()}>Retry</Button></Alert>}
        {requestsObserved && interactions.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Could not load the current request.</Alert.Description></Alert.Content><Button size="sm" variant="ghost" onPress={() => void interactions.refetch()}>Retry</Button></Alert>}
        {conversation.error && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Message history could not be loaded.</Alert.Description></Alert.Content><Button size="sm" variant="ghost" onPress={conversation.retry}>Retry</Button></Alert>}
      </div>}
      <WorkPromptBar repositories={startupRequest?.repositories} workId={agentWorkId} active={processing} disabled={false} mode={processing || resumable ? "send" : "disabled"} sendDisabled={!canSend}
        workActionPending={controlsPending || Boolean(currentTurnId && turn.data?.cancelRequested)} workActionError={actionError}
        placeholder={requiredResponse ? "Answer the request above to continue" : currentTurnId ? "Add to this turn" : processing && disconnected ? "Delivered once the Work reconnects" : "Work on anything"} unavailableText={workLifecycle === "closed" ? closedText : undefined} inputLabel="Instruction" sendLabel="Send instruction"
        onSend={async text => {
          if (!canSend) throw new Error(requiredResponse ? "Answer the request above first." : "Work is not ready for another instruction.");
          if (!pendingInput.current || pendingInput.current.text !== text) pendingInput.current = { text, id: crypto.randomUUID() };
          const input = pendingInput.current;
          await agentWorkClient.sendMessage({ workId: agentWorkId, text, inputId: input.id });
          if (pendingInput.current === input) pendingInput.current = null;
          // A stopped Work keeps the message for its next start; resuming reads it.
          if (resumable) { await continueWithInput(); return; }
          // Keep the send lock until the authoritative post-admission state
          // arrives; an unobserved Work has none to read.
          if (observing) await turn.refetch();
        }}
        onCancelTurn={currentTurnId ? async () => {
          await agentWorkClient.cancelTurn({ workId: agentWorkId, turnId: currentTurnId });
          await turn.refetch();
        } : undefined}
        onUploadAttachment={workLifecycle === "closed" ? undefined : onUploadAttachment}
        modelControl={permissionMode ? <div className="wpb-work-controls">
          <WorkPermissionControl workId={agentWorkId} mode={permissionMode} sandbox={sandbox} canAdjust={canAdjust && allowedActions.includes("set_permission_mode")} starterOnly={processing && !allowedActions.includes("set_permission_mode")} />{thinkingControl}
        </div> : thinkingControl} />
    </div>}
  </section>;
}
