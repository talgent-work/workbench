import { Avatar, Button, Dropdown, Header, Label, Separator, typographyVariants } from "@heroui/react";
import { AlertTriangle, Box, ChartColumn, Ellipsis, History, Library, LogOut, Monitor, Plus, Puzzle, UserRound } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useCurrentUser, useLogout } from "../../lib/account-service";
import type { PersonalPaths } from "./entry-shell";
import { useInstalledConnectors, useInstalledPlugins, usePluginReleases } from "../../lib/marketplace-catalog";
import { serviceLabel } from "../../lib/marketplace-connections";
import { pluginDisplayName, pluginPresentation } from "../../lib/marketplace-plugins";
import { marketplacePaths } from "../../lib/marketplace-routes";
import { installationNeedsSetup } from "../../lib/plugin-parameters";
import { safeDisplayName, safeEmail } from "../../lib/user-display";
import { ConnectorIcon, PluginIcon } from "../marketplace/marketplace-icons";

// The account sits at the sidebar bottom and owns what is about the person:
// what they installed, their profile and signing out.
const installedShown = 8;

type InstalledItem = { id: string; label: string; to: string; icon: ReactNode; disabled: boolean; needsSetup: boolean };

// Installed Plugins (by the name shown), then Installed Connectors. Loads only
// once the person reaches for the menu, and reads the release of each installed
// Plugin (to name it and mark one needing setup), never the whole catalog.
function useInstalledItems(enabled: boolean) {
  const plugins = useInstalledPlugins({ enabled });
  const connectors = useInstalledConnectors({ enabled });
  const installedPlugins = [...(plugins.data?.values() ?? [])];
  const releases = usePluginReleases(installedPlugins.map(installed => installed.slug));
  const pluginItems: InstalledItem[] = installedPlugins.map(installed => {
    const release = releases.get(installed.slug);
    return {
      id: `plugin:${installed.slug}`,
      label: release ? pluginPresentation(release).name : pluginDisplayName(installed.slug),
      to: marketplacePaths.plugin(installed.slug),
      icon: release ? <PluginIcon plugin={release} size={20} /> : <Puzzle size={16} />,
      disabled: !installed.enabled,
      needsSetup: Boolean(release && installationNeedsSetup(release.parameters, installed)),
    };
  });
  const connectorItems: InstalledItem[] = (connectors.data ?? []).map(connector => ({
    id: `connector:${connector.name}`,
    label: serviceLabel(connector.name),
    to: marketplacePaths.connector(connector.name),
    icon: <ConnectorIcon name={connector.name} size={20} />,
    disabled: connector.status === "disabled",
    needsSetup: Boolean(connector.provider && connector.providerConnection !== "connected"),
  }));
  pluginItems.sort((a, b) => a.label.localeCompare(b.label));
  return {
    items: [...pluginItems, ...connectorItems],
    isLoading: enabled && (plugins.isPending || connectors.isPending) && !(plugins.isError || connectors.isError),
    isError: plugins.isError || connectors.isError,
    retry: () => { void plugins.refetch(); void connectors.refetch(); },
  };
}

const personalItems = [
  { id: "personal-new", label: "New work", icon: Plus, to: (paths: PersonalPaths) => paths.newWork },
  { id: "personal-works", label: "Personal works", icon: History, to: (paths: PersonalPaths) => paths.works },
  { id: "personal-library", label: "Library", icon: Library, to: (paths: PersonalPaths) => paths.library },
  { id: "personal-usage", label: "Usage", icon: ChartColumn, to: (paths: PersonalPaths) => paths.usage },
] as const;

/** `personal` adds the person's own pages where the entry's navigation has
 * no place for them. */
export function AccountMenu({ compact, onNavigate, personal }: { compact: boolean; onNavigate: (path: string) => void; personal?: PersonalPaths }) {
  const user = useCurrentUser().data?.user;
  const logout = useLogout();
  // Hovering or focusing the trigger starts loading, so the menu rarely opens empty.
  const [wanted, setWanted] = useState(false);
  const { items: installed, isLoading, isError, retry } = useInstalledItems(wanted);
  const email = safeEmail(user?.primaryEmail);
  const name = safeDisplayName(user?.displayName, user?.id, email || "Profile");
  const initial = name.trim().charAt(0).toUpperCase() || "P";
  return (
    <Dropdown onOpenChange={open => { if (open) setWanted(true); }}>
      <Button variant="ghost" className={compact ? undefined : "p-sidebar-account"} isIconOnly={compact} aria-label={`Account menu: ${name}`}
        onHoverStart={() => setWanted(true)} onFocus={() => setWanted(true)}>
        <Avatar size="sm" className="size-6 shrink-0 rounded-full" aria-hidden="true">
          {user?.avatarUrl && <Avatar.Image src={user.avatarUrl} alt="" />}
          <Avatar.Fallback>{initial}</Avatar.Fallback>
        </Avatar>
        {!compact && <><span className={`${typographyVariants({ type: "body-sm", weight: "medium", truncate: true }).base()} p-sidebar-account-name`}>{name}</span><Ellipsis size={14} aria-hidden="true" /></>}
      </Button>
      <Dropdown.Popover placement="top start" className="p-sidebar-menu">
        <Dropdown.Menu aria-label="Account" onAction={key => {
          const own = personal && personalItems.find(item => item.id === key);
          if (own) onNavigate(own.to(personal));
          else if (key === "profile") onNavigate("/profile");
          else if (key === "computers") onNavigate("/computers");
          else if (key === "sign-out") logout.mutate();
          else if (key === "installed-retry") retry();
          else { const item = installed.find(entry => entry.id === key); if (item) onNavigate(item.to); }
        }}>
          {installed.length > 0 && <Dropdown.Section aria-label="Installed">
            <Header>Installed</Header>
            {installed.slice(0, installedShown).map(item => <InstalledEntry key={item.id} item={item} />)}
            {/* The rest open beside the menu; the entry is marked when one of them needs attention. */}
            {installed.length > installedShown && <Dropdown.SubmenuTrigger>
              <Dropdown.Item id="all-installed" textValue={`All installed (${installed.length})`}><Box size={16} aria-hidden="true" /><Label>All installed ({installed.length})</Label>
                {installed.slice(installedShown).some(item => item.needsSetup) && <AlertTriangle size={13} className="ml-auto shrink-0 text-warning" aria-label="Needs attention" />}
                <Dropdown.SubmenuIndicator /></Dropdown.Item>
              <Dropdown.Popover className="p-sidebar-menu max-h-[60vh] overflow-y-auto">
                <Dropdown.Menu aria-label="All installed" onAction={key => { const item = installed.find(entry => entry.id === key); if (item) onNavigate(item.to); }}>
                  {installed.slice(installedShown).map(item => <InstalledEntry key={item.id} item={item} />)}
                </Dropdown.Menu>
              </Dropdown.Popover>
            </Dropdown.SubmenuTrigger>}
          </Dropdown.Section>}
          {isLoading && <Dropdown.Item id="installed-loading" textValue="Loading installed" isDisabled><Label>Loading installed…</Label></Dropdown.Item>}
          {isError && <Dropdown.Item id="installed-retry" textValue="Installed couldn’t be loaded. Retry"><AlertTriangle size={16} className="text-warning" aria-hidden="true" /><Label>Installed couldn’t be loaded. Retry</Label></Dropdown.Item>}
          {(installed.length > 0 || isLoading || isError) && <Separator />}
          {personal && <Dropdown.Section aria-label="Personal">
            <Header>Personal</Header>
            {personalItems.map(({ id, label, icon: Icon }) => <Dropdown.Item key={id} id={id} textValue={label}><Icon size={16} aria-hidden="true" /><Label>{label}</Label></Dropdown.Item>)}
          </Dropdown.Section>}
          {personal && <Separator />}
          <Dropdown.Item id="profile" textValue="Edit profile"><UserRound size={16} aria-hidden="true" /><Label>Edit profile</Label></Dropdown.Item>
          <Dropdown.Item id="computers" textValue="Computers"><Monitor size={16} aria-hidden="true" /><Label>Computers</Label></Dropdown.Item>
          <Separator />
          <Dropdown.Item id="sign-out" textValue="Sign out" isDisabled={logout.isPending}><LogOut size={16} aria-hidden="true" /><Label>Sign out</Label></Dropdown.Item>
        </Dropdown.Menu>
      </Dropdown.Popover>
    </Dropdown>
  );
}

function InstalledEntry({ item }: { item: InstalledItem }) {
  return <Dropdown.Item id={item.id} textValue={item.label} className={item.disabled ? "opacity-50" : undefined}>
    <span className="flex size-5 shrink-0 items-center justify-center" aria-hidden="true">{item.icon}</span>
    <Label>{item.label}</Label>
    {item.disabled && <span className="sr-only">Disabled</span>}
    {item.needsSetup && <AlertTriangle size={13} className="ml-auto shrink-0 text-warning" aria-label="Needs attention" />}
  </Dropdown.Item>;
}
