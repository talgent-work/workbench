# Native CLI over real execd WebSocket pipe mode; no Executor or SDK wrapper.
# Requires websocket-client==1.8.0 in an isolated Python environment.
# Set EXECD_BINARY and CLAUDE_BINARY to the pinned binaries under evaluation.
# Requires Claude >=2.1.234 for explicit project storage grouping.
# Uses local fake inference; retains raw evidence in a temporary directory.
# Verifies interrupt, SIGINT exit 0 and resume with local files; not storage durability.
import http.server, threading, tempfile, pathlib, subprocess, os, json, gzip

root = pathlib.Path(tempfile.mkdtemp(prefix="talgent-native-execd-"))
project_namespace = "native-probe-project"
resumed_workspace = root / "resumed-workspace"
resumed_workspace.mkdir()
(root / "sample.txt").write_text("native tool probe")
received = []
calls = []
tool_calls = []
question = {
    "questions": [
        {
            "question": "Proceed?",
            "header": "Choice",
            "options": [
                {"label": "Yes", "description": "Proceed"},
                {"label": "No", "description": "Stop"},
            ],
            "multiSelect": False,
        }
    ]
}


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        if self.headers.get("Content-Encoding") == "gzip":
            body = gzip.decompress(body)
        data = json.loads(body)
        if (
            self.path.startswith("/v1/traces")
            or self.path.startswith("/v1/logs")
            or self.path.startswith("/v1/metrics")
        ):
            received.append({"path": self.path, "data": data})
            self.send_response(200)
            self.send_header("content-type", "application/json")
            self.end_headers()
            self.wfile.write(b"{}")
            return
        if "count_tokens" in self.path:
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b'{"input_tokens":10}')
            return
        if "/messages" not in self.path:
            self.send_response(404)
            self.end_headers()
            return
        calls.append(data)
        tool = (
            any(t.get("name") == "AskUserQuestion" for t in data.get("tools", []))
            and not tool_calls
        )
        if tool:
            tool_calls.append(1)
        events = [
            {
                "type": "message_start",
                "message": {
                    "id": "msg_" + str(len(calls)),
                    "type": "message",
                    "role": "assistant",
                    "content": [],
                    "model": "claude-sonnet-4-6",
                    "stop_reason": None,
                    "stop_sequence": None,
                    "usage": {"input_tokens": 10, "output_tokens": 0},
                },
            },
            {
                "type": "content_block_start",
                "index": 0,
                "content_block": (
                    {
                        "type": "tool_use",
                        "id": "question-tool",
                        "name": "AskUserQuestion",
                        "input": {},
                    }
                    if tool
                    else {"type": "text", "text": ""}
                ),
            },
            {
                "type": "content_block_delta",
                "index": 0,
                "delta": (
                    {
                        "type": "input_json_delta",
                        "partial_json": json.dumps(question),
                    }
                    if tool
                    else {"type": "text_delta", "text": "done"}
                ),
            },
            {"type": "content_block_stop", "index": 0},
            {
                "type": "message_delta",
                "delta": {
                    "stop_reason": "tool_use" if tool else "end_turn",
                    "stop_sequence": None,
                },
                "usage": {"output_tokens": 5},
            },
            {"type": "message_stop"},
        ]
        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.end_headers()
        for e in events:
            self.wfile.write(
                ("event: " + e["type"] + "\ndata: " + json.dumps(e) + "\n\n").encode()
            )
            self.wfile.flush()


import socket, time, uuid, shlex, urllib.request, websocket

server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
with socket.socket() as reservation:
    reservation.bind(("127.0.0.1", 0))
    port = reservation.getsockname()[1]
token = str(uuid.uuid4())
env = {
    "PATH": os.environ["PATH"],
    "HOME": str(root),
    "CLAUDE_CONFIG_DIR": str(root / ".claude"),
    "CLAUDE_CODE_PROJECT_DIR_NAME": project_namespace,
    "EXECD_ACCESS_TOKEN": token,
    "ANTHROPIC_BASE_URL": f"http://127.0.0.1:{server.server_port}",
    "ANTHROPIC_API_KEY": "local-test-only",
    "DISABLE_AUTOUPDATER": "1",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
}
native_version = subprocess.check_output(
    [os.environ["CLAUDE_BINARY"], "--version"], env=env, text=True
).strip()
assert tuple(map(int, native_version.split()[0].split("."))) >= (2, 1, 234), (
    "Explicit project storage grouping requires Claude >=2.1.234: " + native_version
)
log = (root / "execd.log").open("w")
daemon = subprocess.Popen(
    [os.environ["EXECD_BINARY"], f"--port={port}"],
    env=env,
    cwd=root,
    stdout=log,
    stderr=log,
)


def api(route, body=None, method=None):
    req = urllib.request.Request(
        f"http://127.0.0.1:{port}" + route,
        data=json.dumps(body).encode() if body is not None else None,
        headers={"X-EXECD-ACCESS-TOKEN": token, "Content-Type": "application/json"},
        method=method,
    )
    with urllib.request.urlopen(req, timeout=3) as r:
        raw = r.read()
        return json.loads(raw) if raw.startswith(b"{") else raw.decode()


connections = []
ids = []
frames = []
controls = []


def run(session_id, inputs, resume=False, stop_pending=False, cwd=root):
    argv = [
        os.environ["CLAUDE_BINARY"],
        "-p",
        "--input-format",
        "stream-json",
        "--output-format",
        "stream-json",
        "--verbose",
        "--permission-prompt-tool",
        "stdio",
        "--resume" if resume else "--session-id",
        session_id,
    ]
    created = api("/pty", {"cwd": str(cwd), "command": "exec " + shlex.join(argv)})
    id = created["session_id"]
    ids.append(id)
    ws = websocket.create_connection(
        f"ws://127.0.0.1:{port}/pty/{id}/ws?pty=0",
        header={"X-EXECD-ACCESS-TOKEN": token},
        timeout=15,
    )
    connections.append(ws)
    buffer = b""
    local = []

    def send(value):
        ws.send_binary(b"\x00" + json.dumps(value).encode() + b"\n")

    def until(predicate):
        nonlocal buffer
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            frame = ws.recv()
            if isinstance(frame, str):
                control = json.loads(frame)
                controls.append(control)
                if control.get("type") == "exit":
                    return control
                continue
            if frame[0] == 2:
                with (root / "claude-stderr.log").open("ab") as f:
                    f.write(frame[1:])
                continue
            assert frame[0] == 1, frame[:10]
            buffer += frame[1:]
            while b"\n" in buffer:
                line, buffer = buffer.split(b"\n", 1)
                if not line:
                    continue
                msg = json.loads(line)
                frames.append(msg)
                local.append(msg)
                if (
                    msg.get("type") == "control_request"
                    and msg["request"].get("subtype") == "can_use_tool"
                ):
                    assert msg["request"]["tool_name"] == "AskUserQuestion", msg
                    if stop_pending:
                        return msg
                    send(
                        {
                            "type": "control_response",
                            "response": {
                                "subtype": "success",
                                "request_id": msg["request_id"],
                                "response": {
                                    "behavior": "allow",
                                    "updatedInput": {
                                        **question,
                                        "answers": {"Proceed?": "Yes"},
                                    },
                                },
                            },
                        }
                    )
                if predicate(msg):
                    return msg
        raise TimeoutError("no expected frame")

    if not inputs:
        request_id = str(uuid.uuid4())
        send(
            {
                "type": "control_request",
                "request_id": request_id,
                "request": {"subtype": "initialize"},
            }
        )
        initialized = until(
            lambda m: m.get("type") == "control_response"
            and m.get("response", {}).get("request_id") == request_id
        )
        assert initialized["response"]["subtype"] == "success", initialized
    for prompt in inputs:
        send(
            {
                "type": "user",
                "session_id": session_id,
                "parent_tool_use_id": None,
                "message": {"role": "user", "content": prompt},
            }
        )
        result = until(lambda m: m.get("type") == "result")
        if stop_pending:
            assert result.get("type") == "control_request", result
            request_id = str(uuid.uuid4())
            send(
                {
                    "type": "control_request",
                    "request_id": request_id,
                    "request": {"subtype": "interrupt"},
                }
            )
            until(
                lambda m: any(x.get("type") == "result" for x in local)
                and any(
                    x.get("type") == "control_response"
                    and x.get("response", {}).get("request_id") == request_id
                    for x in local
                )
            )
            ack = next(
                x
                for x in local
                if x.get("type") == "control_response"
                and x.get("response", {}).get("request_id") == request_id
            )
            assert ack["response"]["subtype"] == "success", ack
        else:
            assert result.get("subtype") == "success", result
    # After the current turn settles, SIGINT exercises native graceful exit.
    # Exit 0 alone is not a storage durability acknowledgement.
    ws.send(json.dumps({"type": "signal", "signal": "SIGINT"}))
    exit_frame = until(lambda m: False)
    assert exit_frame.get("type") == "exit", exit_frame
    assert exit_frame["exit_code"] == 0, exit_frame
    return {
        "execd_session": id,
        "exit_code": exit_frame["exit_code"],
        "results": len([m for m in local if m.get("type") == "result"]),
    }


try:
    deadline = time.monotonic() + 10
    while True:
        try:
            api("/ping")
            break
        except Exception:
            if daemon.poll() is not None or time.monotonic() > deadline:
                raise
            time.sleep(0.05)
    session = str(uuid.uuid4())
    first = run(session, ["FIRST_NATIVE_MARKER", "SECOND_NATIVE_MARKER"])
    answer = [
        b
        for call in calls
        for m in call.get("messages", [])
        for b in (m.get("content", []) if isinstance(m.get("content"), list) else [])
        if b.get("type") == "tool_result" and b.get("tool_use_id") == "question-tool"
    ]
    assert any(
        not b.get("is_error") and "Yes" in json.dumps(b.get("content")) for b in answer
    ), answer
    start = len(calls)
    resumed = run(session, ["RESUME_NATIVE_MARKER"], True, cwd=resumed_workspace)
    assert "FIRST_NATIVE_MARKER" in json.dumps(calls[start:])
    start = len(calls)
    restarted_id = str(uuid.uuid4())
    restarted = run(restarted_id, ["FRESH_NATIVE_MARKER"])
    assert "FIRST_NATIVE_MARKER" not in json.dumps(calls[start:])
    tool_calls.clear()
    interrupted_session = str(uuid.uuid4())
    interrupted = run(
        interrupted_session, ["INTERRUPTED_NATIVE_MARKER"], stop_pending=True
    )
    start = len(calls)
    interrupted_resumed = run(interrupted_session, ["AFTER_INTERRUPT_MARKER"], True)
    assert "INTERRUPTED_NATIVE_MARKER" in json.dumps(calls[start:])
    empty_id = str(uuid.uuid4())
    empty = run(empty_id, [])
    empty_transcripts = list(root.rglob(empty_id + ".jsonl"))
    assert not any(p.stat().st_size for p in empty_transcripts), empty_transcripts
    empty_continued = run(empty_id, ["FIRST_AFTER_EMPTY_MARKER"])
    # The explicit project namespace must survive a workspace path change. Same
    # HOME/project groups native storage, while each conversation keeps its file.
    project_history = root / ".claude" / "projects" / project_namespace
    for native_id in [session, restarted_id, interrupted_session, empty_id]:
        assert (project_history / (native_id + ".jsonl")).stat().st_size > 0
    print(
        json.dumps(
            {
                "empty": empty,
                "empty_continued": empty_continued,
                "interrupted": interrupted,
                "interrupted_resumed": interrupted_resumed,
                "first": first,
                "resumed": resumed,
                "restarted": restarted,
                "project_history": str(project_history),
                "resume_changed_workspace": True,
                "claude_version": native_version,
                "evidence": str(root),
            }
        )
    )
finally:
    (root / "frames.json").write_text(json.dumps(frames))
    (root / "controls.json").write_text(json.dumps(controls))
    (root / "model-requests.json").write_text(json.dumps(calls))
    for id in ids:
        try:
            api("/pty/" + id, method="DELETE")
        except Exception:
            pass
    for ws in connections:
        ws.close()
    daemon.terminate()
    try:
        daemon.wait(timeout=5)
    except subprocess.TimeoutExpired:
        daemon.kill()
        daemon.wait()
    server.shutdown()
    server.server_close()
    log.close()
    print("evidence: " + str(root))
