import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCurrentUser } from "./account-service";
import { personalVariableClient } from "./client";

// The signed-in person's Personal Variables: named values each Work their
// Work Agent does for them enables at its creation and receives as
// environment variables at every start. Listing gives names only; a value is read one at a time, on request,
// and never kept in the query cache.

const variablesKey = (userId: string | undefined) => ["personal-variables", userId];

/** The names of the person's variables, ordered as the core orders them. */
export function usePersonalVariables(enabled = true) {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: variablesKey(userId),
    queryFn: async () => (await personalVariableClient.listPersonalVariables({})).variables.map(variable => variable.name),
    enabled: Boolean(userId) && enabled,
    retry: false,
    refetchOnWindowFocus: false,
  });
}

/** Reads one value; `signal` drops a read nobody waits for any more. */
export async function revealPersonalVariable(name: string, signal: AbortSignal) {
  return (await personalVariableClient.revealPersonalVariable({ name }, { signal })).value;
}

// The core orders names by their bytes; names are ASCII, so code-unit order matches.
const withName = (names: string[] | undefined, name: string) => names && !names.includes(name) ? [...names, name].sort() : names;

/** Saves a variable, replacing any value of that name. */
export function useSetPersonalVariable() {
  const queryClient = useQueryClient();
  const key = variablesKey(useCurrentUser().data?.user?.id);
  return useMutation({
    mutationFn: async (variable: { name: string; value: string }) => { await personalVariableClient.setPersonalVariable(variable); },
    onSuccess: (_, { name }) => {
      queryClient.setQueryData<string[]>(key, names => withName(names, name));
      void queryClient.invalidateQueries({ queryKey: key });
    },
  });
}

export function useDeletePersonalVariable() {
  const queryClient = useQueryClient();
  const key = variablesKey(useCurrentUser().data?.user?.id);
  return useMutation({
    mutationFn: async (name: string) => { await personalVariableClient.deletePersonalVariable({ name }); },
    onSuccess: (_, name) => {
      queryClient.setQueryData<string[]>(key, names => names?.filter(item => item !== name));
      void queryClient.invalidateQueries({ queryKey: key });
    },
  });
}

const NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;
const RESERVED_PREFIX = "TALGENT_";

/** Why a name cannot be saved, for feedback while typing; the core's refusal
 * stays authoritative. A new variable may not reuse a name the person keeps,
 * which would silently replace that value. */
export function nameProblem(name: string, kept: string[], isNew: boolean) {
  if (!name) return "Enter a name.";
  if (!NAME.test(name)) return "Use letters, digits and underscores, not starting with a digit.";
  if (name.startsWith(RESERVED_PREFIX)) return `Names starting with ${RESERVED_PREFIX} are reserved for the platform.`;
  if (isNew && kept.includes(name)) return `${name} already exists.`;
  return "";
}

/** The core names the rule a refused variable broke; other failures get `fallback`. */
export function personalVariableErrorMessage(error: unknown, fallback: string) {
  if (error instanceof ConnectError && error.code === Code.InvalidArgument && error.rawMessage) return error.rawMessage;
  return fallback;
}
