import { useState } from "react";
import { Button, Card, Tabs } from "@heroui/react";
import { AppWindow, Plus } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { LoadState } from "../../components/marketplace/marketplace-shared";
import { ObjectSwitcher } from "../../components/shell/object-switcher";
import { PageLayout } from "../../components/shell/page-layout";
import { useOwnedApplications, type Application } from "../../lib/application-service";
import { AgentSection } from "./agent-section";
import { CredentialsSection } from "./credentials-section";
import { RegisterApplicationDialog } from "./register-application-dialog";
import { SettingsSection } from "./settings-section";
import "../../components/marketplace/marketplace.css";

/** The sections of an Application's page, switched by the topbar tabs. */
const sections = [{ id: "agent", label: "Agent" }, { id: "credentials", label: "Credentials" }, { id: "settings", label: "Settings" }] as const;
type SectionId = typeof sections[number]["id"];
/** A page without ?section= opens where a new Application's owner goes first:
 * its credentials. */
const defaultSection: SectionId = "credentials";

const applicationIcon = <AppWindow size={18} className="shrink-0 text-muted" aria-hidden="true" />;

/** Applications: the programs the person registered to use Workbench through
 * its APIs. The Application in view heads the breadcrumb as its switcher and
 * is kept in ?app=; its sections are the topbar tabs. */
export function ApplicationsPage() {
  const applications = useOwnedApplications();
  const [params, setParams] = useSearchParams();
  const [registering, setRegistering] = useState(false);
  const list = applications.data ?? [];
  const selected = list.find(application => application.id === params.get("app")) ?? list[0];
  const sectionId: SectionId = sections.find(item => item.id === params.get("section"))?.id ?? defaultSection;
  const section = sections.find(item => item.id === sectionId)!;
  const update = (change: (next: URLSearchParams) => void) => setParams(current => { const next = new URLSearchParams(current); change(next); return next; }, { replace: true });
  const select = (id: string) => update(next => next.set("app", id));
  const register = { id: "register-application", label: "Register application", icon: <Plus size={16} aria-hidden="true" />, onPress: () => setRegistering(true) };

  const lead = selected && <ObjectSwitcher noun="Application" followed
    current={{ id: selected.id, name: selected.name, icon: applicationIcon }}
    objects={list.map(item => ({ id: item.id, name: item.name, icon: applicationIcon }))}
    onSwitch={select} actions={[register]} />;
  const tabs = selected && <Tabs className="p-perspective-tabs" selectedKey={sectionId} onSelectionChange={key => update(next => next.set("section", String(key)))}>
    <Tabs.ListContainer><Tabs.List aria-label="Application section">{sections.map(item => <Tabs.Tab key={item.id} id={item.id}>{item.label}<Tabs.Indicator /></Tabs.Tab>)}</Tabs.List></Tabs.ListContainer>
  </Tabs>;

  return <PageLayout workspaceTitle={selected?.name ?? "Applications"} lead={lead} trail={selected ? [{ label: section.label, onPress: () => undefined }] : []} centerActions={tabs}>
    <div className="mx-auto grid w-[min(960px,100%)] gap-8 pb-24 pt-4">
      <LoadState noun="your applications" isPending={applications.isPending} isError={applications.isError} hasData={Boolean(applications.data)} isFetching={applications.isFetching} onRetry={() => void applications.refetch()} />
      {applications.data && (selected ? <ApplicationSection key={selected.id} application={selected} section={sectionId} /> : <NoApplications onRegister={() => setRegistering(true)} />)}
    </div>
    {registering && <RegisterApplicationDialog onClose={() => setRegistering(false)} onRegistered={select} />}
  </PageLayout>;
}

function ApplicationSection({ application, section }: { application: Application; section: SectionId }) {
  switch (section) {
    case "agent": return <AgentSection application={application} />;
    case "credentials": return <CredentialsSection application={application} />;
    case "settings": return <SettingsSection application={application} />;
  }
}

function NoApplications({ onRegister }: { onRegister: () => void }) {
  return <div className="mx-auto w-[min(640px,100%)] pt-12"><Card className="mk-group"><div className="mk-empty">
    <strong>No applications yet</strong>
    <span className="text-muted">Register a program that uses Workbench through its APIs. It gets a client ID and a secret, and calls Workbench as itself.</span>
    <Button size="sm" onPress={onRegister}><Plus size={15} aria-hidden="true" />Register application</Button>
  </div></Card></div>;
}
