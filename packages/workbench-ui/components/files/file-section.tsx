import { useRef, useState, type ReactNode } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { Alert, Button, Dropdown, Input, Label, Modal, TextField, Tooltip } from "@heroui/react";
import { Download, FileCode2, FileImage, FileText, Link2, Link2Off, MoreHorizontal, Pencil, Replace, Trash2 } from "lucide-react";
import { ConfirmDialog } from "../marketplace/marketplace-shared";
import { RailSection } from "../rail/rail-section";
import { notify } from "../../lib/notifications";
import { shareLinkAddress } from "../../lib/share-link";
import { FileFacts } from "./file-facts";

/** One file of a File Space as its section lists it. */
export type FileRow = {
  key: string;
  name: string;
  attachment: boolean;
  sizeBytes: number;
  contentType: string;
  modifiedAt: Date | null;
  /** The Work that published an Artifact, when known. */
  publishedBy?: string;
  shared: boolean;
  /** The reader's own Share Link, when they made one. */
  ownLink?: string;
};

type Notice = { status: "success" | "warning"; title: string; description?: string };

/** What the reader may do with the files. Absent actions are not offered;
 * `rename`, `replace` and `remove` apply while the files may change. */
export type FileActions = {
  download: (row: FileRow) => Promise<unknown>;
  /** Makes a Share Link, returning its address. */
  share?: (row: FileRow) => Promise<string | undefined>;
  unshare?: (row: FileRow) => Promise<Notice>;
  rename?: (row: FileRow, newName: string) => Promise<unknown>;
  replace?: (row: FileRow, file: File) => Promise<unknown>;
  remove?: (row: FileRow) => Promise<unknown>;
  /** What deleting removes, said before the person confirms. */
  removeNote: string;
};

type FileAction = "download" | "share" | "unshare" | "rename" | "replace" | "delete";

// FileSection lists one kind of a File Space's files. Each row previews the
// file; its menu holds the file's actions.
export function FileSection({ title, rows, actions, onOpen, action }: {
  title: string; rows: FileRow[]; actions: FileActions; onOpen: (row: FileRow) => void; action?: ReactNode;
}) {
  const [hoveredFile, setHoveredFile] = useState<string | null>(null);
  const [removing, setRemoving] = useState<FileRow | null>(null);
  const [renaming, setRenaming] = useState<FileRow | null>(null);
  const replaceInput = useRef<HTMLInputElement>(null);
  const [replacing, setReplacing] = useState<FileRow | null>(null);
  // A row is busy while one of its changes is in flight.
  const [busy, setBusy] = useState<string[]>([]);
  const failed = (row: FileRow, verb: string) => (error: Error) => notify(`file-${row.key}`, "danger", `Couldn’t ${verb} ${row.name}`, error.message);
  const done = (row: FileRow, title: string, description?: string) => notify(`file-${row.key}`, "success", title, description);
  const track = <T,>(row: FileRow, work: () => Promise<T>) => {
    setBusy(keys => [...keys, row.key]);
    return work().finally(() => setBusy(keys => keys.filter(key => key !== row.key)));
  };
  const copy = (row: FileRow, url: string) => void navigator.clipboard.writeText(shareLinkAddress(url)).then(
    () => notify(`file-${row.key}`, "success", "Share link copied", `Anyone with the link can view ${row.name}.`),
    () => notify(`file-${row.key}`, "danger", "Couldn’t copy the share link", shareLinkAddress(url)));

  const run = (row: FileRow, key: FileAction) => {
    switch (key) {
      case "download":
        actions.download(row).catch(failed(row, "download"));
        break;
      case "share":
        // Copy the link the person made; otherwise make one of their own.
        if (row.ownLink) copy(row, row.ownLink);
        else actions.share?.(row).then(url => { if (url) copy(row, url); }, failed(row, "share"));
        break;
      case "unshare":
        if (actions.unshare) void track(row, () => actions.unshare!(row)).then(notice => notify(`file-${row.key}`, notice.status, notice.title, notice.description), failed(row, "stop sharing"));
        break;
      case "rename":
        setRenaming(row);
        break;
      case "replace":
        setReplacing(row);
        replaceInput.current?.click();
        break;
      case "delete":
        setRemoving(row);
        break;
    }
  };

  return <RailSection title={title} action={action}>
    <ul className="cr-list">{rows.map(row => {
      const offered: Array<{ id: FileAction; label: string; icon: typeof Download }> = [
        { id: "download", label: "Download", icon: Download },
        ...(actions.share ? [{ id: "share" as const, label: row.ownLink ? "Copy share link" : "Create share link", icon: Link2 }] : []),
        ...(actions.unshare && row.shared ? [{ id: "unshare" as const, label: "Stop sharing", icon: Link2Off }] : []),
        ...(row.attachment && actions.rename ? [{ id: "rename" as const, label: "Rename", icon: Pencil }] : []),
        ...(row.attachment && actions.replace ? [{ id: "replace" as const, label: "Replace", icon: Replace }] : []),
        ...(actions.remove ? [{ id: "delete" as const, label: "Delete", icon: Trash2 }] : []),
      ];
      return <li key={row.key} className="cr-file-row">
        <Tooltip isOpen={hoveredFile === row.key} onOpenChange={open => setHoveredFile(open ? row.key : null)}><Button onHoverStart={() => setHoveredFile(row.key)} onHoverEnd={() => setHoveredFile(null)} onFocus={() => setHoveredFile(row.key)} onBlur={() => setHoveredFile(null)} variant="ghost" className="cr-file" aria-label={`Preview ${row.name}`} onPress={() => onOpen(row)}>
          <FileIcon row={row} /><span>{row.name}</span>
        </Button><Tooltip.Content placement="left" className="cr-file-tooltip">
          <FileFacts name={row.name} sizeBytes={row.sizeBytes} modifiedAt={row.modifiedAt}>
            {row.publishedBy && <span title={row.publishedBy}>Published by Work {row.publishedBy.slice(0, 8)}</span>}
            {row.shared && <span>Shared by link</span>}
          </FileFacts>
        </Tooltip.Content></Tooltip>
        <Dropdown>
          <Tooltip><Button isIconOnly size="sm" variant="ghost" className="cr-file-more" aria-label={`Actions for ${row.name}`} isPending={busy.includes(row.key)}><MoreHorizontal size={14} /></Button><Tooltip.Content>More</Tooltip.Content></Tooltip>
          <Dropdown.Popover placement="bottom end"><Dropdown.Menu aria-label={`Actions for ${row.name}`} onAction={id => run(row, String(id) as FileAction)}>
            {offered.map(({ id, label, icon: Icon }) => <Dropdown.Item key={id} id={id} textValue={label} variant={id === "delete" ? "danger" : undefined}><Icon size={15} aria-hidden="true" /><Label>{label}</Label></Dropdown.Item>)}
          </Dropdown.Menu></Dropdown.Popover>
        </Dropdown>
      </li>;
    })}</ul>
    <input ref={replaceInput} type="file" hidden aria-label="Choose replacement file" onChange={event => {
      const chosen = event.target.files?.[0];
      event.target.value = "";
      const target = replacing;
      setReplacing(null);
      if (!chosen || !target || !actions.replace) return;
      // The new content keeps the Attachment's name and share links.
      const named = new File([chosen], target.name, { type: chosen.type });
      track(target, () => actions.replace!(target, named)).then(() => done(target, `Replaced ${target.name}`), failed(target, "replace"));
    }} />
    <ConfirmDialog isOpen={removing !== null} onOpenChange={open => { if (!open) setRemoving(null); }} title={`Delete ${removing?.name ?? "file"}?`} confirmLabel="Delete" onConfirm={() => {
      const target = removing;
      if (target && actions.remove) track(target, () => actions.remove!(target)).then(() => done(target, `Deleted ${target.name}`), failed(target, "delete"));
      setRemoving(null);
    }}>
      <p>{actions.removeNote}</p>
    </ConfirmDialog>
    {renaming && actions.rename && <RenameDialog key={renaming.key} row={renaming} rename={newName => actions.rename!(renaming, newName)} onClose={() => setRenaming(null)} />}
  </RailSection>;
}

// AttachmentConflictDialog asks before an upload replaces the Attachment of
// the same name; nothing is replaced unless the person confirms.
export function AttachmentConflictDialog({ conflict, onClose, onReplace }: { conflict: File | null; onClose: () => void; onReplace: (file: File) => void }) {
  return <ConfirmDialog isOpen={conflict !== null} onOpenChange={open => { if (!open) onClose(); }} title={`Replace ${conflict?.name ?? "attachment"}?`} confirmLabel="Replace" onConfirm={() => { if (conflict) onReplace(conflict); onClose(); }}>
    <p>An attachment with this name already exists. Replacing it keeps its share links; rename your file instead to keep both.</p>
  </ConfirmDialog>;
}

function RenameDialog({ row, rename, onClose }: { row: FileRow; rename: (newName: string) => Promise<unknown>; onClose: () => void }) {
  const [name, setName] = useState(row.name);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const trimmed = name.trim();
  const submit = () => {
    if (!trimmed || trimmed === row.name || pending) return;
    setPending(true); setError(null);
    rename(trimmed).then(onClose, (cause: unknown) => setError(cause instanceof Error ? cause : new Error(String(cause)))).finally(() => setPending(false));
  };
  return <Modal><Modal.Backdrop isOpen onOpenChange={open => { if (!open && !pending) onClose(); }} isDismissable={!pending}><Modal.Container placement="center" size="sm"><Modal.Dialog>
    <Modal.Header><Modal.Heading>Rename {row.name}</Modal.Heading></Modal.Header>
    <Modal.Body>
      <TextField value={name} onChange={setName} isInvalid={Boolean(error)} autoFocus>
        <Label>Name</Label>
        <Input onKeyDown={event => { if (event.key === "Enter") submit(); }} />
      </TextField>
      {error && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{renameErrorMessage(error)}</Alert.Description></Alert.Content></Alert>}
    </Modal.Body>
    <Modal.Footer><Button variant="secondary" isDisabled={pending} onPress={onClose}>Cancel</Button><Button isPending={pending} isDisabled={!trimmed || trimmed === row.name} onPress={submit}>Rename</Button></Modal.Footer>
  </Modal.Dialog></Modal.Container></Modal.Backdrop></Modal>;
}

function FileIcon({ row }: { row: FileRow }) {
  const kind = row.contentType === "application/pdf" ? "pdf" : row.contentType.startsWith("image/") ? "image" : row.name.endsWith(".json") ? "code" : "document";
  const Icon = kind === "image" ? FileImage : kind === "code" ? FileCode2 : FileText;
  return <Icon size={15} className="cr-file-icon" data-kind={kind} aria-hidden="true" />;
}

function renameErrorMessage(error: Error) {
  if (error instanceof ConnectError && error.code === Code.AlreadyExists) return "Another attachment already has this name.";
  if (error instanceof ConnectError && error.code === Code.InvalidArgument) return "Use one file name without slashes.";
  return error.message;
}
