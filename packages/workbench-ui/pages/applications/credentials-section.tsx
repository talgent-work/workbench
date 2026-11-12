import { Card, Chip } from "@heroui/react";
import { KeyRound } from "lucide-react";
import { CopyButton } from "../../components/copy-button";
import { secretLabel, type Application, type ClientSecret } from "../../lib/application-service";
import { timeAgo } from "../../lib/relative-time";
import { useNow } from "../../lib/use-now";

/** An Application's client ID and the secrets that still sign it in, never the
 * secrets themselves. */
export function CredentialsSection({ application }: { application: Application }) {
  return <section className="mk-section" aria-labelledby="ap-credentials">
    <h3 id="ap-credentials" className="mk-section-title">Credentials</h3>
    <p className="text-[14px] text-muted">Your program exchanges the client ID and a secret for a short-lived token with the OAuth 2.0 client credentials grant at <code>/auth/token</code>.</p>
    <Card className="mk-group">
      <ul className="mk-group-list" aria-label={`${application.name} credentials`}>
        <li className="!items-center">
          <KeyRound size={18} className="shrink-0 text-muted" aria-hidden="true" />
          <span className="mk-row-text min-w-0 flex-1"><strong>Client ID</strong><span><code className="break-all">{application.id}</code></span></span>
          <CopyButton text={application.id} what="client ID" />
        </li>
        {application.secrets.length === 0 && <li><span className="mk-row-text flex-1"><strong className="text-danger">No secret</strong><span>{application.name} can’t sign in.</span></span></li>}
        {application.secrets.map(secret => <SecretRow key={secret.id} secret={secret} />)}
      </ul>
    </Card>
  </section>;
}

function SecretRow({ secret }: { secret: ClientSecret }) {
  const now = useNow(30_000);
  const overlapping = secret.state === "overlapping";
  return <li className="!items-center">
    <span className={`size-2.5 shrink-0 rounded-full ${overlapping ? "bg-warning" : "bg-success"}`} aria-hidden="true" />
    <span className="mk-row-text min-w-0 flex-1">
      <strong className="flex items-center gap-2"><code>{secretLabel(secret)}</code><Chip size="sm" variant="soft" color={overlapping ? "warning" : "success"}><Chip.Label>{overlapping ? "Overlapping" : "Current"}</Chip.Label></Chip></strong>
      <span>Issued {timeAgo(secret.issuedAt, now)} · {secret.lastUsedAt ? `last used ${timeAgo(secret.lastUsedAt, now)}` : "never used"}</span>
    </span>
  </li>;
}
