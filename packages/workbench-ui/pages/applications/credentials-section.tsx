import { useState } from "react";
import { Button, Card, Chip, Meter, Tooltip } from "@heroui/react";
import { KeyRound, RotateCw, Trash2 } from "lucide-react";
import { CopyButton } from "../../components/copy-button";
import { secretLabel, type Application, type ClientSecret } from "../../lib/application-service";
import { timeAgo, timeUntil } from "../../lib/relative-time";
import { useNow } from "../../lib/use-now";
import { RevokeSecretDialog, RotateSecretDialog } from "./secret-dialogs";

/** An Application's client ID and the secret lifecycle: one current secret and
 * at most one overlapping secret on its way out, never the secrets themselves. */
export function CredentialsSection({ application }: { application: Application }) {
  const [rotating, setRotating] = useState(false);
  const [revoking, setRevoking] = useState<ClientSecret | null>(null);
  const current = application.secrets.find(secret => secret.state === "current");
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
        {application.secrets.length === 0 && <li><span className="mk-row-text flex-1"><strong className="text-danger">No secret</strong><span>{application.name} can’t sign in. Rotate to issue one.</span></span></li>}
        {application.secrets.map(secret => <SecretRow key={secret.id} secret={secret} current={current} onRevoke={() => setRevoking(secret)} />)}
      </ul>
    </Card>
    <div className="mk-actions">
      <Button size="sm" variant="secondary" onPress={() => setRotating(true)}><RotateCw size={15} aria-hidden="true" />Rotate secret</Button>
      <p className="mk-note">Rotating keeps the replaced secret working for a limited overlap.</p>
    </div>
    {rotating && <RotateSecretDialog application={application} onClose={() => setRotating(false)} />}
    {revoking && <RevokeSecretDialog application={application} secret={revoking} onClose={() => setRevoking(null)} />}
  </section>;
}

function SecretRow({ secret, current, onRevoke }: { secret: ClientSecret; current?: ClientSecret; onRevoke: () => void }) {
  const now = useNow(30_000);
  const endsAt = secret.state === "overlapping" ? secret.overlapEndsAt : null;
  // The overlap began when the current secret replaced this one.
  const span = endsAt && current ? endsAt.getTime() - current.issuedAt.getTime() : 0;
  const left = endsAt && span > 0 ? Math.max(0, Math.min(100, ((endsAt.getTime() - now) / span) * 100)) : 0;
  const label = secretLabel(secret);
  return <li className="!items-center">
    <span className={`size-2.5 shrink-0 rounded-full ${endsAt ? "bg-warning" : "bg-success"}`} aria-hidden="true" />
    <span className="mk-row-text min-w-0 flex-1">
      <strong className="flex items-center gap-2"><code>{label}</code><Chip size="sm" variant="soft" color={endsAt ? "warning" : "success"}><Chip.Label>{endsAt ? "Overlapping" : "Current"}</Chip.Label></Chip></strong>
      <span>Issued {timeAgo(secret.issuedAt, now)} · {secret.lastUsedAt ? `last used ${timeAgo(secret.lastUsedAt, now)}` : "never used"}{endsAt && <> · ends {timeUntil(endsAt, now)}</>}</span>
      {span > 0 && <Meter aria-label={`Overlap left for ${label}`} value={left} size="sm" color="warning" className="mt-1 max-w-64"><Meter.Track><Meter.Fill /></Meter.Track></Meter>}
    </span>
    <Tooltip delay={300}><Button size="sm" variant="ghost" isIconOnly aria-label={`Revoke secret ${label}`} onPress={onRevoke}><Trash2 size={15} aria-hidden="true" /></Button><Tooltip.Content>Revoke now</Tooltip.Content></Tooltip>
  </li>;
}
