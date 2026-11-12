package execution

// titleHook is the Stop hook every Harness process runs: it reports the
// title the Harness gave the Work's conversation, which the Harness keeps in
// its transcript only. It finds the latest title in the transcript's last
// MiB and answers it as the hook's systemMessage, a JSON string after the
// "talgent-title:" marker; the Harness emits that message in its output
// stream as an informational system message, where the Dispatcher reads it
// (nativeprocess.TitleMarker, which the marker must equal). Only this hook's
// message enters the stream, never other hooks' events. Before there is a
// title it prints nothing. It always exits 0, so it never holds the Turn
// open, and node is the one interpreter every Computer that runs the Harness
// has.
const titleHook = `node -e '` + titleHookScript + `'`

// titleHookScript holds no single quote: titleHook quotes it whole.
const titleHookScript = `let input = "";
process.stdin.on("data", chunk => { input += chunk; }).on("end", () => {
  try {
    const fs = require("fs");
    const fd = fs.openSync(JSON.parse(input).transcript_path, "r");
    const size = fs.fstatSync(fd).size, length = Math.min(size, 1048576), tail = Buffer.alloc(length);
    fs.readSync(fd, tail, 0, length, size - length);
    fs.closeSync(fd);
    let title = "";
    for (const line of tail.toString("utf8").split("\n")) {
      if (!line.includes("\"ai-title\"")) continue;
      try { const entry = JSON.parse(line); if (entry.type === "ai-title" && typeof entry.aiTitle === "string") title = entry.aiTitle; } catch {}
    }
    if (title) process.stdout.write(JSON.stringify({ systemMessage: "talgent-title:" + JSON.stringify(title), suppressOutput: true }) + "\n");
  } catch {}
});`
