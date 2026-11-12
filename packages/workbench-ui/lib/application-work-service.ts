import { useQuery } from "@tanstack/react-query";
import { useCurrentUser } from "./account-service";
import { workClient } from "./client";
import { standaloneWork, type StandaloneWork } from "./standalone-work-service";

// An Application's Works as its Application Owner reads them (Agent's
// WorkService, naming the Application): never driven from here.

/** A Work of the Application with the Work Labels it was started with. */
export type ApplicationWork = StandaloneWork & { labels: Record<string, string> };

/** Where the Application Owner reads one of the Application's Works. */
export const applicationWorkPath = (applicationId: string, workId: string) =>
  `/applications/works/${encodeURIComponent(workId)}?app=${encodeURIComponent(applicationId)}`;

/** One Work Label, key and value. */
export type LabelPair = readonly [key: string, value: string];
export const labelText = ([key, value]: LabelPair) => `${key}=${value}`;

/** The Work States a Work is still open in: everything but closed. */
const openStates = ["starting", "working", "stopping", "stopped", "failed", "closing"];
/** At most this many Works are listed; a full page means there may be more. */
export const listedApplicationWorks = 100;

const applicationWorksKey = (userId: string, applicationId: string, ...rest: unknown[]) => ["application-works", userId, applicationId, ...rest] as const;

/** The Application's Works carrying every one of labels, closed ones included,
 * most recently updated first. */
export function useApplicationWorks(applicationId: string, labels: readonly LabelPair[] = []) {
  const userId = useCurrentUser().data?.user?.id ?? "";
  const filter = Object.fromEntries(labels);
  return useQuery({
    queryKey: applicationWorksKey(userId, applicationId, filter),
    enabled: Boolean(userId && applicationId),
    retry: false,
    refetchInterval: 10000,
    refetchOnWindowFocus: false,
    queryFn: async ({ signal }) => (await workClient.listWorks({ applicationId, labels: filter, limit: listedApplicationWorks }, { signal })).works
      .map((work): ApplicationWork => ({ ...standaloneWork(work), labels: { ...work.labels } })),
  });
}

/** How many of the Application's Works are open, up to a full page. */
export function useOpenApplicationWorks(applicationId: string) {
  const userId = useCurrentUser().data?.user?.id ?? "";
  return useQuery({
    queryKey: applicationWorksKey(userId, applicationId, "open"),
    enabled: Boolean(userId && applicationId),
    retry: false,
    refetchOnWindowFocus: false,
    queryFn: async ({ signal }) => (await workClient.listWorks({ applicationId, states: openStates, limit: listedApplicationWorks }, { signal })).works.length,
  });
}
