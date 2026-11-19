// A local command Connector's command line, as the person types it: the
// program and its arguments separated by spaces, with POSIX shell quoting
// for an argument that holds spaces or quotes. Nothing else of the shell
// applies: no variables, globs or operators. The Marketplace keeps the
// program and its arguments apart, as a native stdio MCP server declares
// them, so a line and its parts round-trip exactly.

export type CommandLine = { command: string; arguments: string[] };

/** The program and arguments of a line, or the reason it has none. */
export function parseCommandLine(line: string): CommandLine | { problem: string } {
  const words: string[] = [];
  let word: string | null = null;
  let quote: "'" | '"' | null = null;
  for (let at = 0; at < line.length; at++) {
    const char = line[at];
    if (quote === "'") {
      if (char === "'") quote = null;
      else word += char;
    } else if (quote === '"') {
      if (char === '"') quote = null;
      else if (char === "\\" && (line[at + 1] === '"' || line[at + 1] === "\\")) word += line[++at];
      else word += char;
    } else if (/\s/.test(char)) {
      if (word !== null) words.push(word);
      word = null;
    } else if (char === "'" || char === '"') {
      quote = char;
      word ??= "";
    } else if (char === "\\") {
      if (at + 1 >= line.length) return { problem: "End the line without a lone backslash." };
      word = (word ?? "") + line[++at];
    } else word = (word ?? "") + char;
  }
  if (quote) return { problem: `Close the ${quote === "'" ? "single" : "double"} quote.` };
  if (word !== null) words.push(word);
  const [command = "", ...args] = words;
  return { command, arguments: args };
}

const plain = /^[A-Za-z0-9_@%+=:,./-]+$/;

/** One argument as the line shows it, quoted only when it must be. */
export function quoteArgument(argument: string) {
  return plain.test(argument) ? argument : `'${argument.replaceAll("'", `'\\''`)}'`;
}

/** The line that parses back to exactly this program and these arguments. */
export function formatCommandLine({ command, arguments: args }: CommandLine) {
  return command ? [command, ...args].map(quoteArgument).join(" ") : "";
}
