import { Button, Card, Typography } from "@heroui/react";
import { useContext, useEffect, useSyncExternalStore, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { sessionNeedsSignIn, useCurrentUser } from "../../lib/account-service";
import { dismiss, notify } from "../../lib/notifications";
import { sessionEnded, signInPagePath, subscribeSessionEnd } from "../../lib/session";
import { EntryShellContext } from "../shell/entry-shell";

const sessionCheckNotice = "auth:session-check";

// No usable session on this device: sign in, then come back here.
function SignInRedirect() {
  const location = useLocation();
  const home = useContext(EntryShellContext)?.home;
  return <Navigate replace to={signInPagePath(`${location.pathname}${location.search}${location.hash}`, home)} />;
}

// A re-check of a session that already loaded, as when the tab regains focus
// after the device slept, can fail while the person is still known. The page
// stays and a notice offers to check again; each new failure replaces the
// notice, and a passing check clears it.
function useSessionCheckNotice(failed: boolean, failedAt: number, retry: () => unknown) {
  useEffect(() => {
    if (!failed) return;
    notify(sessionCheckNotice, "warning", "Couldn’t check your sign-in", "This page may be out of date until the connection is back.", {
      action: { label: "Retry", onPress: () => { void retry(); } },
    });
    return () => dismiss(sessionCheckNotice);
  }, [failed, failedAt, retry]);
}

export function AuthGate({ children }: { children: ReactNode }) {
  const current = useCurrentUser();
  const ended = useSyncExternalStore(subscribeSessionEnd, sessionEnded);
  const user = current.data?.user;
  const recheckFailed = current.isError && !!user && !sessionNeedsSignIn(current.error);
  useSessionCheckNotice(recheckFailed, current.errorUpdatedAt, current.refetch);

  if (ended) {
    return <SignInRedirect />;
  }
  if (current.isLoading) {
    return <span className="sr-only" role="status">Loading profile…</span>;
  }
  if (current.isError && !recheckFailed) {
    if (!sessionNeedsSignIn(current.error)) {
      return (
        <div className="flex min-h-screen items-center justify-center p-6">
          <Card className="w-full max-w-md" role="alert">
            <Card.Header><Card.Title>Unable to load profile</Card.Title></Card.Header>
            <Card.Content><Typography type="body-sm" color="muted">Please try again.</Typography></Card.Content>
            <Card.Footer><Button variant="secondary" isPending={current.isFetching} onPress={() => { void current.refetch(); }}>Retry profile</Button></Card.Footer>
          </Card>
        </div>
      );
    }
    return <SignInRedirect />;
  }
  if (!user) {
    return <SignInRedirect />;
  }
  return <>{children}</>;
}
