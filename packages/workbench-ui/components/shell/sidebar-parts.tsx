import type { ReactNode } from "react";
import { Button, Link as HeroLink, Tooltip, buttonVariants, typographyVariants } from "@heroui/react";
import { PanelLeftClose, PanelLeftOpen, X, type LucideIcon } from "lucide-react";
import { Link } from "react-router-dom";

/** One entry of a sidebar's navigation. */
export type SidebarItem = { label: string; icon: LucideIcon; to: string; badge?: ReactNode; describedBy?: string };

/** A sidebar's navigation link: icon and label, or the icon with a tooltip when compact. */
export function SidebarLink({ item, current, compact, onCloseMobile }: { item: SidebarItem; current: boolean; compact: boolean; onCloseMobile: () => void }) {
  return <Tooltip isDisabled={!compact}>
    <HeroLink href={item.to} aria-label={item.label} aria-describedby={item.describedBy} aria-current={current ? "page" : undefined}
      className={buttonVariants({ size: "sm", variant: current ? "secondary" : "ghost", isIconOnly: compact, className: compact ? "relative" : "p-nav-item" })}
      render={props => "href" in props ? <Link {...props} to={item.to} onClick={onCloseMobile} /> : <span {...props} />}>
      <item.icon size={17} aria-hidden="true" />
      {!compact && <span className={`${typographyVariants({ type: "body-sm", color: current ? "default" : "muted" }).base()} p-nav-label`}>{item.label}</span>}
      {item.badge}
    </HeroLink>
    <Tooltip.Content placement="right">{item.label}</Tooltip.Content>
  </Tooltip>;
}

/** A sidebar's top: the brand leading to the entry's home, and collapsing,
 * expanding or closing the navigation. */
export function SidebarHeader({ compact, mobile, isReading, homeLabel, onHome, onCollapsedChange, onCloseMobile }: {
  compact: boolean; mobile: boolean; isReading: boolean; homeLabel: string; onHome: () => void; onCollapsedChange: (collapsed: boolean) => void; onCloseMobile: () => void;
}) {
  return <div className="p-sidebar-header">
    <div className="p-sidebar-brand" data-testid="sidebar-brand-row">
      <div className="p-brand-lockup">
        <Button size="sm" variant="ghost" className="p-brand-button" isIconOnly aria-label={homeLabel} onPress={onHome}><span className="p-brand-icon" aria-hidden="true" /></Button>
        {!compact && <span className="p-brand-name" aria-hidden="true" />}
      </div>
      {!compact && <Tooltip><Button variant="ghost" size="sm" isIconOnly aria-label={mobile ? "Close navigation" : "Collapse sidebar"} onPress={mobile ? onCloseMobile : () => onCollapsedChange(true)}>{mobile ? <X size={16} /> : <PanelLeftClose size={16} />}</Button><Tooltip.Content>{mobile ? "Close navigation" : "Collapse sidebar"}</Tooltip.Content></Tooltip>}
    </div>
    {compact && <Tooltip><Button variant="ghost" isIconOnly isDisabled={isReading} aria-label="Expand sidebar" onPress={() => onCollapsedChange(false)}><PanelLeftOpen size={18} /></Button><Tooltip.Content placement="right">Expand sidebar</Tooltip.Content></Tooltip>}
  </div>;
}
