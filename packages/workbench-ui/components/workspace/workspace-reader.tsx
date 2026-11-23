import { useRef, useState, type ComponentType, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Breadcrumbs, Button, Spinner, Tooltip } from "@heroui/react";
import { useMutation } from "@tanstack/react-query";
import { ChevronRight, CircleAlert, Download, File, FileText, Folder, FolderOpen, Link2, MousePointerClick, PanelRight, RotateCw, ServerOff, WifiOff } from "lucide-react";
import { timeAgo } from "../../lib/relative-time";
import { downloadWorkspaceFile, useAutoRefresh, useWorkspaceDirectory, useWorkspaceRefresh, workspaceFileVerdict, WorkspaceUnavailable, type WorkspaceEntry, type WorkspaceSource } from "../../lib/workspace-reading";
import { FileFacts } from "../files/file-facts";
import { FilePreview } from "../preview/file-preview";
import { textFormat } from "../preview/file-format";
import { useReadingArea, type ReadingContent } from "../shell/reading-context";
import "./workspace-reader.css";

const ROOT = "workspace";
const parentOf = (path: string) => path.split("/").slice(0, -1).join("/");

/** One of the Work's Attachments, which an inputs/ entry is: its host reads,
 * previews and downloads it as that Attachment, Preview Copy included. */
export type WorkspaceAttachmentView = ComponentType<{ name: string; toolbar: HTMLElement | null }>;

type ReaderProps = {
  source: WorkspaceSource;
  /** What the workspace belongs to, the path's first crumb. */
  label: string;
  /** The Work runs: listings refresh on their own while the page shows. */
  running: boolean;
  /** Where a closed Work's Artifacts are, shown once its workspace is gone. */
  artifactsNote: string;
  Attachment: WorkspaceAttachmentView;
  location: HTMLElement | null;
  tools: HTMLElement | null;
};

/** A Work's Session Workspace in the reading area: a floating directory card
 * over the open file, which keeps the reading area's full width. */
export function WorkspaceReader({ source, label, running, artifactsNote, Attachment, location, tools }: ReaderProps) {
  const auto = useAutoRefresh(running);
  const root = useWorkspaceDirectory(source, "", auto);
  const [selected, setSelected] = useState<string>();
  const [current, setCurrent] = useState("");
  const [cardOpen, setCardOpen] = useState(true);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const card = useRef<HTMLDivElement>(null);
  // The open file's entry comes from its folder's listing, so a refresh that
  // changes its size or time reloads it.
  const folder = useWorkspaceDirectory(source, selected ? parentOf(selected) : "", auto, Boolean(selected));
  const listed = selected ? folder.data?.entries.find(item => item.path === selected) : undefined;
  const toggle = (path: string) => setExpanded(open => { const next = new Set(open); if (next.has(path)) next.delete(path); else next.add(path); return next; });
  // A path segment opens its folder and its ancestors, and scrolls the card to it.
  const navigate = (dir: string) => {
    setSelected(undefined); setCurrent(dir); setCardOpen(true);
    setExpanded(open => { const next = new Set(open); const parts = dir.split("/").filter(Boolean); parts.forEach((_, i) => next.add(parts.slice(0, i + 1).join("/"))); return next; });
    requestAnimationFrame(() => {
      const row = dir ? card.current?.querySelector<HTMLElement>(`[data-path="${CSS.escape(dir)}"]`) : null;
      card.current?.scrollTo({ top: row ? row.offsetTop - 8 : 0 });
    });
  };
  const unavailable = root.error instanceof WorkspaceUnavailable ? root.error : undefined;
  // Once the workspace is gone, an earlier listing still cached for the open
  // file's folder must not keep offering its path or a download.
  const entry = unavailable ? undefined : listed;
  const download = useMutation({ mutationFn: (path: string) => downloadWorkspaceFile(source, path) });
  return <div className="wsb-host">
    {location && createPortal(<PathCrumbs label={label} path={unavailable ? "" : selected ?? current} onNavigate={navigate} />, location)}
    {tools && createPortal(<>
      {!unavailable && <Tooltip><Button isIconOnly size="sm" variant={cardOpen ? "tertiary" : "ghost"} aria-pressed={cardOpen} aria-label="Folders" onPress={() => setCardOpen(!cardOpen)}><PanelRight size={16} /></Button><Tooltip.Content>Folders</Tooltip.Content></Tooltip>}
      <RefreshButton scope={source.scope} auto={auto} />
      {/* An Attachment's own reading offers its download. */}
      {entry?.kind === "file" && !entry.attachmentName && <Tooltip><Button isIconOnly size="sm" variant="ghost" aria-label="Download file" isPending={download.isPending} onPress={() => download.mutate(entry.path)}><Download size={16} /></Button><Tooltip.Content>Download</Tooltip.Content></Tooltip>}
    </>, tools)}
    {download.isError && <Failure message="Could not download this file." onRetry={() => entry && download.mutate(entry.path)} />}
    {unavailable ? <Unavailable error={unavailable} scope={source.scope} artifactsNote={artifactsNote} /> : <>
      {/* The card stays put while the document scrolls. */}
      <div className="wsb-float-anchor">
        <div ref={card} className="wsb-float-card cr-card" hidden={!cardOpen}>
          <TreeLevel source={source} path="" depth={0} auto={auto} expanded={expanded} onToggle={toggle} selected={selected ?? current} onSelect={item => setSelected(item.path)} />
        </div>
      </div>
      {!entry ? <div className="wsb-center wsb-prompt"><MousePointerClick size={32} aria-hidden="true" /><p>Select a file to read it</p></div>
        : entry.attachmentName ? <Attachment key={entry.path} name={entry.attachmentName} toolbar={tools} />
        : <FilePreview key={entry.path} name={entry.name} toolbar={tools} onDownload={() => download.mutate(entry.path)}
          // A changed size or time is new content: the file reloads.
          verdictKey={["workspace", source.scope, entry.path, entry.size, entry.modifiedAt?.getTime() ?? 0]}
          loadVerdict={signal => workspaceFileVerdict(source, entry.path, signal)} />}
    </>}
  </div>;
}

/** The workspace entry in the Work's top bar; pressing it again closes the reader. */
export function WorkspaceButton({ content }: { content: ReadingContent }) {
  const reader = useReadingArea();
  const open = reader.content?.kind === content.kind;
  return <Tooltip><Button isIconOnly size="sm" variant={open ? "tertiary" : "ghost"} aria-pressed={open} aria-label="Workspace"
    onPress={() => open ? reader.close() : reader.open(content)}><FolderOpen size={16} /></Button>
    <Tooltip.Content>Workspace</Tooltip.Content></Tooltip>;
}

// Text the reader renders shows as a document; anything else as a file.
const isTextPath = (name: string) => textFormat(name).kind !== "text" || /\.(txt|log|csv|tsv)$/i.test(name) || !/\.[^.]+$/.test(name);

function EntryIcon({ entry, open }: { entry: WorkspaceEntry; open?: boolean }) {
  if (entry.kind === "directory") return open ? <FolderOpen size={15} aria-hidden="true" /> : <Folder size={15} aria-hidden="true" />;
  if (entry.kind === "link") return <Link2 size={15} aria-hidden="true" />;
  return isTextPath(entry.name) ? <FileText size={15} aria-hidden="true" /> : <File size={15} aria-hidden="true" />;
}

function RefreshButton({ scope, auto }: { scope: string; auto: boolean }) {
  const { refresh, refreshedAt, fetching } = useWorkspaceRefresh(scope);
  return <Tooltip><Button isIconOnly size="sm" variant="ghost" aria-label={`Refresh. Updated ${timeAgo(refreshedAt)}`} onPress={() => void refresh()}>
    <RotateCw size={15} className={fetching ? "wsb-spin" : undefined} /></Button>
    <Tooltip.Content className="cr-file-tooltip"><strong>Refresh</strong><span>Updated {timeAgo(refreshedAt)}</span>
      <span>{auto ? "Refreshes every 10 seconds while the Work runs" : "Not refreshing on its own: the Work isn't running or the page is hidden"}</span></Tooltip.Content></Tooltip>;
}

// The page top bar's breadcrumbs: the label is plain text, ancestors are
// links, and a long path folds its middle into an ellipsis.
function PathCrumbs({ label, path, onNavigate }: { label: string; path: string; onNavigate: (dir: string) => void }) {
  const parts = path.split("/").filter(Boolean);
  const crumbs = [{ label: ROOT, path: "" }, ...parts.map((part, index) => ({ label: part, path: parts.slice(0, index + 1).join("/") }))];
  const shown = crumbs.length > 5 ? [crumbs[0], null, ...crumbs.slice(-3)] : crumbs;
  return <Breadcrumbs aria-label="Workspace path" className="p-location">
    <Breadcrumbs.Item>{label}</Breadcrumbs.Item>
    {shown.map((crumb, index) => crumb === null
      ? <Breadcrumbs.Item key="gap"><span title={path}>…</span></Breadcrumbs.Item>
      : index === shown.length - 1
        ? <Breadcrumbs.Item key={crumb.path}><span title={crumb.label}>{crumb.label}</span></Breadcrumbs.Item>
        : <Breadcrumbs.Item key={crumb.path} onPress={() => onNavigate(crumb.path)}>{crumb.label}</Breadcrumbs.Item>)}
  </Breadcrumbs>;
}

function Unavailable({ error, scope, artifactsNote }: { error: WorkspaceUnavailable; scope: string; artifactsNote: string }) {
  const reader = useReadingArea();
  const { refresh, fetching } = useWorkspaceRefresh(scope);
  // An offline Computer comes back: its files are read again on retry.
  if (error.state === "offline") return <div className="wsb-center" role="status"><WifiOff size={28} aria-hidden="true" />
    <p>{error.message}</p>
    <Button size="sm" variant="secondary" isPending={fetching} onPress={() => void refresh()}>Retry</Button>
  </div>;
  return <div className="wsb-center" role="status"><ServerOff size={28} aria-hidden="true" />
    <p>{error.message}</p>
    {/* Closing the reader shows the context card, where the Artifacts are. */}
    {error.state !== "not_created" && <><p className="wsb-muted">{artifactsNote}</p>
      <Button size="sm" variant="secondary" onPress={reader.close}>Show Artifacts</Button></>}
  </div>;
}

function Failure({ message, onRetry }: { message: string; onRetry: () => void }) {
  return <p className="wsb-failure" role="alert"><CircleAlert size={14} aria-hidden="true" />{message}<Button size="sm" variant="ghost" onPress={onRetry}>Retry</Button></p>;
}

// A file's facts show on hover or keyboard focus; folders need none beyond
// their name.
function EntryTooltip({ entry, children }: { entry: WorkspaceEntry; children: ReactNode }) {
  if (entry.kind === "directory") return <>{children}</>;
  return <Tooltip delay={400}>{children}<Tooltip.Content placement="left" className="cr-file-tooltip">
    {entry.kind === "link" ? <><strong>{entry.name}</strong><span>Symbolic link. Links aren't followed.</span></>
      : <FileFacts name={entry.name} sizeBytes={entry.size} modifiedAt={entry.modifiedAt}>{entry.attachmentName && <span>Attachment of this Work</span>}</FileFacts>}
  </Tooltip.Content></Tooltip>;
}

// One folder's children; a nested folder loads when expanded.
function TreeLevel({ source, path, depth, auto, expanded, onToggle, selected, onSelect }: {
  source: WorkspaceSource; path: string; depth: number; auto: boolean; expanded: Set<string>; onToggle: (path: string) => void; selected?: string; onSelect: (entry: WorkspaceEntry) => void;
}) {
  const listing = useWorkspaceDirectory(source, path, auto);
  const pad = { paddingLeft: 8 + depth * 14 };
  if (listing.isPending) return <p className="wsb-tree-note" style={pad}><Spinner size="sm" /> Loading…</p>;
  if (listing.isError) return listing.error instanceof WorkspaceUnavailable ? null : <div style={pad}><Failure message="Could not list this folder." onRetry={() => void listing.refetch()} /></div>;
  if (!listing.data.entries.length) return <p className="wsb-tree-note" style={pad}>Empty folder</p>;

  return <ul role={depth === 0 ? "tree" : "group"} aria-label={depth === 0 ? "Workspace files" : undefined} className="wsb-tree">
    {listing.data.entries.map(entry => {
      const open = expanded.has(entry.path);
      const isFolder = entry.kind === "directory";
      return <li key={entry.path} role="treeitem" aria-expanded={isFolder ? open : undefined} aria-selected={selected === entry.path} aria-disabled={entry.kind === "link" || undefined}>
        {/* An open folder's row sticks below its ancestors while its contents
            scroll, so a deep or long folder can always be collapsed again. */}
        <EntryTooltip entry={entry}><Button variant="ghost" className="wsb-row" data-path={entry.path} data-sticky={isFolder && open} data-current={selected === entry.path}
          style={isFolder && open ? { ...pad, top: depth * 28 - 10 } : pad} aria-disabled={entry.kind === "link" || undefined}
          aria-label={entry.kind === "link" ? `${entry.name}, symbolic link, not followed` : entry.name}
          onPress={() => isFolder ? onToggle(entry.path) : entry.kind === "file" ? onSelect(entry) : undefined}>
          {isFolder ? <ChevronRight size={13} className="wsb-caret" data-open={open} aria-hidden="true" /> : <span className="wsb-caret" />}
          <EntryIcon entry={entry} open={open} /><span className="wsb-name">{entry.name}</span>
        </Button></EntryTooltip>
        {isFolder && open && <TreeLevel source={source} path={entry.path} depth={depth + 1} auto={auto} expanded={expanded} onToggle={onToggle} selected={selected} onSelect={onSelect} />}
      </li>;
    })}
    {listing.data.total > listing.data.entries.length && <li className="wsb-tree-note" style={pad}>Showing the first {listing.data.entries.length.toLocaleString()} of {listing.data.total.toLocaleString()} items</li>}
  </ul>;
}
