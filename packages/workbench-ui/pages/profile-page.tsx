import { Avatar, Button, Tooltip } from "@heroui/react";
import { Info, LogOut } from "lucide-react";
import { PageLayout } from "../components/shell/page-layout";
import { ProviderRows } from "../components/marketplace/provider-pages";
import { useProviderAuthorizationCallback } from "../components/profile/provider-callback";
import { WorkAgentAppearance } from "../components/profile/work-agent-appearance";
import { WorkAgentSoul } from "../components/profile/work-agent-soul";
import { useCurrentUser, useLogout } from "../lib/account-service";
import { avatarInitial, safeAvatarUrl, safeDisplayName, safeEmail } from "../lib/user-display";
import "../components/marketplace/marketplace.css";

// Profile holds everything about the person: who they are, as GitHub
// reports it; the Work Agent that represents them and its Soul; the Provider Authorizations
// their agents act with; and signing out of this device, beside who they are so
// it stays in reach however many Providers follow.
export function ProfilePage() {
  const connecting = useProviderAuthorizationCallback();
  const current = useCurrentUser();
  const logout = useLogout();
  const user = current.data?.user;
  const email = safeEmail(user?.primaryEmail);
  const displayName = safeDisplayName(user?.displayName, user?.id, email || "Profile");
  const username = safeDisplayName(user?.username, user?.id, "");
  const avatarUrl = safeAvatarUrl(user?.avatarUrl);
  if (connecting) return <PageLayout workspaceTitle="Profile"><p className="sr-only" role="status">Connecting your account…</p></PageLayout>;
  return (
    <PageLayout workspaceTitle="Profile">
      <div className="mx-auto grid w-[min(640px,100%)] gap-8 py-6">
        <header className="mk-detail-head !items-center">
          <Avatar aria-label={`${displayName} avatar`} className="size-16 shrink-0 rounded-full">
            {avatarUrl && <Avatar.Image src={avatarUrl} alt="" />}
            <Avatar.Fallback className="text-[26px] font-semibold">{avatarInitial(displayName, email, "P")}</Avatar.Fallback>
          </Avatar>
          <div className="mk-detail-title">
            <div className="flex items-center gap-2">
              <h2>{displayName}</h2>
              <Tooltip delay={200}>
                <Tooltip.Trigger aria-label="Where these details come from" className="rounded-full text-muted"><Info size={15} aria-hidden="true" /></Tooltip.Trigger>
                <Tooltip.Content>From your GitHub profile; updated each time you sign in.</Tooltip.Content>
              </Tooltip>
            </div>
            <p className="flex flex-wrap gap-x-3 text-[14px] text-muted">{username && <span>@{username}</span>}{email && <span>{email}</span>}</p>
          </div>
          <div className="mk-detail-actions">
            <Tooltip delay={300}>
              <Button size="sm" variant="secondary" isPending={logout.isPending} onPress={() => logout.mutate()}>
                <LogOut size={15} aria-hidden="true" />{logout.isPending ? "Signing out" : "Sign out"}
              </Button>
              <Tooltip.Content>Signs out this device only; other devices stay signed in.</Tooltip.Content>
            </Tooltip>
          </div>
        </header>
        <WorkAgentAppearance />
        <WorkAgentSoul />
        <section className="mk-section" aria-labelledby="pf-providers">
          <div className="flex items-center gap-2">
            <h3 id="pf-providers" className="mk-section-title">Providers</h3>
            <Tooltip delay={200}>
              <Tooltip.Trigger aria-label="About Providers" className="rounded-full text-muted"><Info size={15} aria-hidden="true" /></Tooltip.Trigger>
              <Tooltip.Content>Accounts your agents act with in Works you start. Connecting installs nothing.</Tooltip.Content>
            </Tooltip>
          </div>
          <ProviderRows />
        </section>
      </div>
    </PageLayout>
  );
}
