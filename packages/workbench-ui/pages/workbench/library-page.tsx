import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { Alert, Button, Label, ListBox, SearchField, Select, Table } from "@heroui/react";
import { WorkFileKind, WorkFileSchema } from "@talgent/contracts/agent/public/v1/work_file_pb";
import type { LibraryFile, LibraryWork } from "@talgent/contracts/agent/public/v2/work_pb";
import { FileText } from "lucide-react";
import { Link } from "react-router-dom";
import { formatAgo } from "../../components/analytics/work-usage/work-status";
import { formatBytes } from "../../components/preview/preview-verdict";
import { PageLayout } from "../../components/shell/page-layout";
import { useEntryShell } from "../../components/shell/entry-shell";
import { useReadingArea } from "../../components/shell/reading-context";
import { useDebounced } from "../../hooks/use-debounced";
import { useNow } from "../../lib/use-now";
import { untitledWorkLabel, useDirectoryUsers, useLibrary } from "../../lib/standalone-work-service";
import { Person } from "./person";

const periods = [{ id: "any", label: "Any time", days: null }, { id: "1", label: "Last 24 hours", days: 1 }, { id: "7", label: "Last 7 days", days: 7 }, { id: "30", label: "Last 30 days", days: 30 }] as const;

/** A Work's name in the Library. */
function libraryWorkTitle(work: Pick<LibraryWork, "title"> | undefined) {
  return work?.title || untitledWorkLabel;
}

const rowKey = (file: LibraryFile) => `${file.workId}/${file.name}`;

/** Library: the Artifacts of the person's Standalone Works and of the Works
 * shared with them, searched by name and narrowed by Work and time. */
export function LibraryPage() {
  return <PageLayout workspaceTitle="Library"><LibraryTable /></PageLayout>;
}

function LibraryTable() {
  const { personal } = useEntryShell();
  const reader = useReadingArea();
  const [name, setName] = useState("");
  const [workId, setWorkId] = useState("");
  const [period, setPeriod] = useState<(typeof periods)[number]["id"]>("any");
  const query = useDebounced(name, 250);
  const library = useLibrary({ name: query, workId, addedWithinDays: periods.find(item => item.id === period)!.days });
  const works = library.data?.works ?? [];
  const files = library.data?.files ?? [];
  const sharers = useDirectoryUsers(files.map(file => file.sharedByUserId));
  const filtered = name !== "" || workId !== "" || period !== "any";
  const clear = () => { setName(""); setWorkId(""); setPeriod("any"); };
  const title = (file: LibraryFile) => file.workTitle || libraryWorkTitle(works.find(work => work.workId === file.workId));
  // A Library file is one of its Work's Artifacts.
  const open = (file: LibraryFile) => reader.open({ kind: "work-file", label: title(file), workId: file.workId,
    file: create(WorkFileSchema, { workId: file.workId, kind: WorkFileKind.ARTIFACT, name: file.name, sizeBytes: file.sizeBytes, contentType: file.contentType, updatedAt: file.addedAt }) });
  const now = useNow();
  return <div className="mx-auto flex w-full max-w-[960px] flex-col gap-6 pb-24 pt-4">
    <div className="flex flex-wrap gap-3">
      <SearchField aria-label="Find a file" value={name} onChange={setName} className="w-72"><SearchField.Group><SearchField.SearchIcon /><SearchField.Input placeholder="Find a file" /><SearchField.ClearButton aria-label="Clear search" /></SearchField.Group></SearchField>
      <Select aria-label="Work" className="w-72" value={workId || "any"} onChange={key => setWorkId(key === "any" || key == null ? "" : String(key))}>
        <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
        <Select.Popover><ListBox>
          <ListBox.Item id="any" textValue="Any Work">Any Work<ListBox.ItemIndicator /></ListBox.Item>
          {works.map(work => <ListBox.Item key={work.workId} id={work.workId} textValue={libraryWorkTitle(work)}><Label className="truncate">{libraryWorkTitle(work)}</Label><ListBox.ItemIndicator /></ListBox.Item>)}
        </ListBox></Select.Popover>
      </Select>
      <Select aria-label="Added" className="w-44" value={period} onChange={key => setPeriod((periods.find(item => item.id === key) ?? periods[0]).id)}>
        <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
        <Select.Popover><ListBox>{periods.map(item => <ListBox.Item key={item.id} id={item.id} textValue={item.label}>{item.label}<ListBox.ItemIndicator /></ListBox.Item>)}</ListBox></Select.Popover>
      </Select>
    </div>
    {library.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Your Library couldn’t be loaded.</Alert.Description></Alert.Content><Button size="sm" variant="secondary" onPress={() => void library.refetch()}>Retry</Button></Alert>}
    {/* A row opens its file; beside the reading area the table scrolls rather than squeezing its columns. */}
    <Table><Table.ScrollContainer><Table.Content aria-label="Files" className="w-full min-w-[640px]" onRowAction={key => { const file = files.find(item => rowKey(item) === key); if (file) open(file); }}>
      <Table.Header><Table.Column isRowHeader>Name</Table.Column><Table.Column>Work</Table.Column><Table.Column className="w-44">Shared by</Table.Column><Table.Column className="w-28">Added</Table.Column><Table.Column className="w-24">Size</Table.Column></Table.Header>
      <Table.Body renderEmptyState={() => <div className="flex flex-col items-center gap-2 py-8 text-sm text-muted">
        {library.isPending ? <span role="status">Loading your Library…</span> : library.isError ? null
          : filtered ? <>No files match these filters.<Button size="sm" variant="secondary" onPress={clear}>Clear filters</Button></> : "Files your Works create show up here."}
      </div>}>
        {files.map(file => {
          const sharer = file.sharedByUserId ? sharers.data?.get(file.sharedByUserId) : undefined;
          return <Table.Row key={rowKey(file)} id={rowKey(file)}>
            <Table.Cell><Button variant="ghost" size="sm" className="-ms-2 justify-start" onPress={() => open(file)} aria-label={`Preview ${file.name}`}><FileText size={16} className="text-muted" aria-hidden="true" /><span className="truncate">{file.name}</span></Button></Table.Cell>
            <Table.Cell><Link className="text-muted hover:underline" to={personal.work(file.workId)}>{title(file)}</Link></Table.Cell>
            <Table.Cell>{file.sharedByUserId && (sharer ? <Person user={sharer} /> : <span className="text-muted">Someone</span>)}</Table.Cell>
            <Table.Cell>{file.addedAt ? formatAgo(now - timestampDate(file.addedAt).getTime()) : "—"}</Table.Cell>
            <Table.Cell>{formatBytes(Number(file.sizeBytes))}</Table.Cell>
          </Table.Row>;
        })}
      </Table.Body>
    </Table.Content></Table.ScrollContainer></Table>
  </div>;
}
