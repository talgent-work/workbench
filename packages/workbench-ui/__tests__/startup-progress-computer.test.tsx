import { create } from "@bufbuild/protobuf";
import { render, screen } from "@testing-library/react";
import { StartupProgressSchema, StartupStageSchema } from "@talgent/workbench-contracts/agent/public/v1/startup_pb";
import { WorkStartupRequestSchema } from "@talgent/workbench-contracts/agent/public/v1/work_pb";
import { describe, expect, it } from "vitest";
import { StartupProgressPanel } from "../components/session/startup-progress";

const preparing = create(StartupProgressSchema, {
  requestId: "start", admitted: true, status: "preparing",
  stages: [create(StartupStageSchema, { stage: "sandbox", status: "active" })],
});
const request = create(WorkStartupRequestSchema, { requestId: "start", initialMessage: "Hello" });

describe("Startup progress names the Work's Computer", () => {
  it("tells a start on a Managed Computer apart from a Cloud start", () => {
    render(<StartupProgressPanel progress={preparing} request={request} computer={{ name: "studio", managed: true }} />);
    const panel = screen.getByRole("region", { name: "Startup progress" });
    expect(panel).toHaveTextContent("Getting your computer ready for this Work.");
    expect(panel).not.toHaveTextContent("cloud environment");
    expect(panel).toHaveTextContent("Computerstudio");
  });

  it("keeps the Cloud wording when the Work runs on a Cloud", () => {
    render(<StartupProgressPanel progress={preparing} request={request} computer={{ name: "Talgent Cloud", managed: false }} />);
    const panel = screen.getByRole("region", { name: "Startup progress" });
    expect(panel).toHaveTextContent("Bringing your cloud environment online.");
    expect(panel).toHaveTextContent("ComputerTalgent Cloud");
  });
});
