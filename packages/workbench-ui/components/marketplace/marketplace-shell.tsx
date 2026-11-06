import { Tabs } from "@heroui/react";
import type { ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { PageLayout } from "../shell/page-layout";
import { marketplacePaths } from "../../lib/marketplace-routes";
import "./marketplace.css";

export type Crumb = { label: string; to: string };

const sections = [{ id: "plugins", label: "Plugins", to: marketplacePaths.plugins }, { id: "connectors", label: "Connectors", to: marketplacePaths.connectors }] as const;

// Marketplace is the Plugin and Connector catalog only: its sections
// switch in the topbar, what the person installed is in the account menu, and
// Provider Authorizations are on Profile. A Provider page keeps this shell
// with Profile as its parent and no catalog switch.
export function MarketplaceShell({ title, trail = [], root = { label: "Marketplace", to: marketplacePaths.plugins }, children }: {
  title: string; trail?: Crumb[]; root?: Crumb; children: ReactNode;
}) {
  const navigate = useNavigate();
  const { pathname } = useLocation();
  const current = pathname.startsWith(marketplacePaths.connectors) ? "connectors" : "plugins";
  // The switch shows on the two lists only; a category or detail page leads back by its breadcrumb.
  const onList = pathname === marketplacePaths.plugins || pathname === marketplacePaths.connectors;
  const tabs = <Tabs className="p-perspective-tabs" selectedKey={current} onSelectionChange={key => navigate(sections.find(section => section.id === key)!.to)}>
    <Tabs.ListContainer><Tabs.List aria-label="Marketplace section">{sections.map(section => <Tabs.Tab key={section.id} id={section.id}>{section.label}<Tabs.Indicator /></Tabs.Tab>)}</Tabs.List></Tabs.ListContainer>
  </Tabs>;
  return <PageLayout workspaceTitle={title} centerActions={onList ? tabs : undefined} trail={[root, ...trail].map(crumb => ({ label: crumb.label, onPress: () => navigate(crumb.to) }))}>
    <div className="mk-market">
      <div className="mk-main">{children}</div>
    </div>
  </PageLayout>;
}
