import { useState } from "react";
import { Alert, Button, ListBox, Modal, SearchField, Spinner } from "@heroui/react";
import { Share2 } from "lucide-react";
import { useDirectoryUsers, useGrantPersonalWorkShare, useRevokeWorkShare, useShareCandidates } from "../../lib/personal-work-service";
import { personName } from "../../lib/user-display";
import { Person } from "./person";

/** Share: the people a Personal Work's owner lets read it. Anyone in the
 * User Directory may be added; the Work stays private to the rest. */
export function ShareButton({ workId, ownerId, readerIds }: { workId: string; ownerId: string; readerIds: string[] }) {
  const [open, setOpen] = useState(false);
  return <Modal isOpen={open} onOpenChange={setOpen}>
    <Button size="sm" variant="secondary"><Share2 size={15} aria-hidden="true" />Share</Button>
    <Modal.Backdrop><Modal.Container placement="center" size="md"><Modal.Dialog>
      <Modal.CloseTrigger />
      <Modal.Header><Modal.Heading>Share this work</Modal.Heading></Modal.Header>
      <Modal.Body className="grid gap-4"><ShareReaders workId={workId} ownerId={ownerId} readerIds={readerIds} /></Modal.Body>
    </Modal.Dialog></Modal.Container></Modal.Backdrop>
  </Modal>;
}

function ShareReaders({ workId, ownerId, readerIds }: { workId: string; ownerId: string; readerIds: string[] }) {
  const [query, setQuery] = useState("");
  const candidates = useShareCandidates(query);
  const readers = useDirectoryUsers(readerIds);
  const grant = useGrantPersonalWorkShare(workId);
  const revoke = useRevokeWorkShare(workId);
  const offered = (candidates.data ?? []).filter(user => user.userId !== ownerId && !readerIds.includes(user.userId));
  const error = grant.error ?? revoke.error;
  return <>
    <p className="text-sm text-muted">People you add can read the conversation and its files. They can’t send instructions.</p>
    <SearchField aria-label="Find people" value={query} onChange={setQuery} autoFocus>
      <SearchField.Group><SearchField.SearchIcon /><SearchField.Input placeholder="Find people by username" /><SearchField.ClearButton aria-label="Clear search" /></SearchField.Group>
    </SearchField>
    {query.trim() && <div aria-live="polite">
      {candidates.isPending ? <span className="flex items-center gap-2 text-sm text-muted"><Spinner size="sm" />Searching…</span>
        : candidates.isError ? <span className="text-sm text-danger" role="alert">People couldn’t be found. <Button size="sm" variant="ghost" onPress={() => void candidates.refetch()}>Retry</Button></span>
        : offered.length === 0 ? <span className="text-sm text-muted">No one else matches “{query.trim()}”.</span>
        : <ListBox aria-label="People to add" selectionMode="none" onAction={key => { const user = offered.find(item => item.userId === key); if (user) grant.mutate(user, { onSuccess: () => setQuery("") }); }}>
          {offered.map(user => <ListBox.Item key={user.userId} id={user.userId} textValue={personName(user)}><Person user={user} /><span className="ms-auto text-xs text-muted">Add</span></ListBox.Item>)}
        </ListBox>}
    </div>}
    {error && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>{error.message}</Alert.Description></Alert.Content></Alert>}
    <section aria-label="People with access" className="grid gap-2">
      <h3 className="text-sm font-medium">People with access</h3>
      {readerIds.length === 0 ? <p className="text-sm text-muted">Only you can read this work.</p>
        : <ul className="grid gap-1">{readerIds.map(id => {
          const user = readers.data?.get(id);
          return <li key={id} className="flex items-center gap-2">
            {user ? <Person user={user} /> : <span className="text-sm text-muted">{readers.isPending ? "Loading…" : "Someone no longer in the directory"}</span>}
            <Button size="sm" variant="ghost" className="ms-auto" isPending={revoke.isPending && revoke.variables === id} onPress={() => revoke.mutate(id)} aria-label={`Remove ${user ? personName(user) : "this person"}`}>Remove</Button>
          </li>;
        })}</ul>}
    </section>
    {grant.isPending && <p className="sr-only" role="status">Sharing…</p>}
  </>;
}
