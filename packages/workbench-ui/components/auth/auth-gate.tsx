import { Button, Card, Typography } from "@heroui/react";
import { Code, ConnectError } from "@connectrpc/connect";
import { useContext, useSyncExternalStore, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useCurrentUser } from "../../lib/account-service";
import { sessionEnded, signInPagePath, subscribeSessionEnd } from "../../lib/session";
import { EntryShellContext } from "../shell/entry-shell";

// No usable session on this device: sign in, then come back here.
function SignInRedirect() {
  const location = useLocation();
  const home = useContext(EntryShellContext)?.home;
  return <Navigate replace to={signInPagePath(`${location.pathname}${location.search}${location.hash}`, home)} />;
}

export function AuthGate({ children }: { children: ReactNode }) {
  const current = useCurrentUser();
  const ended = useSyncExternalStore(subscribeSessionEnd, sessionEnded);

  if (ended) {
    return <SignInRedirect />;
  }
  if (current.isLoading) {
    return <span className="sr-only" role="status">Loading profile…</span>;
  }
  if (current.isError) {
    if (current.error instanceof ConnectError && current.error.code !== Code.Unauthenticated) {
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
  if (!current.data?.user) {
    return <SignInRedirect />;
  }
  return <>{children}</>;
}
