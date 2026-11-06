import { useEffect, useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button } from "@heroui/react";
import { AlertTriangle, ArrowUp, Paperclip, X } from "lucide-react";
import { useLocation } from "react-router-dom";
import type { WorkStartInput } from "../../lib/start-work-request";
import { useConnectors, useInstalledPlugins, usePluginCatalog, usePluginReleases } from "../../lib/marketplace-catalog";
import { useUsableComputers } from "../../lib/computer-service";
import { startModels, startModelSelection } from "../../lib/work-model";
import { defaultPermissionMode } from "../../lib/permission-modes";
import { useWorkStartDraft } from "../../lib/work-start-draft";
import type { WorkStartSource } from "../../lib/work-start-source";
import { connectorChoices, connectorLoadout, pluginChoices, pluginLoadout } from "../../lib/work-start-loadout";
import { startWorkErrorMessage } from "./start-work-launch-state";
import { PromptBar } from "../composer/prompt-bar";
import { PromptAddMenu, PromptModelControl } from "../composer/prompt-bar-controls";
import { SkillHighlight } from "../composer/skill-completion";
import { useSlashCompletion, type SlashCandidate } from "../composer/use-slash-completion";
import { ConnectorsMenu, PluginsMenu } from "./work-start-catalog";
import { AgentPicker, ComputerPicker, HarnessPicker, RepositoryPicker } from "./work-start-tray";
import "./work-start-tray.css";

type Menu = "Computer" | "Permissions" | "Repository" | "Agent" | "Plugins" | "Connectors";

// `unavailable` shows the form disabled with the reason, without anyone's
// personal loadout: a Work runs with its owner's own choices.
export function WorkStartForm({ source, disabled: blocked = false, unavailable, onStart }: { source: WorkStartSource; disabled?: boolean; unavailable?: ReactNode; onStart: (input: WorkStartInput) => Promise<void> }) {
  const disabled = blocked || Boolean(unavailable);
  const id = useId();
  const input = useRef<HTMLTextAreaElement>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const { pathname } = useLocation();
  const { draft, update, current } = useWorkStartDraft(source.draft);
  const options = useQuery({ queryKey: ["work-start-options", ...source.key], queryFn: ({ signal }) => source.loadOptions(signal), retry: false, enabled: !unavailable });
  // Repository discovery waits on the Provider, so it loads apart from the other options.
  const repositories = useQuery({ queryKey: ["work-start-repositories", ...source.key], queryFn: ({ signal }) => source.loadRepositories(signal), retry: false, enabled: !unavailable });
  const catalog = usePluginCatalog();
  const installed = useInstalledPlugins();
  const connectors = useConnectors();
  // Only a source that chooses a Computer offers the choice; without one the platform's default runs the Work.
  const computers = useUsableComputers(Boolean(source.chooseComputer) && !unavailable);
  const computerList = source.chooseComputer ? computers.data : undefined;
  // A remembered choice is never dropped silently: the start waits for the
  // list that confirms it, and one the list no longer has must be chosen again.
  const computerMissing = Boolean(draft.computer && computerList && !computerList.some(item => item.id === draft.computer));
  const computer = computerMissing ? undefined : computerList?.find(item => item.id === draft.computer) ?? computerList?.[0];
  const offline = computer && !computer.reachable ? computer : undefined;
  const computerUnconfirmed = Boolean(source.chooseComputer && draft.computer && !computerList);
  const managed = Boolean(computer?.managed);
  // Only a Managed Computer's Harness takes a permission mode and a sandbox.
  // A machine without Claude Code's sandbox is never asked for it.
  const chosenHarness = managed ? draft.harness ?? { permissionMode: defaultPermissionMode, sandbox: false } : undefined;
  const harness = chosenHarness && { ...chosenHarness, sandbox: chosenHarness.sandbox && Boolean(computer?.sandbox) };
  const [menu, setMenu] = useState<Menu | null>(null);
  // A Plugin's slug while its settings for this Work are open.
  const [pluginView, setPluginView] = useState("");
  const [modelOpen, setModelOpen] = useState(false);
  const [error, setError] = useState("");
  const [invalid, setInvalid] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [uploading, setUploading] = useState("");
  const models = startModels(options.data?.models ?? [], managed);
  // The first model offered is the default, also when a saved draft names
  // one that is no longer offered.
  const model = models.find(item => !item.machineLogin && item.configurationId === draft.model?.configurationId) ?? models[0];
  const thinking = draft.thinkingLevel ?? model?.defaultThinkingLevel ?? "";
  // Plugins added for this Work are read in full for their Skills and Agents.
  const added = unavailable ? [] : Object.entries(draft.plugins).filter(([slug, choice]) => choice.include && !(options.data?.plugins ?? []).some(plugin => plugin.name === slug)).map(([slug]) => slug);
  const details = usePluginReleases(added);
  const rows = unavailable ? [] : pluginLoadout(options.data?.plugins ?? [], catalog.data ?? [], installed.data, draft, details);
  const included = rows.filter(row => row.included);
  const connectorRows = unavailable ? [] : connectorLoadout(options.data?.connectors ?? [], connectors.data ?? [], draft, managed);
  const agents = included.flatMap(row => row.agents);
  const candidates: SlashCandidate[] = included.flatMap(row => row.skills.map(skill => ({ ...skill, group: row.name })));
  const slash = useSlashCompletion(draft.instruction, instruction => update({ instruction }), candidates, `${id}-skills`, true);
  const choices = pluginChoices(rows, draft);
  const problems = included.filter(row => row.problem);
  const agentMissing = Boolean(draft.agent) && !agents.some(agent => agent.name === draft.agent);
  const controlsDisabled = disabled || draft.submitting;
  const openMenu = (name: Menu) => (open: boolean) => setMenu(open ? name : null);

  useEffect(() => {
    const element = input.current;
    if (element) { element.style.height = "auto"; element.style.height = `${Math.min(element.scrollHeight, 220)}px`; }
  }, [draft.instruction]);

  async function submit(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    if (current().submitting || disabled) return;
    setInvalid(false); setError(""); setSubmitted(true);
    if (!draft.instruction.trim()) { setInvalid(true); setError("Enter an initial instruction."); input.current?.focus(); return; }
    // Load failures are reported with a retry; there is nothing to correct here.
    if (options.isError || catalog.isError || installed.isError) return;
    if (!model || (thinking && !model.thinkingLevels.includes(thinking))) { setError("Choose an available model and thinking level before starting."); return; }
    // Blocking problems are reported before submission; missing authorization is not one.
    if (problems.length || Object.keys(choices.errors).length || agentMissing || offline) return;
    if (computerMissing) { setError("Choose a Computer before starting."); return; }
    // A Computer list that failed to load is reported with a retry.
    if (computerUnconfirmed) { if (!computers.isError) setError("Computers are still loading. Try again in a moment."); return; }
    // Chosen repositories are checked once their discovery answers; the start waits for it.
    const reachable = new Set((repositories.data?.repositories ?? []).map(repository => repository.id));
    if (draft.repositories.length && repositories.isPending) { setError("Repositories are still loading. Try again in a moment."); return; }
    if (draft.repositories.some(repository => !reachable.has(repository))) { setError("A selected repository is no longer reachable. Update your selection before starting."); return; }
    update({ submitting: true }); setMenu(null); setModelOpen(false);
    try {
      for (const attachment of draft.files) {
        if (attachment.uploaded) continue;
        setUploading(attachment.file.name);
        await source.uploadAttachment?.(attachment.file);
        update(value => ({ ...value, files: value.files.map(item => item.id === attachment.id ? { ...item, uploaded: true } : item) }));
      }
      setUploading("");
      const instructions = [draft.instruction.trim()];
      if (draft.files.length) instructions.push(`Use the attached files: ${draft.files.map(item => item.file.name).join(", ")}.`);
      const connectorsChanged = connectorChoices(connectorRows);
      await onStart({ initialMessage: instructions.join("\n\n"), model: startModelSelection(model), thinkingLevel: thinking,
        repositorySelections: draft.repositories.map(repositoryId => ({ repositoryId, baseBranch: "" })),
        ...(computer && { computerId: computer.id }),
        ...(harness && { harness }),
        ...(choices.plugins.length || connectorsChanged.length || draft.agent ? { creationCapabilities: { plugins: choices.plugins, connectors: connectorsChanged, ...(draft.agent ? { startupAgent: draft.agent } : {}) } } : {}),
      });
      update({ files: [] });
    } catch (cause) { setError(startWorkErrorMessage(cause, computer)); }
    finally { setUploading(""); update({ submitting: false }); }
  }

  return <form className="wpb-form" noValidate onSubmit={event => void submit(event)}>
    {unavailable}
    {/* The slash menu floats under the composer, over the tray, like the tray's own cards. */}
    <div className="relative">
    {slash.menu && <div className="sk-menu-float sk-menu-under">{slash.menu}</div>}
    <PromptBar
      input={{ ref: input, "aria-label": "Initial instruction", "aria-invalid": invalid, "aria-describedby": invalid ? `${id}-error` : undefined, value: draft.instruction, placeholder: "What would you like to do? Type / for Skills", disabled: controlsDisabled, ...slash.inputProps,
        onChange: event => update({ instruction: event.target.value }),
        onKeyDown: event => { if (slash.onKeyDown(event)) return; if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) { event.preventDefault(); void submit(); } } }}
      context={<>
        <SkillHighlight text={draft.instruction} candidates={candidates} />
        {draft.files.length > 0 && <div className="wpb-context" aria-label="Selected context">
          {draft.files.map(attachment => <Button key={attachment.id} size="sm" variant="tertiary" isDisabled={controlsDisabled} aria-label={`Remove attachment ${attachment.file.name}`} onPress={() => update({ files: draft.files.filter(item => item.id !== attachment.id) })}><Paperclip size={13} /><span className="wpb-filename">{attachment.file.name}</span><X size={12} /></Button>)}
        </div>}
      </>}
      leading={<PromptAddMenu disabled={controlsDisabled} onAttach={source.uploadAttachment ? () => fileInput.current?.click() : undefined}
        onCommands={() => { update(value => ({ ...value, instruction: !value.instruction || /\s$/.test(value.instruction) ? `${value.instruction}/` : `${value.instruction} /` })); setTimeout(() => input.current?.focus()); }} />}
    >
      <PromptModelControl models={models} model={model} thinking={thinking} disabled={controlsDisabled} loading={options.isPending && !unavailable} open={modelOpen} onOpenChange={setModelOpen} onChange={(next, thinkingLevel) => update({ model: startModelSelection(next), thinkingLevel })} />
      <Button type="submit" size="sm" isIconOnly className="wpb-send" aria-label="Start work" isPending={draft.submitting} isDisabled={disabled || options.isPending || installed.isLoading || !model}><ArrowUp size={19} /></Button>
    </PromptBar>
    </div>
    <input ref={fileInput} type="file" multiple hidden aria-label="Choose attachments" disabled={controlsDisabled} onChange={event => { const files = Array.from(event.target.files ?? []).map(file => ({ id: crypto.randomUUID(), file, uploaded: false })); update(value => ({ ...value, files: [...value.files, ...files] })); event.target.value = ""; }} />
    <div className="wpb-tray" role="group" aria-label="Work startup configuration">
      {/* Another kind of Computer offers other models, so its thinking effort starts over; a Cloud drops the Harness choices. */}
      <ComputerPicker computers={computerList} selected={computer} disabled={controlsDisabled} open={menu === "Computer"} onOpenChange={openMenu("Computer")}
        onChange={id => update(value => { const toManaged = Boolean(computerList?.find(item => item.id === id)?.managed); return { ...value, computer: id, ...(toManaged !== managed && { thinkingLevel: undefined }), ...(!toManaged && { harness: undefined }) }; })} />
      {harness && <HarnessPicker value={harness} sandboxAvailable={Boolean(computer?.sandbox)} disabled={controlsDisabled} open={menu === "Permissions"} onOpenChange={openMenu("Permissions")} onChange={next => update({ harness: next })} />}
      <RepositoryPicker repositories={repositories.data?.repositories ?? []} recent={repositories.data?.recentRepositoryIds ?? []} unavailable={repositories.isError || Boolean(repositories.data?.repositoriesUnavailable)} loading={repositories.isPending}
        selected={draft.repositories} disabled={controlsDisabled} open={menu === "Repository"} onOpenChange={openMenu("Repository")} onChange={selected => update({ repositories: selected })} onRetry={() => void repositories.refetch()} returnTo={pathname} />
      <AgentPicker agents={agents} value={draft.agent} disabled={controlsDisabled} open={menu === "Agent"} onOpenChange={openMenu("Agent")} onChange={agent => update({ agent })} />
      <PluginsMenu rows={rows} catalog={catalog.data ?? []} draft={draft} update={update} disabled={controlsDisabled} open={menu === "Plugins"} onOpenChange={openMenu("Plugins")} view={pluginView} onViewChange={setPluginView} errors={submitted ? choices.errors : {}} />
      <ConnectorsMenu rows={connectorRows} update={update} disabled={controlsDisabled} open={menu === "Connectors"} onOpenChange={openMenu("Connectors")} returnTo={pathname} />
    </div>
    {submitted && (problems.length > 0 || Object.keys(choices.errors).length > 0 || agentMissing || offline) && <div className="wst-feedback" role="alert">
      {problems.map(row => <p key={row.slug} className="wpb-feedback-error"><AlertTriangle size={14} aria-hidden="true" /> {row.name}: {row.problem.toLowerCase()}. <Button size="sm" variant="ghost" onPress={() => { setPluginView(row.slug); setMenu("Plugins"); }}>Fix</Button></p>)}
      {Object.keys(choices.errors).filter(slug => !problems.some(row => row.slug === slug)).map(slug => <p key={slug} className="wpb-feedback-error"><AlertTriangle size={14} aria-hidden="true" /> {rows.find(row => row.slug === slug)?.name}: check its settings for this Work. <Button size="sm" variant="ghost" onPress={() => { setPluginView(slug); setMenu("Plugins"); }}>Fix</Button></p>)}
      {agentMissing && <p className="wpb-feedback-error"><AlertTriangle size={14} aria-hidden="true" /> The selected Startup Agent is no longer available. Choose another one.</p>}
      {offline && <p className="wpb-feedback-error"><AlertTriangle size={14} aria-hidden="true" /> {offline.name} is offline. Choose another Computer.</p>}
    </div>}
    {draft.submitting && <p className="sr-only" role="status">{uploading ? `Uploading ${uploading}…` : "Submitting startup request…"}</p>}
    {computerMissing && <p className="wpb-feedback" role="alert">The Computer you chose is no longer available. <Button size="sm" variant="ghost" onPress={() => setMenu("Computer")}>Choose a Computer</Button></p>}
    {source.chooseComputer && computers.isError && !computerList && <p className="wpb-feedback" role="alert">Computers could not be loaded. <Button size="sm" variant="ghost" onPress={() => void computers.refetch()}>Retry</Button></p>}
    {(options.isError || catalog.isError || installed.isError) && <p className="wpb-feedback" role="alert">Startup configuration could not be loaded. <Button size="sm" variant="ghost" onPress={() => { void options.refetch(); void catalog.refetch(); void installed.refetch(); }}>Retry</Button></p>}
    {error && <p id={`${id}-error`} className="wpb-feedback wpb-feedback-error" role="alert">{error}</p>}
  </form>;
}
