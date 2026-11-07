import { Code, ConnectError } from "@connectrpc/connect";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { WorkFileAccessDisposition, WorkFileKind, type WorkFile } from "@talgent/contracts/agent/public/v1/work_file_pb";
import { useCurrentUser } from "./account-service";
import { workFileClient } from "./client";
import { NameTakenError } from "./name-taken-error";
import type { PreviewVerdict } from "../components/preview/preview-verdict";
import { previewVerdict } from "./preview-verdict";

// A Personal Work's files, by their position: Work, kind and name. Its owner
// and Work Share holders read them; only the owner changes them.
export type WorkFileRef = Pick<WorkFile, "kind" | "name">;

export function workFileKey(file: WorkFileRef) {
  return `${file.kind === WorkFileKind.ARTIFACT ? "artifact" : "attachment"}/${file.name}`;
}

export function workFilesQueryKey(workId: string | undefined, userId?: string) {
  return ["work-files", userId ?? "", workId] as const;
}

export function useWorkFiles(workId: string | undefined) {
  const userId = useCurrentUser().data?.user?.id;
  return useQuery({
    queryKey: workFilesQueryKey(workId, userId),
    queryFn: async ({ signal }) => (await workFileClient.listWorkFiles({ workId: workId! }, { signal })).files,
    enabled: Boolean(workId && userId),
    retry: false,
    refetchOnWindowFocus: false,
    refetchInterval: 5000,
  });
}

/** A short-lived link for one read of the file, saved or shown inline. */
export async function workFileAccessLink(workId: string, file: WorkFileRef, download: boolean) {
  const resp = await workFileClient.createWorkFileAccessLink({
    workId, kind: file.kind, name: file.name,
    disposition: download ? WorkFileAccessDisposition.DOWNLOAD : WorkFileAccessDisposition.PREVIEW,
  });
  if (!resp.accessUrl) throw new Error("The file link is missing.");
  return resp.accessUrl;
}

export function useDownloadWorkFile(workId: string) {
  return useMutation({
    mutationFn: async (file: WorkFileRef) => {
      const url = await workFileAccessLink(workId, file, true);
      window.open(url, "_blank", "noopener,noreferrer");
      return url;
    },
  });
}

/** How Storage shows the file, through Agent. */
export async function workFilePreview(workId: string, file: WorkFileRef): Promise<PreviewVerdict> {
  return previewVerdict((await workFileClient.getWorkFilePreview({ workId, kind: file.kind, name: file.name })).preview);
}

/** Asks for the file's Preview Copy when it needs one. */
export async function requestWorkFilePreview(workId: string, file: WorkFileRef): Promise<PreviewVerdict> {
  return previewVerdict((await workFileClient.requestWorkFilePreview({ workId, kind: file.kind, name: file.name })).preview);
}

// Creates the Attachment, or replaces the one of the same name when replace
// is set. Storage never renames or replaces on its own: a taken name is
// refused with NameTakenError.
export async function uploadWorkAttachment(workId: string, file: File, replace = false, fetchImpl: typeof fetch = fetch) {
  let uploadUrl: string;
  try {
    // An unknown type stays undeclared, so Storage infers it from the name.
    uploadUrl = (await workFileClient.createWorkAttachmentUpload({ workId, name: file.name, replace, contentType: file.type })).uploadUrl;
  } catch (error) {
    if (error instanceof ConnectError && error.code === Code.AlreadyExists) throw new NameTakenError(file.name);
    throw error;
  }
  if (!uploadUrl) throw new Error("upload target is missing");
  const upload = await fetchImpl(uploadUrl, { method: "PUT", headers: file.type ? { "content-type": file.type } : undefined, body: file });
  if (upload.status === 409) throw new NameTakenError(file.name);
  if (!upload.ok) throw new Error(`upload failed: ${upload.status}`);
  return file.name;
}

export function useUploadWorkAttachment(workId: string | undefined) {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  return useMutation({
    mutationFn: ({ file, replace = false }: { file: File; replace?: boolean }) => {
      if (!workId) throw new Error("The Work is required.");
      return uploadWorkAttachment(workId, file, replace);
    },
    onSuccess: () => { void queryClient.invalidateQueries({ queryKey: workFilesQueryKey(workId, userId) }); },
  });
}

// The owner's changes to the Work's files; each refreshes the file list.
function useWorkFileMutation<T, R>(workId: string, change: (input: T) => Promise<R>) {
  const queryClient = useQueryClient();
  const userId = useCurrentUser().data?.user?.id;
  return useMutation({ mutationFn: change, onSuccess: () => { void queryClient.invalidateQueries({ queryKey: workFilesQueryKey(workId, userId) }); } });
}

export function useRenameWorkAttachment(workId: string) {
  return useWorkFileMutation(workId, ({ name, newName }: { name: string; newName: string }) => workFileClient.renameWorkAttachment({ workId, name, newName }));
}

// Deleting a file ends its share links.
export function useDeleteWorkFile(workId: string) {
  return useWorkFileMutation(workId, (file: WorkFileRef) => workFileClient.deleteWorkFile({ workId, kind: file.kind, name: file.name }));
}

export function useCreateWorkFileShareLink(workId: string) {
  return useWorkFileMutation(workId, async (file: WorkFileRef) => (await workFileClient.createWorkFileShareLink({ workId, kind: file.kind, name: file.name })).shareLink);
}

// Stopping sharing ends every Share Link of the file.
export function useStopWorkFileSharing(workId: string) {
  return useWorkFileMutation(workId, (file: WorkFileRef) => workFileClient.stopWorkFileSharing({ workId, kind: file.kind, name: file.name }));
}
