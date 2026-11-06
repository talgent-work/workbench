import { useRef, useState } from "react";
import { Alert, Button, Card, Chip, Dropdown, Label, Link as HeroLink, Switch } from "@heroui/react";
import type { InstalledPlugin, PluginRelease } from "@talgent-work/workbench-contracts/marketplace/public/v1/marketplace_pb";
import { ChevronRight, Lock, MoreHorizontal } from "lucide-react";
import { Link } from "react-router-dom";
import { useInstalledPlugins, usePluginActions } from "../../lib/marketplace-catalog";
import { isPlatformPlugin, pluginPresentation } from "../../lib/marketplace-plugins";
import { marketplacePaths } from "../../lib/marketplace-routes";
import { notify } from "../../lib/notifications";
import { draftsFrom, missingRequired, parameterLabel, parseParameters, type ParameterDrafts, type ParameterErrors } from "../../lib/plugin-parameters";
import { serviceLabel } from "../../lib/marketplace-connections";
import { ConnectorIcon, PluginIcon } from "./marketplace-icons";
import { CapIcon, ConfirmDialog } from "./marketplace-shared";
import { ParameterFields } from "./parameter-fields";

// Marketplace › Plugins › Name. The header carries identity and actions; the
// body explains what the Plugin brings into a Work. Skills and Agents appear
// only here, never in Marketplace lists.
export function PluginPage({ plugin }: { plugin: PluginRelease }) {
  const installedPlugins = useInstalledPlugins();
  const installed = installedPlugins.data?.get(plugin.slug);
  const actions = usePluginActions();
  const presentation = pluginPresentation(plugin);
  const [drafts, setDrafts] = useState<ParameterDrafts>({});
  const [errors, setErrors] = useState<ParameterErrors>({});
  const [confirm, setConfirm] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  const platform = isPlatformPlugin(plugin.slug);
  const saved = { configuration: installed?.configuration, redacted: installed?.redactedParameters };
  const missing = installed ? missingRequired(plugin.parameters, saved) : [];
  const values = { ...draftsFrom(installed?.configuration), ...drafts };
  const hasSettings = Object.keys(plugin.parameters).length > 0;

  const submit = () => {
    const parsed = parseParameters(plugin.parameters, drafts, saved);
    setErrors(parsed.errors);
    const invalid = Object.keys(parsed.errors)[0];
    if (invalid) { form.current?.querySelector<HTMLElement>(`[name="${CSS.escape(invalid)}"]`)?.focus(); return; }
    const name = presentation.name;
    if (!installed) {
      actions.install.mutate({ slug: plugin.slug, configuration: parsed.values }, {
        onSuccess: result => { setDrafts({}); notify(`plugin:${plugin.slug}`, "success", `${name} installed`, "Works you start from now on include it.", { action: { label: "Undo", onPress: () => actions.uninstall.mutate(result) } }); },
        onError: () => notify(`plugin:${plugin.slug}`, "danger", `${name} couldn’t be installed`, "Check the settings and try again."),
      });
      return;
    }
    // A blank field that had a saved value clears that setting.
    const cleared = Object.entries(drafts).filter(([key, value]) => !value.trim() && installed.configuration?.[key] !== undefined).map(([key]) => key);
    actions.configure.mutate({ installed, values: parsed.values, cleared }, {
      onSuccess: () => { setDrafts({}); notify(`plugin:${plugin.slug}`, "success", "Settings saved", "Works you start from now on use them."); },
      onError: () => notify(`plugin:${plugin.slug}`, "danger", "Settings couldn’t be saved", "Check the values and try again."),
    });
  };
  const toggle = (value: InstalledPlugin) => actions.setEnabled.mutate({ installed: value, enabled: !value.enabled }, {
    onSuccess: () => notify(`plugin:${plugin.slug}`, "default", `${presentation.name} ${value.enabled ? "disabled" : "enabled"}`),
    onError: () => notify(`plugin:${plugin.slug}`, "danger", `${presentation.name} couldn’t be changed`, "Refresh and try again."),
  });

  return <div className="mk-detail">
    <header className="mk-detail-head">
      <PluginIcon plugin={plugin} size={64} />
      <div className="mk-detail-title"><h2>{presentation.name}</h2><p className="mk-meta"><span>{presentation.category}</span><span>Version {plugin.version}</span></p></div>
      <div className="mk-detail-actions">
        {platform ? <span className="mk-muted mk-inline"><Lock size={15} aria-hidden="true" />Always enabled</span>
          : !installed ? <Button variant="primary" isPending={actions.install.isPending} isDisabled={installedPlugins.isPending} onPress={submit}>Install</Button>
          : <>
            <Switch isSelected={installed.enabled} isDisabled={actions.setEnabled.isPending} onChange={() => toggle(installed)} aria-label={`Enable ${presentation.name}`}><Switch.Control><Switch.Thumb /></Switch.Control></Switch>
            <Dropdown><Button size="sm" variant="ghost" isIconOnly aria-label={`More actions for ${presentation.name}`}><MoreHorizontal size={18} /></Button>
              <Dropdown.Popover placement="bottom end"><Dropdown.Menu aria-label="More actions" onAction={key => { if (key === "uninstall") setConfirm(true); }}>
                <Dropdown.Item id="uninstall" textValue="Uninstall" variant="danger"><Label>Uninstall…</Label></Dropdown.Item>
              </Dropdown.Menu></Dropdown.Popover></Dropdown>
          </>}
      </div>
    </header>

    {installed && <ConfirmDialog isOpen={confirm} onOpenChange={setConfirm} title={`Uninstall ${presentation.name}?`} confirmLabel="Uninstall" onConfirm={() => actions.uninstall.mutate(installed, {
      onSuccess: () => notify(`plugin:${plugin.slug}`, "default", `${presentation.name} uninstalled`),
      onError: () => notify(`plugin:${plugin.slug}`, "danger", `${presentation.name} couldn’t be uninstalled`, "Refresh and try again."),
    })}><p>Your saved settings are removed. Works already running keep it. Connectors and connected Providers aren’t affected.</p></ConfirmDialog>}
    {missing.length > 0 && <Alert status="warning"><Alert.Indicator /><Alert.Content>
      <Alert.Title>Version {plugin.version} needs {missing.map(name => parameterLabel(name, plugin.parameters[name]).toLowerCase()).join(", ")}</Alert.Title>
      <Alert.Description>Works you start ask for it first. Add it under Settings to stop being asked.</Alert.Description>
    </Alert.Content></Alert>}

    <section className="mk-section" aria-labelledby="mk-about">
      <h3 id="mk-about" className="mk-section-title">About</h3>
      <Card className={`mk-group mk-about${presentation.author || presentation.license || presentation.homepage ? "" : " mk-about-single"}`}>
        <div className="mk-about-main">
          <p className="mk-about-text">{plugin.description}</p>
          {presentation.keywords.length > 0 && <p className="mk-keywords">{presentation.keywords.map(word => <Chip key={word} size="sm" variant="soft"><Chip.Label>{word}</Chip.Label></Chip>)}</p>}
        </div>
        {(presentation.author || presentation.license || presentation.homepage) && <dl className="mk-about-facts">
          {presentation.author && <div><dt>Author</dt><dd>{presentation.author}</dd></div>}
          {presentation.license && <div><dt>License</dt><dd>{presentation.license}</dd></div>}
          {presentation.homepage && <div><dt>Homepage</dt><dd><HeroLink href={presentation.homepage} target="_blank" rel="noreferrer">{presentation.homepage.replace(/^https:\/\//, "")}</HeroLink></dd></div>}
        </dl>}
      </Card>
    </section>

    {plugin.skills.length > 0 && <Group title="Skills" count={plugin.skills.length}>{plugin.skills.map(skill => <li key={skill.name}>
      <CapIcon kind="skill" />
      <span className="mk-row-text"><strong><code>/{skill.name}</code>{skill.argumentHint && <span className="mk-hint"> {skill.argumentHint}</span>}</strong><span>{skill.description}</span></span>
    </li>)}</Group>}
    {plugin.agents.length > 0 && <Group title="Agents" count={plugin.agents.length}>{plugin.agents.map(agent => <li key={agent.name}>
      <CapIcon kind="agent" /><span className="mk-row-text"><strong>{agent.name.split(":").at(-1)}</strong><span>{agent.description}</span></span>
    </li>)}</Group>}
    {plugin.mcpServers.length > 0 && <Group title="Connectors" count={plugin.mcpServers.length}>{plugin.mcpServers.map(server => {
      const connector = server.relationship?.status === "supported" ? server.relationship.connectorName : "";
      // A declaration without a catalog Connector still loads with the Plugin.
      return connector ? <li key={server.name} className="mk-group-link"><Link to={marketplacePaths.connector(connector)}>
        <ConnectorIcon name={connector} size={32} /><span className="mk-row-text"><strong>{serviceLabel(connector)}</strong><span>MCP connector</span></span><ChevronRight size={16} className="mk-muted ml-auto" aria-hidden="true" />
      </Link></li> : <li key={server.name}><CapIcon kind="mcp" /><span className="mk-row-text"><strong>{server.name}</strong><span>Loads with this plugin · not in the Connector catalog</span></span></li>;
    })}</Group>}

    {!platform && hasSettings && <section className="mk-section">
      <h3 className="mk-section-title">Settings</h3>
      <Card className="mk-group mk-settings-card">
        <form ref={form} noValidate className="mk-form" onSubmit={event => { event.preventDefault(); submit(); }}>
          <ParameterFields declarations={plugin.parameters} drafts={values} errors={errors} redacted={installed?.redactedParameters}
            onChange={(name, value) => { setDrafts(current => ({ ...current, [name]: value })); setErrors(current => ({ ...current, [name]: "" })); }} />
          {installed && <div className="mk-actions"><Button type="submit" variant="secondary" isPending={actions.configure.isPending} isDisabled={!Object.keys(drafts).length}>Save settings</Button></div>}
        </form>
      </Card>
    </section>}
  </div>;
}

function Group({ title, count, children }: { title: string; count: number; children: React.ReactNode }) {
  return <section className="mk-section">
    <h3 className="mk-section-title">{title} <span className="mk-count">{count}</span></h3>
    <Card className="mk-group"><ul className="mk-group-list">{children}</ul></Card>
  </section>;
}
