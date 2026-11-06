import { useMemo } from "react";
import { Button, Popover, Separator } from "@heroui/react";
import { Box, ChartColumn, History, Library, Plus } from "lucide-react";
import { useLocation } from "react-router-dom";
import type { ShellSidebarProps } from "@talgent-work/workbench-ui/components/shell/app-shell";
import { WorkHistory, type WorkHistorySource } from "@talgent-work/workbench-ui/components/work/work-history";
import { AccountMenu } from "@talgent-work/workbench-ui/components/shell/account-menu";
import { SidebarHeader, SidebarLink, type SidebarItem } from "@talgent-work/workbench-ui/components/shell/sidebar-parts";
import { personalPathsAt } from "@talgent-work/workbench-ui/components/shell/entry-shell";
import { personalWorksKey, usePersonalWorks } from "@talgent-work/workbench-ui/lib/personal-work-service";

const paths = personalPathsAt("");
const items: SidebarItem[] = [
  { label: "New work", icon: Plus, to: paths.newWork },
  { label: "Library", icon: Library, to: paths.library },
  { label: "Marketplace", icon: Box, to: "/marketplace" },
  { label: "Usage", icon: ChartColumn, to: paths.usage },
];

const affects = [personalWorksKey];

// Talgent Workbench's navigation: what a person does on their own, Recent
// with their Personal Works, and the account at the bottom.
export function WorkbenchSidebar({ compact, mobile, isReading, onNavigate, onCollapsedChange, onCloseMobile }: ShellSidebarProps) {
  const { pathname } = useLocation();
  const works = usePersonalWorks();
  const data = useMemo(() => works.data?.slice(0, 20).map(work => ({ id: work.id, title: work.title, state: work.state, waiting: work.waiting, stateReason: work.stateReason, disconnectedSince: work.disconnectedSince, to: paths.work(work.id) })), [works.data]);
  const history: WorkHistorySource = { data, isLoading: works.isLoading, error: works.error, refetch: works.refetch, affects };
  const activeWorkId = pathname.match(/^\/works\/([^/]+)/)?.[1];
  const navLink = (item: SidebarItem) => <SidebarLink key={item.label} item={item} compact={compact} onCloseMobile={onCloseMobile}
    current={item.to === "/" ? pathname === "/" : pathname === item.to || pathname.startsWith(`${item.to}/`)} />;
  const recent = <WorkHistory history={history} activeId={activeWorkId ? decodeURIComponent(activeWorkId) : null} onNavigate={onNavigate} />;
  return <>
    <SidebarHeader compact={compact} mobile={mobile} isReading={isReading} homeLabel="Workbench home" onHome={() => onNavigate(paths.newWork)} onCollapsedChange={onCollapsedChange} onCloseMobile={onCloseMobile} />
    <div className="p-sidebar-scroll">
      <nav className="p-sidebar-nav" aria-label={mobile ? "Mobile workbench navigation" : "Workbench"}>{items.map(navLink)}</nav>
      <Separator />
      {compact ? <Popover><Button isIconOnly variant="ghost" aria-label="Recent"><History size={18} /></Button><Popover.Content placement="right top" className="p-sessions-popover"><Popover.Dialog aria-label="History">{recent}</Popover.Dialog></Popover.Content></Popover> : recent}
    </div>
    <div className="p-sidebar-footer">
      <AccountMenu compact={compact} onNavigate={onNavigate} />
    </div>
  </>;
}
