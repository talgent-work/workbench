import { useCallback, useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { Breadcrumbs, Button, Card, Drawer } from "@heroui/react";
import { Menu } from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";
import { coreReading } from "./core-reading";
import { ReadingContext, type ReadingContent, type ReadingRenderer } from "./reading-context";
import { ReadingPanel } from "./reading-panel";

/** What every entry's sidebar is given by the shell. */
export type ShellSidebarProps = {
  compact: boolean;
  mobile: boolean;
  isReading: boolean;
  onNavigate: (path: string) => void;
  onCollapsedChange: (collapsed: boolean) => void;
  onCloseMobile: () => void;
};

/** One level of the page location; the last one is the current page. */
export type Crumb = { label: string; to?: string; onPress?: () => void };

export interface AppShellProps {
  children: ReactNode;
  sidebar: (props: ShellSidebarProps) => ReactNode;
  /** The page's name for assistive technology. */
  title: string;
  /** What precedes the location, such as the Project switcher. */
  lead?: ReactNode;
  /** The page location; absent where the sidebar entry already names the page. */
  crumbs?: Crumb[];
  /** Centered in the top bar, such as a perspective switch. */
  center?: ReactNode;
  topActions?: ReactNode;
  hideTopline?: boolean;
  width?: "contained" | "fluid";
  /** Shown above the page, such as a closed Project's banner. */
  banner?: ReactNode;
  /** Mounted beside the page, such as the shell's dialogs. */
  overlays?: ReactNode;
  /** The entry's own readings, tried before the core's. */
  reading?: ReadingRenderer;
}

const sidebarStorageKey = "talgent.shell.sidebarCollapsed";

/** The page frame every entry shares: the collapsible sidebar and its mobile
 * drawer, the top bar with location and actions, the page, and the reading
 * area that splits off beside it. */
export function AppShell({ children, sidebar, title, lead, crumbs, center, topActions, hideTopline = false, width = "fluid", banner, overlays, reading: entryReading }: AppShellProps) {
  const [mobileOpen, setMobileOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(readStoredSidebarCollapsed);
  const location = useLocation();
  const navigate = useNavigate();
  const widthClass = width === "fluid" ? "p-page w-full max-w-none" : "p-page mx-auto w-full max-w-[1400px]";
  const routeScope = `${location.pathname}${location.search}`;
  const [selection, setSelection] = useState<{ scope: string; content: ReadingContent } | null>(null);
  const reading = selection?.scope === routeScope ? selection.content : null;
  const view = reading ? entryReading?.(reading) ?? coreReading(reading) : null;
  const [fullscreen, setFullscreen] = useState(false);
  const [narrowReader, setNarrowReader] = useState(() => window.matchMedia?.("(max-width: 740px)").matches ?? false);
  useEffect(() => {
    const media = window.matchMedia?.("(max-width: 740px)");
    if (!media) return;
    const update = () => setNarrowReader(media.matches);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  const [split, setSplit] = useState(1 / 3);
  const [container, setContainer] = useState<HTMLElement | null>(null);
  const [body, setBody] = useState<HTMLElement | null>(null);
  const [contextToolbar, setContextToolbar] = useState<HTMLDivElement | null>(null);
  const [contextToggleToolbar, setContextToggleToolbar] = useState<HTMLDivElement | null>(null);
  const [workStatusToolbar, setWorkStatusToolbar] = useState<HTMLDivElement | null>(null);
  const [toolbar, setToolbar] = useState<HTMLDivElement | null>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const openReading = (content: ReadingContent, trigger?: HTMLElement) => {
    returnFocus.current = trigger ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    setSelection({ scope: routeScope, content });
    setFullscreen(false);
  };
  const closeReading = useCallback(() => {
    setSelection(null); setFullscreen(false);
    requestAnimationFrame(() => {
      if (returnFocus.current?.isConnected) returnFocus.current.focus();
      else toolbar?.querySelector<HTMLElement>("button")?.focus();
    });
  }, [toolbar, setFullscreen, setSelection]);
  const effectiveCollapsed = Boolean(view) || sidebarCollapsed;
  const sidebarState = effectiveCollapsed ? "collapsed" : "expanded";

  useEffect(() => {
    writeStoredSidebarCollapsed(sidebarCollapsed);
  }, [sidebarCollapsed]);

  const go = (path: string) => { navigate(path); setMobileOpen(false); };
  const renderSidebar = (compact: boolean, mobile = false) => sidebar({ compact, mobile, isReading: Boolean(view), onNavigate: go, onCollapsedChange: setSidebarCollapsed, onCloseMobile: () => setMobileOpen(false) });
  const crumbLink = (crumb: Crumb) => crumb.to ? { href: crumb.to, onClick: (event: { preventDefault: () => void }) => { event.preventDefault(); navigate(crumb.to!); } }
    : crumb.onPress ? { href: "#", onClick: (event: { preventDefault: () => void }) => { event.preventDefault(); crumb.onPress!(); } } : {};

  return (
    <ReadingContext.Provider value={{ content: view ? reading : null, open: openReading, close: closeReading, workStatusToolbar, contextToolbar, contextToggleToolbar, toolbar }}><main ref={setContainer} className="p-app" data-reading={Boolean(view)} data-sidebar-state={sidebarState} style={{ "--pr-main-share": `${split}fr`, "--pr-reader-share": `${1 - split}fr` } as CSSProperties}>
      <aside className="p-sidebar" inert={Boolean(view && (fullscreen || narrowReader))} data-collapsed={effectiveCollapsed} data-sidebar-state={sidebarState} data-sidebar-tone="light" data-testid="app-sidebar" aria-label="Workspace navigation">
        <Card variant="transparent" className="p-sidebar-shell">{renderSidebar(effectiveCollapsed)}</Card>
      </aside>
      <Drawer.Backdrop isOpen={mobileOpen} onOpenChange={setMobileOpen}>
        <Drawer.Content placement="left" className="p-sidebar-drawer"><Drawer.Dialog aria-label="Workspace navigation"><div className="p-drawer-navigation">{renderSidebar(false, true)}</div></Drawer.Dialog></Drawer.Content>
      </Drawer.Backdrop>
      <section ref={setBody} className="p-body" inert={Boolean(view && (fullscreen || narrowReader))}>
        {hideTopline ? null : (
          <header className={`p-topbar ${center ? "p-dashboard-topbar" : ""}`} data-testid="app-title-header">
            <div className="p-breadcrumb">
              <Button variant="ghost" isIconOnly className="p-mobile-toggle" aria-label="Open navigation" onPress={() => setMobileOpen(true)}><Menu size={20} /></Button>
              <h1 className="sr-only">{title}</h1>
              {lead}
              {crumbs && <Breadcrumbs aria-label="Page location" className="p-location">
                {crumbs.map((crumb, index) => <Breadcrumbs.Item key={`${index}:${crumb.label}`} {...(index < crumbs.length - 1 ? crumbLink(crumb) : {})}>{crumb.label}</Breadcrumbs.Item>)}
              </Breadcrumbs>}
            </div>
            {center}
            <div className="p-actions p-page-actions" data-testid="app-title-actions">{topActions}<div className="p-context-actions" ref={setToolbar} /><div className="p-context-actions" ref={setWorkStatusToolbar} /><div className="p-context-actions" ref={setContextToolbar} /><div className="p-context-actions" ref={setContextToggleToolbar} /></div>
          </header>
        )}
        <div data-testid="app-layout-main" data-layout-width={width} data-sidebar-state={sidebarState} className={widthClass}>
          {banner}
          {children}
        </div>
      </section>
      {overlays}
      {reading && view && <ReadingPanel key={view.key} view={view} onClose={closeReading} fullscreen={fullscreen} onFullscreenChange={setFullscreen} split={split} onSplitChange={setSplit} body={body} container={container} />}
    </main></ReadingContext.Provider>
  );
}

function readStoredSidebarCollapsed() {
  try {
    return window.localStorage.getItem(sidebarStorageKey) === "true";
  } catch {
    return false;
  }
}

function writeStoredSidebarCollapsed(collapsed: boolean) {
  try {
    window.localStorage.setItem(sidebarStorageKey, collapsed ? "true" : "false");
  } catch {
    // localStorage can be unavailable in privacy-restricted browsers.
  }
}
