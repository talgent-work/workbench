import { AppShell } from "@talgent/workbench-ui/components/shell/app-shell";
import type { PageLayoutProps } from "@talgent/workbench-ui/components/shell/entry-shell";
import { WorkbenchSidebar } from "./workbench-sidebar";

/** Talgent Workbench's page frame: the shell with the Workbench sidebar. */
export function WorkbenchLayout({ children, workspaceTitle = "Workbench", topActions, centerActions, lead, width = "fluid", trail = [] }: PageLayoutProps) {
  return <AppShell title={workspaceTitle} width={width} topActions={topActions} center={centerActions} lead={lead} crumbs={lead ? trail : [...trail, { label: workspaceTitle }]}
    sidebar={shell => <WorkbenchSidebar {...shell} />}>
    {children}
  </AppShell>;
}
