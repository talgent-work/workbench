import type { WorkModelOption } from "@talgent/workbench-contracts/agent/public/v1/model_pb";
import type { WorkStartConnector, WorkStartPlugin, WorkStartRepositories } from "@talgent/workbench-contracts/agent/public/v1/work_start_options_pb";
import type { WorkStartDraftScope } from "./work-start-draft";

/** Where a new Work's choices come from: the options and repositories offered
 * to whoever the Work runs for, `key` naming them so a change reloads them.
 * Without `uploadAttachment` the Work takes no attachments; with
 * `chooseComputer` the person picks the Computer it runs on. */
export type WorkStartSource = {
  draft: WorkStartDraftScope;
  key: readonly unknown[];
  loadOptions: (signal: AbortSignal) => Promise<{ models: WorkModelOption[]; plugins: WorkStartPlugin[]; connectors: WorkStartConnector[] }>;
  loadRepositories: (signal: AbortSignal) => Promise<Pick<WorkStartRepositories, "repositories" | "recentRepositoryIds" | "repositoriesUnavailable">>;
  uploadAttachment?: (file: File) => Promise<unknown>;
  chooseComputer?: boolean;
};
