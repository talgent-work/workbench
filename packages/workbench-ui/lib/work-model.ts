import { create } from "@bufbuild/protobuf";
import { ModelVendor, WorkModelOptionSchema, type WorkModelOption, type WorkModelSelection } from "@talgent-work/workbench-contracts/agent/public/v1/model_pb";

// What a Work's conversation uses for its model: a Model Configuration the
// platform provides, or, on a Managed Computer, the machine's own Claude login.

/** A model a new Work may start on. */
export type StartModel = { configurationId: string; machineLogin: boolean; name: string; vendor: ModelVendor; thinkingLevels: string[]; defaultThinkingLevel: string };

export const machineLoginName = "This machine's Claude login";

/** The Agent Harness's thinking levels. The machine's Claude login has no
 * Model Template, so it offers all of them and none is the default. */
const harnessThinkingLevels = ["low", "medium", "high", "xhigh", "max"];

export const machineLoginModel: StartModel = { configurationId: "", machineLogin: true, name: machineLoginName, vendor: ModelVendor.ANTHROPIC, thinkingLevels: harnessThinkingLevels, defaultThinkingLevel: "" };

/** The models a new Work may start on its Computer: the provided Model
 * Configurations, and on a Managed Computer first its own Claude login, the
 * default there. */
export function startModels(provided: WorkModelOption[], managed: boolean): StartModel[] {
  const configurations = provided.map(option => ({ configurationId: option.configurationId, machineLogin: false, name: option.name, vendor: option.vendor, thinkingLevels: option.thinkingLevels, defaultThinkingLevel: option.defaultThinkingLevel }));
  return managed ? [machineLoginModel, ...configurations] : configurations;
}

/** A start model's identity among the choices. */
export const startModelKey = (model: StartModel) => model.machineLogin ? "machine-login" : model.configurationId;

/** The model selection a start request carries. */
export const startModelSelection = (model: StartModel) => model.machineLogin ? { machineLogin: true } : { configurationId: model.configurationId };

const machineLoginOption = create(WorkModelOptionSchema, { name: machineLoginName, vendor: machineLoginModel.vendor, thinkingLevels: harnessThinkingLevels });

/** How a Work's model is presented: the machine's Claude login, or its Model
 * Configuration as currently provided, none when withdrawn. */
export function workModelOption(model?: WorkModelSelection, option?: WorkModelOption): WorkModelOption | undefined {
  return model?.machineLogin ? machineLoginOption : option;
}
