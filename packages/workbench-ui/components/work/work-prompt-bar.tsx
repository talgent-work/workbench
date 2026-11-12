import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Alert, CloseButton } from "@heroui/react";
import { PluginPreparationStatus, type WorkPluginResult } from "@talgent/contracts/agent/public/v1/plugin_pb";
import type { WorkStartupRepository } from "@talgent/contracts/agent/public/v1/work_pb";
import { MessageInput, type MessageInputProps } from "../chat/message-input";
import type { SlashCandidate } from "../composer/use-slash-completion";
import { agentWorkClient } from "../../lib/client";
import { pluginDisplayName } from "../../lib/marketplace-plugins";
import { notify } from "../../lib/notifications";

// Safe reason codes of Plugin installation and loading; unknown codes are
// never shown raw. A failed plugin degrades the Work, so no retry is offered.
const reasons: Record<string, string> = {
  installation_failed: "Couldn’t be installed.",
  marketplace_unavailable: "The catalog was unavailable, so it wasn’t installed.",
  preparation_failed: "Couldn’t be set up.",
  not_loaded: "Installed but not loaded by the agent.",
};
function pluginFailure(result: WorkPluginResult) {
  if (result.installation === PluginPreparationStatus.FAILED) return reasons[result.installationReason] ?? "Couldn’t be loaded.";
  if (result.loading === PluginPreparationStatus.FAILED) return reasons[result.loadingReason] ?? "Couldn’t be loaded.";
  return "";
}

// Why a selected repository was not checked out; the Work continues without it.
const checkoutReasons: Record<string, string> = {
  timed_out: "Checkout didn’t finish in time.",
  checkout_unavailable: "The repository couldn’t be cloned.",
  branch_unavailable: "The selected base branch wasn’t found.",
  target_conflict: "Another folder already uses its place in the workspace.",
  preparation_incomplete: "Checkout didn’t finish before the Work started.",
};

// The running Work's composer. Slash candidates are exactly what the Harness
// reported, listed flat: the runtime gives them no reliable category. Plugins
// that failed to install or load and repositories not checked out are reported
// above it while the Work continues.
export function WorkPromptBar({ repositories = [], ...props }: MessageInputProps & { workId: string; active: boolean; repositories?: WorkStartupRepository[] }) {
  const { workId, active } = props;
  const [dismissed, setDismissed] = useState<string[]>([]);
  const capabilities = useQuery({ queryKey: ["work-capabilities", workId], queryFn: () => agentWorkClient.getWorkCapabilities({ workId }), enabled: active, retry: false, refetchInterval: active ? 15_000 : false });
  const results = useQuery({ queryKey: ["work-plugin-results", workId], queryFn: () => agentWorkClient.getWorkPluginResults({ workId }), retry: false, refetchInterval: active ? 30_000 : false });
  const commands = capabilities.data?.observed ? capabilities.data.commands : [];
  // A namespaced name marks a Plugin Skill for the in-text highlight only.
  const candidates: SlashCandidate[] = commands.map(command => ({ name: command.name, description: command.description, argumentHint: command.argumentHint, builtIn: !command.name.includes(":") }));
  useCommandChanges(workId, commands.map(command => command.name), Boolean(capabilities.data?.observed));
  const failures = (results.data?.plugins ?? []).map(result => ({ slug: result.slug, reason: pluginFailure(result) })).filter(item => item.reason && !dismissed.includes(item.slug));
  const missing = repositories.filter(repository => repository.checkoutStatus === "not_checked_out" && !dismissed.includes(`repository:${repository.repositoryId}`));
  const dismiss = (key: string) => setDismissed(current => [...current, key]);
  const notices = failures.length || missing.length ? <div className="grid gap-2 mb-2">
    {failures.map(failure => <DegradationNotice key={failure.slug} name={pluginDisplayName(failure.slug)} title={`${pluginDisplayName(failure.slug)} isn’t available in this Work`} description={`${failure.reason} Other plugins work as usual.`} onDismiss={() => dismiss(failure.slug)} />)}
    {missing.map(repository => { const name = repository.name || repository.repositoryId; const reason = checkoutReasons[repository.checkoutReason];
      return <DegradationNotice key={repository.repositoryId} name={name} title={`${name} wasn’t checked out`} description={`${reason ? `${reason} ` : ""}The Work continues without it.`} onDismiss={() => dismiss(`repository:${repository.repositoryId}`)} />; })}
  </div> : undefined;
  return <MessageInput {...props} notices={notices} slash={{ candidates, observed: Boolean(capabilities.data?.observed) }} />;
}

// A startup step that degraded: the Work runs without what it names.
function DegradationNotice({ name, title, description, onDismiss }: { name: string; title: string; description: string; onDismiss: () => void }) {
  return <Alert status="warning">
    <Alert.Indicator /><Alert.Content><Alert.Title>{title}</Alert.Title><Alert.Description>{description}</Alert.Description></Alert.Content>
    <CloseButton aria-label={`Dismiss ${name} notice`} onPress={onDismiss} />
  </Alert>;
}

// A notice when the Harness reports a changed command set, not on first load.
function useCommandChanges(workId: string, names: string[], observed: boolean) {
  const previous = useRef<string[] | null>(null);
  const key = names.join("\n");
  useEffect(() => {
    if (!observed) return;
    const current = key ? key.split("\n") : [];
    const before = previous.current;
    previous.current = current;
    if (!before) return;
    const added = current.filter(name => !before.includes(name));
    if (added.length) notify(`commands:${workId}`, "default", added.length === 1 ? "New command available" : `${added.length} new commands available`, `${added.map(name => `/${name}`).join(", ")} can be used in this Work. Type / to use it.`);
    else if (current.length !== before.length) notify(`commands:${workId}`, "default", "Commands updated", "Type / to see what this Work can run now.");
  }, [key, observed, workId]);
}
