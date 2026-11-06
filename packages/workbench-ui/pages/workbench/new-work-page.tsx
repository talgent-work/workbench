import { useNavigate } from "react-router-dom";
import { PageLayout } from "../../components/shell/page-layout";
import { useEntryShell } from "../../components/shell/entry-shell";
import { WorkStartForm } from "../../components/work-start/work-start-form";
import { personalWorkStartSource, useStartPersonalWork } from "../../lib/personal-work-service";
import type { WorkStartSource } from "../../lib/work-start-source";

const source: WorkStartSource = { ...personalWorkStartSource, draft: { key: "personal", instruction: "" } };

/** New work: the start form with nothing above it but the question. A
 * Personal Work's first input names it. */
export function NewWorkPage() {
  const navigate = useNavigate();
  const { personal } = useEntryShell();
  const start = useStartPersonalWork();
  return <PageLayout workspaceTitle="New work">
    <div className="agent-grid">
      <section className="stream-panel stream-panel-starting" aria-label="Start work"><div className="wpb-stage">
        <h2 className="wpb-heading">What do you want to work on?</h2>
        <WorkStartForm source={source} onStart={async input => { navigate(personal.work(await start.mutateAsync(input))); }} />
      </div></section>
    </div>
  </PageLayout>;
}
