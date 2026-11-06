import type { StreamDiagnosticFrame } from "@talgent/workbench-contracts/streamgateway/public/v1/streamgateway_pb";
import { notify } from "./notifications";

// Model provider refusals the Harness or a SubAgent recovers from are told live
// as notices, never kept in the Work Message Stream. Reasons mirror the
// Dispatcher's command.Notice* vocabulary documented on StreamDiagnosticFrame;
// delivery diagnostics are connection state and stay silent.
const providerNotices: Record<string, { title: string; description: string }> = {
  model_rate_limited: { title: "Model provider rate limited", description: "The model provider is limiting requests for this Work." },
  model_unavailable: { title: "Model provider unavailable", description: "The model provider could not handle a request for this Work." },
};

export function notifyWorkNotice(workId: string, diagnostic: StreamDiagnosticFrame) {
  // A reply delivered after its request closed, as when its turn was cancelled.
  if (diagnostic.reason === "interaction_closed") {
    notify(`interaction-closed:${workId}`, "warning", "Request already closed", diagnostic.message || "The request you answered had already closed, so your answer was not used.");
    return;
  }
  const notice = providerNotices[diagnostic.reason];
  if (!notice) return;
  // One notice per Work: a burst of refusals replaces it instead of stacking.
  notify(`model-provider:${workId}`, "warning", notice.title, diagnostic.message || notice.description);
}
