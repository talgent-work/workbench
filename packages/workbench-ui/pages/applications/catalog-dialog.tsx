import { useState } from "react";
import { Button, Modal, SearchField, Tabs } from "@heroui/react";
import type { Connector, InstalledPlugin, PluginSummary } from "@talgent/contracts/marketplace/public/v1/marketplace_pb";
import { Check, Lock } from "lucide-react";
import { ConnectorIcon, PluginIcon } from "../../components/marketplace/marketplace-icons";
import { LoadState } from "../../components/marketplace/marketplace-shared";
import { serviceLabel } from "../../lib/marketplace-connections";
import { capabilityText, pluginPresentation } from "../../lib/marketplace-plugins";
import { missingRequired } from "../../lib/plugin-parameters";

export type CatalogKind = "plugins" | "connectors";

type Catalog<T> = { data?: T; isPending: boolean; isError: boolean; isFetching: boolean; refetch: () => unknown };

/** The Marketplace catalog, installing for the Application rather than for
 * the person: a Plugin that needs settings first opens them. */
export function CatalogDialog({ applicationName, initial, plugins, connectors, installedPlugins, required, installing, onInstallPlugin, onInstallConnector, onClose }: {
  applicationName: string; initial: CatalogKind;
  /** The entry being installed, as `plugin:<slug>` or `connector:<name>`. */
  installing: string;
  plugins: Catalog<PluginSummary[]>; connectors: Catalog<Connector[]>;
  installedPlugins: Map<string, InstalledPlugin>; required: Set<string>;
  onInstallPlugin: (plugin: PluginSummary, needsSettings: boolean) => void; onInstallConnector: (connector: Connector) => void; onClose: () => void;
}) {
  const [kind, setKind] = useState<CatalogKind>(initial);
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const pluginRows = (plugins.data ?? []).filter(plugin => `${pluginPresentation(plugin).name} ${plugin.description}`.toLowerCase().includes(q));
  const connectorRows = (connectors.data ?? []).filter(connector => !connector.required && `${serviceLabel(connector.name)} ${connector.description}`.toLowerCase().includes(q));
  const source = kind === "plugins" ? plugins : connectors;
  const rows = kind === "plugins" ? pluginRows : connectorRows;
  const installed = (label: string) => <span className="inline-flex items-center gap-1 px-2 text-[13px] text-muted" aria-label={`${label} is installed`}><Check size={14} aria-hidden="true" />Installed</span>;
  const install = (label: string, key: string, onInstall: () => void) => <Button size="sm" variant="secondary" aria-label={`Install ${label}`} isPending={installing === key}
    isDisabled={Boolean(installing) && installing !== key} onPress={onInstall}>Install</Button>;
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open) onClose(); }}><Modal.Container placement="center" size="lg" scroll="inside"><Modal.Dialog>
    <Modal.Header><Modal.Heading>Add to {applicationName}</Modal.Heading></Modal.Header>
    <Modal.Body className="grid gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <Tabs selectedKey={kind} onSelectionChange={key => setKind(key as CatalogKind)}>
          <Tabs.ListContainer><Tabs.List aria-label="Catalog">{(["plugins", "connectors"] as const).map(id => <Tabs.Tab key={id} id={id}>{id === "plugins" ? "Plugins" : "Connectors"}<Tabs.Indicator /></Tabs.Tab>)}</Tabs.List></Tabs.ListContainer>
        </Tabs>
        <SearchField aria-label="Search the catalog" value={query} onChange={setQuery} className="min-w-48 flex-1">
          <SearchField.Group><SearchField.SearchIcon /><SearchField.Input placeholder={`Search ${kind}`} /><SearchField.ClearButton aria-label="Clear search" /></SearchField.Group>
        </SearchField>
      </div>
      <LoadState noun={kind} isPending={source.isPending} isError={source.isError} hasData={Boolean(source.data)} isFetching={source.isFetching} onRetry={() => void source.refetch()} />
      {source.data && <ul className="mk-group-list" aria-label={`Catalog ${kind}`}>
        {kind === "plugins" ? pluginRows.map(plugin => {
          const { name } = pluginPresentation(plugin);
          const needsSettings = missingRequired(plugin.parameters, {}).length > 0;
          return <li key={plugin.slug} className="!items-center">
            <PluginIcon plugin={plugin} size={36} />
            <span className="mk-row-text min-w-0 flex-1"><strong>{name}</strong><span>{plugin.description}</span><span>{capabilityText(plugin)}{needsSettings && " · needs setup"}</span></span>
            {required.has(plugin.slug) ? <span className="inline-flex items-center gap-1 px-2 text-[13px] text-muted"><Lock size={14} aria-hidden="true" />Always loads</span>
              : installedPlugins.has(plugin.slug) ? installed(name) : install(name, `plugin:${plugin.slug}`, () => onInstallPlugin(plugin, needsSettings))}
          </li>;
        }) : connectorRows.map(connector => {
          const label = serviceLabel(connector.name);
          return <li key={connector.name} className="!items-center">
            <ConnectorIcon name={connector.name} size={36} />
            <span className="mk-row-text min-w-0 flex-1"><strong>{label}</strong><span>{connector.description}</span></span>
            {connector.status !== "not_installed" ? installed(label) : install(label, `connector:${connector.name}`, () => onInstallConnector(connector))}
          </li>;
        })}
        {rows.length === 0 && <li><span className="text-[13px] text-muted">{q ? `Nothing matches “${query}”.` : `The catalog has no ${kind} yet.`}</span></li>}
      </ul>}
    </Modal.Body>
    <Modal.Footer><Button onPress={onClose}>Done</Button></Modal.Footer>
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}
