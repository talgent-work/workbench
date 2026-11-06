import { useState, type FormEvent } from "react";
import { Alert, Button, Card, Input, Label, Skeleton, TextField } from "@heroui/react";
import { CircleCheck, Monitor } from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { PageLayout } from "../../components/shell/page-layout";
import { expiryLabel, normalizeUserCode, registrationRefusal, registrationRefusalText, useApproveRegistration, useRegistration } from "../../lib/computer-service";
import { useNow } from "../../lib/use-now";
import "../../components/marketplace/marketplace.css";

/** Approving a machine's registration: `talgent-computer register` shows a
 * code and a link here; the signed-in person who approves owns the Computer. */
export function ConnectComputerPage() {
  const [search, setSearch] = useSearchParams();
  const code = normalizeUserCode(search.get("code"));
  return <PageLayout workspaceTitle="Connect a computer">
    <div className="mx-auto grid w-[min(480px,100%)] gap-6 py-6">
      {code
        ? <Registration key={code} code={code} onChangeCode={() => setSearch({}, { replace: true })} />
        : <CodeForm onSubmit={value => setSearch({ code: value })} />}
    </div>
  </PageLayout>;
}

function CodeForm({ onSubmit }: { onSubmit: (code: string) => void }) {
  const [value, setValue] = useState("");
  const code = normalizeUserCode(value);
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (code) onSubmit(code);
  };
  return <Card className="mk-group">
    <form onSubmit={submit} aria-label="Enter code">
      <Card.Content className="grid gap-4 p-5">
        <p className="text-[14px] text-muted">Enter the code <code>talgent-computer register</code> shows.</p>
        <TextField value={value} onChange={setValue} autoFocus>
          <Label>Code</Label>
          <Input placeholder="XXXX-XXXX" autoComplete="off" spellCheck={false} />
        </TextField>
        <Button type="submit" className="justify-self-start" isDisabled={!code}>Continue</Button>
      </Card.Content>
    </form>
  </Card>;
}

function Registration({ code, onChangeCode }: { code: string; onChangeCode: () => void }) {
  const registration = useRegistration(code);
  const approve = useApproveRegistration();
  const now = useNow(30_000);
  if (registration.isPending) return <Card className="mk-group" aria-busy="true"><Card.Content className="grid gap-3 p-5">
    <p className="sr-only" role="status">Checking code…</p>
    <Skeleton className="h-5 w-40 rounded" /><Skeleton className="h-4 w-24 rounded" />
  </Card.Content></Card>;
  if (registration.isError) {
    const refusal = registrationRefusal(registration.error);
    return <Refused text={registrationRefusalText[refusal]} onChangeCode={onChangeCode}
      onRetry={refusal === "failed" ? () => void registration.refetch() : undefined} isRetrying={registration.isFetching} />;
  }
  if (approve.isError && registrationRefusal(approve.error) !== "failed") {
    return <Refused text={registrationRefusalText[registrationRefusal(approve.error)]} onChangeCode={onChangeCode} />;
  }
  const { machineName, expiresAt, approved } = registration.data;
  if (approved || approve.isSuccess) return <Card className="mk-group" role="status"><Card.Content className="grid gap-3 p-5">
    <p className="flex items-center gap-2 text-[15px] font-medium"><CircleCheck size={18} className="text-success" aria-hidden="true" />Approved — return to your terminal</p>
    <p className="text-[14px] text-muted">{machineName} is now one of your computers.</p>
    <Link to="/computers" className="justify-self-start text-[14px] underline underline-offset-4">View your computers</Link>
  </Card.Content></Card>;
  return <Card className="mk-group"><Card.Content className="grid gap-4 p-5">
    <div className="flex items-center gap-3">
      <Monitor size={20} aria-hidden="true" />
      <span className="mk-row-text min-w-0">
        <strong className="truncate">{machineName}</strong>
        <span>Code {code}{expiresAt && <> · {expiryLabel(expiresAt, now)}</>}</span>
      </span>
    </div>
    <p className="text-[14px] text-muted">Approve only a machine you are setting up.</p>
    {approve.isError && <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content><Alert.Description>Approval didn’t go through. Try again.</Alert.Description></Alert.Content></Alert>}
    <div className="flex gap-2">
      <Button isPending={approve.isPending} onPress={() => approve.mutate(code)}>Approve</Button>
      <Button variant="ghost" isDisabled={approve.isPending} onPress={onChangeCode}>Use another code</Button>
    </div>
  </Card.Content></Card>;
}

function Refused({ text, onChangeCode, onRetry, isRetrying = false }: { text: string; onChangeCode: () => void; onRetry?: () => void; isRetrying?: boolean }) {
  return <Alert status="danger" role="alert"><Alert.Indicator /><Alert.Content>
    <Alert.Title>{text}</Alert.Title>
    <div className="mt-2 flex gap-2">
      {onRetry && <Button size="sm" variant="secondary" isPending={isRetrying} onPress={onRetry}>Try again</Button>}
      <Button size="sm" variant="ghost" onPress={onChangeCode}>Use another code</Button>
    </div>
  </Alert.Content></Alert>;
}
