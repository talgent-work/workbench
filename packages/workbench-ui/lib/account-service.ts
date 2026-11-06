import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation, useQueryClient, useQuery } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { sessionClient } from "./client";
import { dismiss, notify } from "./notifications";
import { signOut } from "./session";
import { safeAvatarUrl, safeDisplayName, safeEmail } from "./user-display";

export const currentUserQueryKey = ["account", "current-user"] as const;

export type CurrentUser = {
  id: string;
  userId: string;
  username: string;
  displayName: string;
  primaryEmail: string;
  avatarUrl: string;
  firstName?: string;
  lastName?: string;
  nickName?: string;
  preferredLanguage?: string;
  gender?: string;
};

type CurrentUserView = {
  user?: CurrentUser;
};

// Account ids are canonical lowercase UUIDs, the only subject the gateway admits.
function isAccountId(value: string | undefined): value is string {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value?.trim() ?? "");
}

export function useCurrentUser() {
  return useQuery<CurrentUserView>({
    queryKey: currentUserQueryKey,
    queryFn: async () => {
      const response = await sessionClient.getCurrentSession({});
      const session = response.session;
      if (!isAccountId(session?.userId)) {
        throw new ConnectError("unauthenticated", Code.Unauthenticated);
      }
      const username = safeDisplayName(session.username, session.userId, "");
      const primaryEmail = safeEmail(session.primaryEmail);
      const displayName = safeDisplayName(session.displayName, session.userId, session.primaryEmail || "Profile");
      return {
        user: {
          id: session.userId,
          userId: session.userId,
          username,
          displayName,
          primaryEmail,
          avatarUrl: safeAvatarUrl(session.avatarUrl),
        },
      };
    },
    retry: false,
    refetchOnWindowFocus: "always",
    staleTime: 60_000,
  });
}

// Ends this device's Login Session, forgets everything loaded for it and
// returns to the sign-in page.
export function useLogout() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: signOut,
    onError: () => notify("sign-out", "danger", "You’re still signed in", "Signing out didn’t reach Talgent. Check your connection and try again."),
    onSuccess: () => {
      dismiss("sign-out");
      navigate("/login", { replace: true });
      queryClient.clear();
    },
  });
}
