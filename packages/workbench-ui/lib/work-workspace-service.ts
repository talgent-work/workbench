import { workWorkspaceClient } from "./client";
import type { WorkspaceSource } from "./workspace-reading";

/** A Standalone Work's workspace as Agent serves it to the Work's readers. */
export function workWorkspaceSource(workId: string): WorkspaceSource {
  return {
    scope: `work:${workId}`,
    browse: (path, signal) => workWorkspaceClient.browseWorkWorkspace({ workId, path }, { signal }),
    preview: (path, signal) => workWorkspaceClient.getWorkWorkspaceFilePreview({ workId, path }, { signal }),
    download: path => workWorkspaceClient.createWorkWorkspaceFileDownload({ workId, path }),
  };
}
