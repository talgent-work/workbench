// Local acceptance fixture: a stdio MCP server with one read-only tool,
// speaking newline-delimited JSON-RPC without an SDK.
import { readFile } from "node:fs/promises";
import { writeFileSync } from "node:fs";
import { createInterface } from "node:readline";

const send = message => process.stdout.write(JSON.stringify({ jsonrpc: "2.0", ...message }) + "\n");

async function read() {
  try {
    const text = await readFile(process.argv[2], "utf8");
    if (process.argv[3] === "--disconnect") {
      setTimeout(() => {
        writeFileSync(`${process.argv[2]}.disconnected`, "closed");
        process.exit(0);
      }, 100);
    }
    return { content: [{ type: "text", text }] };
  } catch (error) {
    return { content: [{ type: "text", text: String(error.message) }], isError: true };
  }
}

createInterface({ input: process.stdin }).on("line", async line => {
  const { id, method, params } = JSON.parse(line);
  if (id === undefined) return;
  if (method === "initialize") send({ id, result: { protocolVersion: params.protocolVersion, capabilities: { tools: {} }, serverInfo: { name: "telemetry-probe", version: "1.0.0" } } });
  else if (method === "tools/list") send({ id, result: { tools: [{ name: "read", description: "Read the telemetry fixture file", inputSchema: { type: "object" } }] } });
  else if (method === "tools/call") send({ id, result: await read() });
  else send({ id, error: { code: -32601, message: "unsupported" } });
});
