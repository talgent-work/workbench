import { useEffect, useState } from "react";
import { Alert, Button, Spinner } from "@heroui/react";
import { Check, ChevronDown, CircleAlert, Cloud, Monitor, RotateCcw, Square, X } from "lucide-react";
import type { StartupProgress } from "@talgent/workbench-contracts/agent/public/v1/startup_pb";
import type { WorkStartupRequest } from "@talgent/workbench-contracts/agent/public/v1/work_pb";
import type { WorkModelOption } from "@talgent/workbench-contracts/agent/public/v1/model_pb";
import { modelVendors } from "../../lib/model-vendors";
import type { WorkComputer } from "../../lib/computer-service";
import "./startup-progress.css";

const stages: Record<string, { name: string; detail: string }> = {
  admission: { name: "Preparing to start", detail: "Checking availability for your Work." },
  sandbox: { name: "Preparing computer", detail: "Bringing your cloud environment online." },

  agent: { name: "Starting Agent", detail: "Loading the Agent and enabled plugins." },
  workspace: { name: "Preparing workspace", detail: "Getting your working files and tools ready." },
  input: { name: "Submitting task", detail: "Passing your instruction to the Agent." },
};
const statuses: Record<string, string> = { pending: "Waiting", active: "In progress", completed: "Ready", failed: "Failed", cancelled: "Cancelled" };
interface Props {
  progress: StartupProgress | undefined;
  workTitle?: string;
  request?: WorkStartupRequest;
  model?: WorkModelOption;
  onRetry?: () => void;
  onArchive?: () => void;
  retryLabel?: string;
  retryDescription?: string;
  retryPending?: boolean;
  retryError?: string;
  /** The start waits in its Computer's line for room. */
  waitingForRoom?: boolean;
  /** The Computer the Work starts on; a Cloud when absent. */
  computer?: WorkComputer;
}

export function StartupProgressPanel(props: Props) {
  if (!props.progress || props.progress.status === "completed") return null;
  return <StartupProgressView key={props.progress.requestId} {...props} progress={props.progress} />;
}

function StartupProgressView({ computer, progress, workTitle, request, model, onRetry, onArchive, retryLabel = "Retry startup", retryDescription, retryPending = false, retryError, waitingForRoom = false }: Props & { progress: StartupProgress }) {
  const running = progress.status === "preparing" || progress.status === "recovering";
  const failed = progress.status === "failed";
  const cancelled = progress.status === "cancelled";
  const [observedAt] = useState(() => Date.now());
  const [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    if (!running) return;
    const timer = window.setInterval(() => setElapsed(Math.floor((Date.now() - observedAt) / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [observedAt, running]);
  // A start waiting in its Computer's line has no preparation under way yet.
  const queued = waitingForRoom && !failed && !cancelled;
  const managed = computer?.managed ?? false;
  const title = failed ? "Work couldn’t start" : cancelled ? "Startup cancelled" : queued ? "Waiting for room" : running ? "Starting your Work" : "Waiting for startup status";
  const current = progress.stages.find(stage => stage.status === "active");
  const elapsedText = `${String(Math.floor(elapsed / 60)).padStart(2, "0")}:${String(elapsed % 60).padStart(2, "0")}`;
  return <section className="wlp-stage" aria-label="Startup progress">
    <div className="wlp-panel" data-state={progress.status}>
      <header className="wlp-heading">
        <span className="wlp-symbol" data-running={running} aria-hidden="true">{running && <span className="wlp-orbit" />}{failed ? <CircleAlert /> : cancelled ? <Square /> : managed ? <Monitor /> : <Cloud />}</span>
        <div key={title} className="wlp-heading-copy"><h2>{title}</h2>{workTitle && <p title={workTitle}>{workTitle}</p>}</div>
        {running && <span className="wlp-elapsed" role="timer" title="Time observing this startup on this page" aria-label={`Observed on this page for ${elapsed} seconds`}>{elapsedText}</span>}
      </header>
      {!queued && <ol className="wlp-steps" aria-label="Startup stages">
        {progress.stages.map(stage => {
          const active = stage.status === "active" && running;
          const done = stage.status === "completed";
          const state = done ? "complete" : stage.status;
          return <li key={stage.stage} data-state={state} aria-current={active || stage.status === "failed" ? "step" : undefined}>
            <span key={state} className="wlp-step-marker" aria-hidden="true">{done ? <Check size={14} /> : active ? <Spinner size="sm" /> : stage.status === "failed" ? <X size={14} /> : <span />}</span>
            <div className="wlp-step-copy"><span>{stages[stage.stage]?.name ?? stage.stage}</span>{active && <p className="wlp-stage-detail">{stageDetail(stage.stage, managed)}<span className="wlp-activity" aria-hidden="true" /></p>}</div>
            <span className="sr-only">{statuses[stage.status] ?? "Waiting for status"}</span>
          </li>;
        })}
      </ol>}
      <div className="sr-only" role="status">{title}. {running && current && !queued ? stages[current.stage]?.name ?? current.stage : ""}</div>
      {queued ? <p className="wlp-notice">It starts once a running Work on its Computer ends.</p>
        : progress.status === "recovering" ? <p className="wlp-notice">A startup step is being retried automatically.</p>
        : running && elapsed >= 45 && <p className="wlp-notice">This is taking longer than usual. Work is still preparing.</p>}
      {(failed || cancelled) && progress.reason && <Alert className="wlp-error" status={failed ? "danger" : "default"} role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{progress.reason}</Alert.Description></Alert.Content></Alert>}
      {request && <details className="wlp-request"><summary><span>View request</span><span>{[model?.name, request.initialThinkingLevel].filter(Boolean).join(" · ")}</span><ChevronDown size={14} /></summary>
        <div className="wlp-request-content"><p>{request.initialMessage}</p><dl>
          <div><dt>Computer</dt><dd>{computer?.name ?? (managed ? "Managed Computer" : "Cloud")}</dd></div>
          {model && <div><dt>Model</dt><dd>{model.name} · {modelVendors[model.vendor]?.label}</dd></div>}
          <div><dt>Agent</dt><dd>{request.startupAgent || "Default Agent"}</dd></div>
          {request.repositories.length > 0 && <div><dt>Repository</dt><dd>{request.repositories.map(repository => repository.name || repository.repositoryId).join(", ")}</dd></div>}
          <div><dt>Plugins</dt><dd>{request.plugins.length ? request.plugins.join(", ") : "None"}</dd></div>
        </dl></div>
      </details>}
      {retryError && <Alert className="wlp-error" status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{retryError}</Alert.Description></Alert.Content></Alert>}
      <footer className="wlp-footer"><p>{retryDescription ?? (running ? "Work starts in the background." : request ? "Your request is saved." : "Waiting for startup details.")}</p>{onArchive && <Button size="sm" variant="danger-soft" isDisabled={retryPending} onPress={onArchive}>Archive Work</Button>}{onRetry && <Button size="sm" isPending={retryPending} onPress={onRetry}><RotateCcw size={14} />{retryLabel}</Button>}</footer>
    </div>
  </section>;
}

// A Managed Computer is the person's own machine, already online when a start
// reaches it; its sandbox stage readies the Work there.
function stageDetail(stage: string, managed: boolean): string {
  if (managed && stage === "sandbox") return "Getting your computer ready for this Work.";
  return stages[stage]?.detail ?? "Waiting for the next startup update.";
}
