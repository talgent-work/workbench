import { useState, type FormEvent } from "react";
import { Alert, Button, Card, FieldError, Input, Label, TextField } from "@heroui/react";
import { ConnectError, Code } from "@connectrpc/connect";
import { applicationNameProblem, useRenameApplication, type Application } from "../../lib/application-service";
import { notify } from "../../lib/notifications";

/** The name the owner manages the Application by. It names no Agent: the
 * Application's Agent keeps the name set on the Agent tab. */
export function SettingsSection({ application }: { application: Application }) {
  return <section className="mk-section" aria-labelledby="ap-name">
    <h3 id="ap-name" className="mk-section-title">Name</h3>
    <RenameForm key={application.name} application={application} />
  </section>;
}

function renameErrorMessage(error: unknown) {
  const code = error instanceof ConnectError ? error.code : undefined;
  if (code === Code.InvalidArgument) return "Use a name of 1–60 characters.";
  if (code === Code.NotFound) return "This application no longer exists.";
  return "The name could not be saved. Try again.";
}

function RenameForm({ application }: { application: Application }) {
  const rename = useRenameApplication();
  const [draft, setDraft] = useState(application.name);
  const name = draft.trim();
  const problem = applicationNameProblem(draft);
  const dirty = name !== application.name;
  const save = (event: FormEvent) => {
    event.preventDefault();
    if (!dirty || problem || rename.isPending) return;
    rename.mutate({ applicationId: application.id, name }, { onSuccess: () => notify("application-renamed", "success", "Application renamed") });
  };
  return <Card className="mk-group">
    <form onSubmit={save} aria-label="Application name">
      <Card.Content className="grid gap-4 p-5">
        <TextField value={draft} onChange={setDraft} isInvalid={Boolean(problem)} isDisabled={rename.isPending}>
          <Label>Name</Label>
          <Input />
          <FieldError>{problem}</FieldError>
        </TextField>
        {rename.isError && <Alert status="danger">
          <Alert.Indicator />
          <Alert.Content><Alert.Title>{renameErrorMessage(rename.error)}</Alert.Title></Alert.Content>
        </Alert>}
        <div className="flex flex-wrap items-center justify-between gap-3">
          <p className="mk-muted min-w-[16rem] flex-1 text-[13px]">Shown in your applications list. The Agent keeps its own name.</p>
          <div className="ml-auto flex gap-2">
            <Button size="sm" variant="ghost" isDisabled={!dirty || rename.isPending} onPress={() => { rename.reset(); setDraft(application.name); }}>Discard</Button>
            <Button size="sm" type="submit" isDisabled={!dirty || Boolean(problem)} isPending={rename.isPending}>{rename.isPending ? "Saving" : "Save"}</Button>
          </div>
        </div>
      </Card.Content>
    </form>
  </Card>;
}
