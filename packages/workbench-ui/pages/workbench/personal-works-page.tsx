import { Alert, Button, Table, buttonVariants } from "@heroui/react";
import { Plus } from "lucide-react";
import { Link, useNavigate } from "react-router-dom";
import { formatAgo } from "../../components/analytics/work-usage/work-status";
import { PageLayout } from "../../components/shell/page-layout";
import { useEntryShell } from "../../components/shell/entry-shell";
import { usePersonalWorks } from "../../lib/personal-work-service";
import { useNow } from "../../lib/use-now";
import { workPresentation } from "../../lib/work-state-presentation";

/** The person's Personal Works, most recently updated first, for an entry
 * that lists them on a page of their own. */
export function PersonalWorksPage() {
  const { personal } = useEntryShell();
  const works = usePersonalWorks();
  const navigate = useNavigate();
  const now = useNow();
  return <PageLayout workspaceTitle="Personal works" topActions={<Link to={personal.newWork} className={buttonVariants({ size: "sm" })}><Plus size={16} aria-hidden="true" />New work</Link>}>
    <div className="mx-auto flex w-full max-w-[960px] flex-col gap-6 pb-24 pt-4">
      {works.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Your works couldn’t be loaded.</Alert.Description></Alert.Content><Button size="sm" variant="secondary" onPress={() => void works.refetch()}>Retry</Button></Alert>}
      <Table><Table.ScrollContainer><Table.Content aria-label="Personal works" className="w-full" onRowAction={key => navigate(personal.work(String(key)))}>
        <Table.Header><Table.Column isRowHeader>Work</Table.Column><Table.Column className="w-56">State</Table.Column><Table.Column className="w-28">Updated</Table.Column></Table.Header>
        <Table.Body renderEmptyState={() => <div className="py-8 text-center text-sm text-muted">{works.isPending ? <span role="status">Loading your works…</span> : works.isError ? null : "Works you start for yourself show up here."}</div>}>
          {(works.data ?? []).map(work => {
            const state = workPresentation(work);
            return <Table.Row key={work.id} id={work.id}>
              <Table.Cell><Link className="hover:underline" to={personal.work(work.id)}>{work.title}</Link></Table.Cell>
              <Table.Cell>{state ? <span className="flex items-center gap-1.5 whitespace-nowrap text-sm"><state.icon size={14} aria-hidden="true" />{state.label}{state.description && <span className="sr-only">. {state.description}</span>}</span> : <span className="text-muted">—</span>}</Table.Cell>
              <Table.Cell>{work.updatedAt ? formatAgo(now - work.updatedAt.getTime()) : "—"}</Table.Cell>
            </Table.Row>;
          })}
        </Table.Body>
      </Table.Content></Table.ScrollContainer></Table>
    </div>
  </PageLayout>;
}
