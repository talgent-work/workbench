// PROTOTYPE (Applications): the frame both variants share. The breadcrumb opens with
// the Application in view, which is also the switcher. The Application in
// view is in ?app=, so it survives a variant switch.
import { useState, type ReactNode } from "react";
import { Button, Card, Dropdown, Header, Label, Separator, typographyVariants } from "@heroui/react";
import { Check, ChevronRight, ChevronsUpDown, Plus } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { AgentIcon } from "../../../components/agent/agent-icon";
import { PageLayout } from "../../../components/shell/page-layout";
import { useProtoStore, type ProtoApplication } from "./proto-data";
import { RegisterApplicationDialog } from "./proto-dialogs";
import "./application-switcher.css";

function ApplicationSwitcher({ application, applications, followed, onSwitch, onRegister }: { application: ProtoApplication; applications: ProtoApplication[]; followed: boolean; onSwitch: (id: string) => void; onRegister: () => void }) {
  return <Dropdown>
    <Button size="sm" variant="ghost" aria-label={`Application menu: ${application.name}`} className="p-app-switcher" data-followed={followed || undefined}>
      <AgentIcon icon={application.agent.icon} size={20} />
      <span className={typographyVariants({ type: "body-sm", weight: "medium", truncate: true }).base()}>{application.name}</span>
      <span className="p-app-switcher-slot" aria-hidden="true">
        <ChevronRight size={14} className="p-app-switcher-separator" />
        <ChevronsUpDown size={14} className="p-app-switcher-chevron" />
      </span>
    </Button>
    <Dropdown.Popover placement="bottom start" className="p-sidebar-menu">
      <Dropdown.Menu aria-label="Applications" onAction={key => key === "register-application" ? onRegister() : onSwitch(String(key))}>
        <Dropdown.Section aria-label="Switch application">
          <Header>Your applications</Header>
          {applications.map(item => <Dropdown.Item key={item.id} id={item.id} textValue={item.name}>
            <AgentIcon icon={item.agent.icon} size={20} /><Label>{item.name}</Label>
            {item.id === application.id && <Check size={16} aria-label="Current application" />}
          </Dropdown.Item>)}
        </Dropdown.Section>
        <Separator />
        <Dropdown.Item id="register-application" textValue="Register application"><Plus size={16} aria-hidden="true" /><Label>Register application</Label></Dropdown.Item>
      </Dropdown.Menu>
    </Dropdown.Popover>
  </Dropdown>;
}

/** `section` names a crumb after the Application, such as the tab in view. */
export function ApplicationScope({ section, center, children }: { section?: string; center?: ReactNode; children: (application: ProtoApplication, leave: () => void) => ReactNode }) {
  const { applications } = useProtoStore();
  const [params, setParams] = useSearchParams();
  const [registering, setRegistering] = useState(false);
  const selected = applications.find(application => application.id === params.get("app")) ?? applications[0];
  const select = (id: string) => setParams(current => { const next = new URLSearchParams(current); next.set("app", id); return next; }, { replace: true });
  const leave = () => setParams(current => { const next = new URLSearchParams(current); next.delete("app"); return next; }, { replace: true });
  const lead = selected && <ApplicationSwitcher application={selected} applications={applications} followed={Boolean(section)} onSwitch={select} onRegister={() => setRegistering(true)} />;
  return <PageLayout workspaceTitle={selected?.name ?? "Applications"} lead={lead} trail={selected && section ? [{ label: section, onPress: () => undefined }] : []} centerActions={selected ? center : undefined}>
    <div className="p-app-scope" hidden />
    {selected ? children(selected, leave)
      : <div className="mx-auto w-[min(640px,100%)] pt-16"><Card className="mk-group"><div className="mk-empty">
        <strong>No applications yet</strong>
        <span className="text-muted">Register a program that uses Workbench through its APIs. It gets a client ID, a secret and its own Agent.</span>
        <Button size="sm" onPress={() => setRegistering(true)}><Plus size={15} aria-hidden="true" />Register application</Button>
      </div></Card></div>}
    {registering && <RegisterApplicationDialog onClose={() => setRegistering(false)} onRegistered={select} />}
  </PageLayout>;
}
