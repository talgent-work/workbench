import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { useRef } from "react";
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { DirectoryUser, LibraryFile, LibraryWork, PersonalWorkSummary } from "@talgent-work/workbench-contracts/agent/public/v1/personal_work_pb";
import { useCurrentUser } from "./account-service";
import { agentWorkQueryKey } from "./agent-work-service";
import { agentWorkClient, personalWorkClient } from "./client";
import { workStartFields, type WorkStartInput } from "./start-work-request";
import type { WorkStartSource } from "./work-start-source";

// A person's own use of their Agent (Agent's PersonalWorkService): the
// Personal Works they start for themselves, sharing them, and their Library.

/** stateReason: why it stopped; disconnectedSince: when it was disconnected,
 * null while observed. */
export type PersonalWork = { id: string; title: string; state: string; stateReason: string; waiting: boolean; updatedAt: Date | null; disconnectedSince: Date | null };

function personalWork(work: PersonalWorkSummary): PersonalWork {
  return { id: work.workId, title: work.title.trim() || "Untitled work", state: work.state, stateReason: work.stateReason, waiting: work.waiting, updatedAt: work.updatedAt ? timestampDate(work.updatedAt) : null, disconnectedSince: work.disconnectedSince ? timestampDate(work.disconnectedSince) : null };
}

/** Every person's Personal Works lists, the prefix of personalWorksQueryKey. */
export const personalWorksKey = ["personal-works"] as const;

export function personalWorksQueryKey(userId: string | undefined) {
  return [...personalWorksKey, userId ?? ""] as const;
}

/** How a Work is named among a person's own Works when the Workbench holds no
 * title for it, such as a Work another product started. */
export const untitledWorkLabel = "Work";

/** The person's Personal Works, closed ones included, most recently updated first. */
export function usePersonalWorks() {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: personalWorksQueryKey(userId),
    queryFn: async ({ signal }) => (await personalWorkClient.listPersonalWorks({ limit: 100 }, { signal })).works.map(personalWork),
    enabled: Boolean(userId),
    retry: false,
    refetchInterval: 10000,
    refetchOnWindowFocus: false,
  });
}

export function useChangePersonalWorkTitle(workId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async (title: string) => (await personalWorkClient.changePersonalWorkTitle({ workId, title })).title,
    onSuccess: () => { void client.invalidateQueries({ queryKey: personalWorksKey }); },
  });
}

/** People in the User Directory whose username matches the query. */
export function useShareCandidates(query: string) {
  const text = query.trim();
  return useQuery({
    queryKey: ["share-candidates", text],
    queryFn: async ({ signal }) => (await personalWorkClient.searchShareReaders({ query: text, limit: 10 }, { signal })).users,
    enabled: text.length > 0,
    retry: false,
    placeholderData: keepPreviousData,
    staleTime: 30_000,
  });
}

/** The people behind the given identities, by identity; an unknown one is absent. */
export function useDirectoryUsers(userIds: string[]) {
  const ids = [...new Set(userIds.filter(Boolean))].sort();
  return useQuery({
    queryKey: ["directory-users", ids],
    queryFn: async ({ signal }) => new Map((await personalWorkClient.getShareReaders({ userIds: ids }, { signal })).users.map(user => [user.userId, user] as const)),
    enabled: ids.length > 0,
    retry: false,
    staleTime: 60_000,
  });
}

export function useGrantPersonalWorkShare(workId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (reader: DirectoryUser) => personalWorkClient.grantPersonalWorkShare({ workId, readerUserId: reader.userId }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: agentWorkQueryKey(workId) }); },
  });
}

export function useRevokeWorkShare(workId: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (readerUserId: string) => agentWorkClient.revokeWorkShare({ workId, readerUserId }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: agentWorkQueryKey(workId) }); },
  });
}

export type LibraryFilter = { name: string; workId: string; addedWithinDays: number | null };

export type Library = { files: LibraryFile[]; works: LibraryWork[] };

/** The person's Personal Library, narrowed as asked, most recently added first. */
export function useLibrary(filter: LibraryFilter) {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: ["personal-library", userId ?? "", filter.name.trim(), filter.workId, filter.addedWithinDays],
    queryFn: async ({ signal }): Promise<Library> => {
      const after = filter.addedWithinDays === null ? undefined : timestampFromDate(new Date(Date.now() - filter.addedWithinDays * 86_400_000));
      const response = await personalWorkClient.listLibraryFiles({ nameQuery: filter.name.trim(), workId: filter.workId, addedAfter: after }, { signal });
      return { files: response.files, works: response.works };
    },
    enabled: Boolean(userId),
    retry: false,
    placeholderData: keepPreviousData,
    refetchOnWindowFocus: false,
  });
}

/** What a new Personal Work may start with, under the person's own Agent. */
export const personalWorkStartSource: Omit<WorkStartSource, "draft"> = {
  key: ["personal"],
  loadOptions: async signal => {
    const options = (await personalWorkClient.getPersonalWorkStartOptions({}, { signal })).options;
    return { models: options?.models ?? [], plugins: options?.plugins ?? [], connectors: options?.connectors ?? [] };
  },
  loadRepositories: async signal => {
    const found = (await personalWorkClient.listPersonalWorkStartRepositories({}, { signal })).repositories;
    return { repositories: found?.repositories ?? [], recentRepositoryIds: found?.recentRepositoryIds ?? [], repositoriesUnavailable: found?.repositoriesUnavailable ?? false };
  },
  chooseComputer: true,
};

/** Starts a Personal Work. One attempt keeps its request identity until it
 * succeeds, so a retried start returns the Work it already started. */
export function useStartPersonalWork() {
  const client = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  const attempt = useRef<string | null>(null);
  return useMutation({
    mutationFn: async (input: WorkStartInput) => {
      attempt.current ??= crypto.randomUUID();
      const workId = (await personalWorkClient.startPersonalWork({ requestId: attempt.current, ...workStartFields(input) })).workId;
      if (!workId) throw new Error("Work was not created.");
      attempt.current = null;
      return workId;
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: personalWorksQueryKey(userId) }); },
  });
}

/** A Work the person reads in their Library: their own Personal Work or one
 * shared with them; undefined until known or when the Library has none. */
export function useLibraryWork(workId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: ["library-work", workId],
    queryFn: async ({ signal }) => (await personalWorkClient.listLibraryFiles({ workId }, { signal })).works.find(work => work.workId === workId) ?? null,
    enabled: Boolean(workId) && enabled,
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: 60_000,
  });
}
