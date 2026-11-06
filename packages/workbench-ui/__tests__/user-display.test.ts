import { describe, expect, it } from "vitest";
import { safeAvatarUrl, safeDisplayName } from "../lib/user-display";

describe("user display helpers", () => {
  it("keeps GitHub avatar URLs as reported", () => {
    expect(safeAvatarUrl("https://avatars.githubusercontent.com/u/583231?v=4")).toBe("https://avatars.githubusercontent.com/u/583231?v=4");
    expect(safeAvatarUrl("http://avatars.example.test/octo.png")).toBe("http://avatars.example.test/octo.png");
    expect(safeAvatarUrl("javascript:alert(1)")).toBe("");
  });

  it("never shows an Account id as a name", () => {
    expect(safeDisplayName("01a11fb2-e6c2-701f-80b7-38c54aa2eae9", undefined, "User")).toBe("User");
    expect(safeDisplayName("Octo Cat", "01a11fb2-e6c2-701f-80b7-38c54aa2eae9")).toBe("Octo Cat");
  });
});
