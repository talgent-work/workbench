import { expect, it } from "vitest";
import { formatCommandLine, parseCommandLine } from "../lib/command-line";

it("splits a line on spaces and keeps quoted spaces inside one argument", () => {
  expect(parseCommandLine("  npx  chrome-devtools-mcp@0.6.0 --headless ")).toEqual({ command: "npx", arguments: ["chrome-devtools-mcp@0.6.0", "--headless"] });
  expect(parseCommandLine(`node server.js --root '/srv/My Docs' --title "Team \\"A\\"" a\\ b ''`))
    .toEqual({ command: "node", arguments: ["server.js", "--root", "/srv/My Docs", "--title", `Team "A"`, "a b", ""] });
  expect(parseCommandLine("")).toEqual({ command: "", arguments: [] });
});

it("names what is wrong with a line it cannot read", () => {
  expect(parseCommandLine("node 'server")).toEqual({ problem: "Close the single quote." });
  expect(parseCommandLine(`node "server`)).toEqual({ problem: "Close the double quote." });
  expect(parseCommandLine("node \\")).toHaveProperty("problem");
});

it("formats a program and its arguments into the line that parses back to them", () => {
  const cases = [
    { command: "npx", arguments: ["chrome-devtools-mcp@0.6.0"] },
    { command: "/opt/My Tools/mcp", arguments: ["--root", "/srv/My Docs", "it's", `say "hi"`, "", "back\\slash", "tab\there"] },
  ];
  for (const parts of cases) expect(parseCommandLine(formatCommandLine(parts))).toEqual(parts);
  expect(formatCommandLine(cases[0])).toBe("npx chrome-devtools-mcp@0.6.0");
  expect(formatCommandLine({ command: "node", arguments: ["it's"] })).toBe(`node 'it'\\''s'`);
});
