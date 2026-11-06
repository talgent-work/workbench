import { AppShell } from "@talgent-work/workbench-ui/components/shell/app-shell";
import type { PageLayoutProps } from "@talgent-work/workbench-ui/components/shell/entry-shell";
import { WorkbenchSidebar } from "./workbench-sidebar";

/** Talgent Workbench's page frame: the shell with the Workbench sidebar. */
export function WorkbenchLayout({ children, workspaceTitle = "Workbench", topActions, centerActions, width = "fluid", trail = [] }: PageLayoutProps) {
  return <AppShell title={workspaceTitle} width={width} topActions={topActions} center={centerActions} crumbs={[...trail, { label: workspaceTitle }]}
    sidebar={shell => <WorkbenchSidebar {...shell} />}>
    {children}
  </AppShell>;
}
