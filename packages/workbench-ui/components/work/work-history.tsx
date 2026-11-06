import { workPresentation } from "../../lib/work-state-presentation";
import { useRef, useState } from "react";
import { typographyVariants, Button, Chip, Dropdown, Label, ListBox, Modal, Spinner, Tooltip, Typography } from "@heroui/react";
import { Archive, Circle, Ellipsis, Folder, Pause, Play } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Code, ConnectError } from "@connectrpc/connect";
import { agentWorkClient } from "../../lib/client";
import { agentWorkQueryKey } from "../../lib/agent-work-service";
import { isStaleWorkControl, workControlErrorMessage } from "../../lib/work-projection";

/** One recent Work: what it is for, its state and why it stopped, since when
 * it is disconnected (when it is) and where it opens. */
export type WorkHistoryRow = { id: string; title: string; state: string; waiting: boolean; stateReason?: string; disconnectedSince?: Date | null; to: string };

/** The recent Works as the entry lists them, and what refreshes once one is controlled. */
export type WorkHistorySource = {
  data: WorkHistoryRow[] | undefined;
  isLoading: boolean;
  error: Error | null;
  refetch: () => unknown;
  /** Query keys whose answers change with a Work's state. */
  affects: ReadonlyArray<readonly unknown[]>;
};

/** Recent in the sidebar: the person's recent Works, each opening its page,
 * with the owner's stop, resume and archive. */
export function WorkHistory({ history, activeId, onNavigate }: { history: WorkHistorySource; activeId: string | null; onNavigate: (path: string) => void }) {
  const [hoverId, setHoverId] = useState<string | null>(null);
  const [menuId, setMenuId] = useState<string | null>(null);
  const [archiveTarget, setArchiveTarget] = useState<WorkHistoryRow | null>(null);
  const inFlight = useRef(false);
  const queryClient = useQueryClient();
  const targetId = archiveTarget?.id ?? menuId;
  const snapshot = useQuery({
    queryKey: agentWorkQueryKey(targetId),
    queryFn: () => agentWorkClient.getWork({ workId: targetId ?? "" }),
    enabled: Boolean(targetId),
    staleTime: 0,
    retry: false,
    refetchInterval: targetId ? 5000 : false,
  });
  const control = useMutation({
    mutationFn: async ({ id, action }: { id: string; action: "stop" | "resume" | "archive" }) => {
      if (inFlight.current) return;
      inFlight.current = true;
      try {
        // The version of the state this menu offered; a changed Work is refused.
        const request = { workId: id, expectedVersion: snapshot.data?.work?.version };
        if (action === "stop") await agentWorkClient.stopWork(request);
        else if (action === "resume") await agentWorkClient.resumeWork(request);
        else await agentWorkClient.archiveWork(request);
        if (action === "archive") setArchiveTarget(null);
      } catch (cause) {
        if (action === "archive" && isStaleWorkControl(cause)) setArchiveTarget(null);
        throw cause;
      } finally { inFlight.current = false; }
    },
    onSettled: async (_, __, { id }) => {
      await Promise.all([agentWorkQueryKey(id), ...history.affects].map(queryKey => queryClient.invalidateQueries({ queryKey })));
    },
  });
  const session = snapshot.data?.work;
  const allowed = snapshot.isError || snapshot.isFetching || control.isPending || session?.archivePending || session?.resumePending ? [] : session?.allowedActions ?? [];
  const accessLost = history.error && [Code.PermissionDenied, Code.Unauthenticated].includes(ConnectError.from(history.error).code);
  const rows = accessLost ? [] : history.data ?? [];
  return <section className="p-session-history" aria-label="History">
    {/* Archived Works stay in Recent, marked by their Closed state. */}
    <Typography type="body-sm" color="muted" className="p-history-heading">Recent</Typography>
    {history.error && <div role="alert"><Typography type="body-sm">History unavailable</Typography><Button size="sm" variant="ghost" onPress={() => { void history.refetch(); }}>Retry history</Button></div>}
    {control.isError && !archiveTarget && <Typography type="body-sm" className="text-danger" role="alert">{workControlErrorMessage(control.error)}</Typography>}
    {/* Selection marks the open Work, so choosing a row navigates on selection
        change: with toggle selection a click on a list that already has a
        selected row changes selection and never fires onAction. */}
    <div className="p-history-list"><div className="p-session-rows" onMouseLeave={() => setHoverId(null)} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setHoverId(null); }}>
      <ListBox className="p-session-list" aria-label="Recent" selectionMode="single" disallowEmptySelection selectedKeys={activeId && rows.some(row => row.id === activeId) ? [activeId] : []} onSelectionChange={keys => { const id = keys === "all" ? undefined : [...keys][0]; const row = rows.find(item => item.id === id); if (row && row.id !== activeId) onNavigate(row.to); }} renderEmptyState={() => <Typography type="body-sm" color="muted" className={history.isLoading ? "sr-only" : "p-sidebar-empty"}>{history.isLoading ? "Loading history…" : history.error ? "" : "No recent work"}</Typography>}>
        {rows.map(row => {
          // A forgotten question outranks the lifecycle, so the row asks for the
          // person even after the Work stopped.
          const presentation = workPresentation(row);
          const Icon = presentation?.icon ?? Circle;
          const label = presentation?.label ?? "Unknown state";
          return <ListBox.Item key={row.id} id={row.id} textValue={row.title} className="p-session-item" data-waiting={row.waiting || undefined} data-actions-visible={hoverId === row.id || menuId === row.id} onHoverStart={() => setHoverId(row.id)} onFocus={() => setHoverId(row.id)}>
            <Chip size="sm" variant="soft" color={presentation?.color ?? "default"} aria-hidden="true">{!row.waiting && !row.disconnectedSince && (row.state === "starting" || row.state === "stopping") ? <Spinner size="sm" color="current" /> : <Icon size={16} />}</Chip>
            <Tooltip><Tooltip.Trigger role="presentation" tabIndex={-1} className="p-session-label"><Label><span className={typographyVariants({ type: "body-sm", color: row.id === activeId || row.waiting ? "default" : "muted", truncate: true }).base()} >{row.title}<span className="sr-only"> · {label}{presentation?.description && `. ${presentation.description}`}</span></span></Label></Tooltip.Trigger><Tooltip.Content placement="right" offset={48} className="p-session-tooltip">{row.title} · {label}{presentation?.description && <span className="block text-muted">{presentation.description}</span>}</Tooltip.Content></Tooltip>
          </ListBox.Item>;
        })}
      </ListBox>
      <div className="p-session-actions">{rows.map(row => <div key={row.id} className="p-session-action" data-visible={hoverId === row.id || menuId === row.id}>
        <Dropdown onOpenChange={open => { setMenuId(open ? row.id : null); setHoverId(row.id); }}>
          <Button size="sm" variant="ghost" isIconOnly aria-label={`Actions for ${row.id}`}><Ellipsis size={16} /></Button>
          <Dropdown.Popover><Dropdown.Menu aria-label={`Actions for ${row.id}`} onAction={key => {
            if (key === "open") onNavigate(row.to);
            else if (key === "retry") void snapshot.refetch();
            else if (key === "archive") { control.reset(); setArchiveTarget(row); }
            else if ((key === "stop" || key === "resume") && allowed.includes(key)) control.mutate({ id: row.id, action: key });
          }}>
            <Dropdown.Item id="open" textValue="Open work"><Folder size={16} /><Label>Open work</Label></Dropdown.Item>
            {snapshot.isFetching && <Dropdown.Item id="loading" isDisabled textValue="Checking actions"><Spinner size="sm" /><Label>Checking actions</Label></Dropdown.Item>}
            {snapshot.isError && <Dropdown.Item id="retry" textValue="Retry actions"><Label>Retry actions</Label></Dropdown.Item>}
            {allowed.includes("resume") && <Dropdown.Item id="resume" textValue="Resume work"><Play size={16} /><Label>Resume work</Label></Dropdown.Item>}
            {allowed.includes("stop") && <Dropdown.Item id="stop" textValue="Stop work"><Pause size={16} /><Label>Stop work</Label></Dropdown.Item>}
            {allowed.includes("archive") && <Dropdown.Item id="archive" textValue="Archive work"><Archive size={16} /><Label>Archive work</Label></Dropdown.Item>}
          </Dropdown.Menu></Dropdown.Popover>
        </Dropdown>
      </div>)}</div>
    </div></div>
    <Modal><Modal.Backdrop isOpen={Boolean(archiveTarget)} onOpenChange={open => { if (!open && !control.isPending) setArchiveTarget(null); }} isDismissable={!control.isPending} isKeyboardDismissDisabled={control.isPending}>
      <Modal.Container placement="center" size="sm"><Modal.Dialog>
        <Modal.Header><Modal.Heading>Archive work?</Modal.Heading></Modal.Header>
        <Modal.Body><Typography type="body-sm">Archive “{archiveTarget?.title}”? This permanently closes the conversation and removes its working files. Conversation history, attachment originals, published files and your home are kept.</Typography>{control.isError && <Typography type="body-sm" className="text-danger" role="alert">{workControlErrorMessage(control.error)}</Typography>}{snapshot.isError && <Button variant="ghost" onPress={() => { void snapshot.refetch(); }}>Retry actions</Button>}</Modal.Body>
        <Modal.Footer><Button autoFocus variant="secondary" isDisabled={control.isPending} onPress={() => setArchiveTarget(null)}>Cancel</Button><Button variant="danger" isPending={control.isPending} isDisabled={!allowed.includes("archive")} onPress={() => { if (archiveTarget && !inFlight.current) control.mutate({ id: archiveTarget.id, action: "archive" }); }}>Archive work</Button></Modal.Footer>
      </Modal.Dialog></Modal.Container>
    </Modal.Backdrop></Modal>
  </section>;
}
