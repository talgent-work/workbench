import { useRef, useState, type ReactNode } from "react";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Alert, Button } from "@heroui/react";
import { WorkFileKind, type WorkFile } from "@talgent/workbench-contracts/agent/public/v1/work_file_pb";
import { Plus } from "lucide-react";
import { AttachmentConflictDialog, FileSection, type FileRow } from "../../components/files/file-section";
import { useReadingArea } from "../../components/shell/reading-context";
import { WorkTokenSection } from "../../components/work/token-usage";
import { WorkContextRail } from "../../components/work/work-context-rail";
import { useWorkProgressSection } from "../../components/work/work-progress-section";
import { useSubAgentsSection } from "../../components/work/subagents-section";
import { useCurrentUser } from "../../lib/account-service";
import { NameTakenError } from "../../lib/name-taken-error";
import type { SubAgent } from "../../lib/subagents";
import { useWorkTrace } from "../../lib/use-work-trace";
import { useCreateWorkFileShareLink, useDeleteWorkFile, useDownloadWorkFile, useRenameWorkAttachment, useStopWorkFileSharing, useUploadWorkAttachment, useWorkFiles, workFileKey } from "../../lib/work-file-service";
import type { WorkProgress } from "../../lib/work-progress";
import { isTerminalWorkLifecycle } from "../../lib/work-state-presentation";

/** A Work's context card: its Progress, Agents, files and Token Usage. Its
 * owner changes its files and reads its usage; a Work Share holder reads its
 * files. Only a Personal Work lists its files here (`withFiles`). */
export function PersonalWorkContext({ workId, title, owner, withFiles, workLifecycle, workCreatedAt, progress, subAgents }: {
  workId: string; title: string; owner: boolean; withFiles: boolean; workLifecycle: string; workCreatedAt: Date | null; progress: WorkProgress | null | undefined; subAgents: SubAgent[] | undefined;
}) {
  const reader = useReadingArea();
  const [collapsed, setCollapsed] = useState(false);
  const visible = !collapsed && !reader.content;
  const progressSection = useWorkProgressSection({ progress, scope: workId, visible });
  const subAgentsSection = useSubAgentsSection({ agents: subAgents, scope: workId, visible });
  const files = useWorkFiles(withFiles ? workId : undefined);
  const closed = workLifecycle === "closed";
  const trace = useWorkTrace({ projectId: "", agentWorkId: owner ? workId : null, workCreatedAt, isWorkTerminal: isTerminalWorkLifecycle(workLifecycle) });
  const hasTokens = owner && Boolean(trace.data?.totals.length || trace.data?.buckets.length || trace.isError);
  const listed = files.data ?? [];
  const artifacts = listed.filter(file => file.kind === WorkFileKind.ARTIFACT);
  const attachments = listed.filter(file => file.kind === WorkFileKind.ATTACHMENT);
  const editable = withFiles && owner && !closed;
  const showAttachments = attachments.length > 0 || editable;
  const upload = useUploadWorkAttachment(workId);
  // An upload refused for a taken name waits for the person to choose Replace.
  const [conflict, setConflict] = useState<File | null>(null);
  const uploadFile = (file: File, replace = false) => upload.mutate({ file, replace }, { onError: error => { if (error instanceof NameTakenError) { upload.reset(); setConflict(file); } } });
  const fileInput = useRef<HTMLInputElement>(null);
  const open = (file: WorkFile) => reader.open({ kind: "work-file", label: title, workId, file });
  const hasContent = Boolean(progress?.items.length || subAgentsSection || artifacts.length || showAttachments || hasTokens);

  return <WorkContextRail hasContent={hasContent} collapsed={collapsed} onCollapsedChange={setCollapsed} beside={!hasContent && files.isLoading && <p className="sr-only" role="status">Loading context…</p>}>
    {progressSection}
    {subAgentsSection}
    {artifacts.length > 0 && <WorkFileSection title="Artifacts" workId={workId} files={artifacts} owner={owner} editable={editable} onOpen={open} />}
    {/* Attachments stays while it can take an upload, so its heading's + is always reachable. */}
    {showAttachments && <WorkFileSection title="Attachments" workId={workId} files={attachments} owner={owner} editable={editable} onOpen={open}
      action={editable && <Button isIconOnly size="sm" variant="ghost" aria-label="Add attachment" isPending={upload.isPending} onPress={() => fileInput.current?.click()}><Plus size={16} /></Button>} />}
    <AttachmentConflictDialog conflict={conflict} onClose={() => setConflict(null)} onReplace={file => uploadFile(file, true)} />
    <input ref={fileInput} type="file" hidden aria-label="Upload attachment file" onChange={event => { const file = event.target.files?.[0]; if (file) uploadFile(file); event.target.value = ""; }} />
    {files.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Files couldn’t be loaded.</Alert.Description></Alert.Content><Button size="sm" variant="ghost" onPress={() => void files.refetch()}>Retry</Button></Alert>}
    {hasTokens && <WorkTokenSection totals={trace.data?.totals ?? []} onOpenTimeline={() => reader.open({ kind: "trace", label: title, projectId: "", workId, createdAt: workCreatedAt })} />}
    {upload.error && !(upload.error instanceof NameTakenError) && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{upload.error.message}</Alert.Description></Alert.Content></Alert>}
  </WorkContextRail>;
}

// The owner shares and changes the Work's files; a Work Share holder downloads them.
function WorkFileSection({ title, workId, files, owner, editable, onOpen, action }: {
  title: string; workId: string; files: WorkFile[]; owner: boolean; editable: boolean; onOpen: (file: WorkFile) => void; action?: ReactNode;
}) {
  const userId = useCurrentUser().data?.user?.id;
  const download = useDownloadWorkFile(workId);
  const share = useCreateWorkFileShareLink(workId);
  const unshare = useStopWorkFileSharing(workId);
  const removal = useDeleteWorkFile(workId);
  const rename = useRenameWorkAttachment(workId);
  const upload = useUploadWorkAttachment(workId);
  const byKey = new Map(files.map(file => [workFileKey(file), file]));
  const file = (row: FileRow) => byKey.get(row.key)!;
  const rows: FileRow[] = files.map(file => ({
    key: workFileKey(file), name: file.name, attachment: file.kind === WorkFileKind.ATTACHMENT, sizeBytes: Number(file.sizeBytes), contentType: file.contentType,
    modifiedAt: file.updatedAt ? timestampDate(file.updatedAt) : null, shared: file.shareLinks.length > 0, ownLink: file.shareLinks.find(link => link.createdByUserId === userId)?.url,
  }));
  return <FileSection title={title} rows={rows} onOpen={row => onOpen(file(row))} action={action} actions={{
    download: row => download.mutateAsync(file(row)),
    ...(owner && {
      share: async (row: FileRow) => (await share.mutateAsync(file(row)))?.url,
      unshare: async (row: FileRow) => { await unshare.mutateAsync(file(row)); return { status: "success" as const, title: "Sharing stopped", description: `Links to ${row.name} no longer open it.` }; },
    }),
    ...(editable && {
      rename: (row: FileRow, newName: string) => rename.mutateAsync({ name: row.name, newName }),
      replace: (_: FileRow, named: File) => upload.mutateAsync({ file: named, replace: true }),
      remove: (row: FileRow) => removal.mutateAsync(file(row)),
    }),
    removeNote: "The file is removed from this Work right away. Its share links stop working.",
  }} />;
}
