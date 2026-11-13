# Native CLI provider probe; no Talgent Executor imports or production services.
# Run with CLAUDE_BINARY pointing at the exact version being evaluated.
import http.server, threading, tempfile, pathlib, subprocess, os, json, gzip, time, signal, shlex, sys

root = pathlib.Path(tempfile.mkdtemp(prefix="talgent-native-trace-"))
(root / "sample.txt").write_text("native tool probe")
received = []
calls = []
tool_calls = []
interrupt_ready = threading.Event()
interrupt_release = threading.Event()
scenario = os.environ.get("NATIVE_PROBE_SCENARIO", "read")
assert scenario in ("read", "agent", "nested_agent", "skill", "mcp", "tool_fail", "denied", "skill_invalid", "mcp_fail", "slash_skill", "mcp_connect_fail", "mcp_disconnect", "details_off", "content_on", "interrupted"), scenario
failed_tool = scenario in ("tool_fail", "denied", "skill_invalid", "mcp_fail")
expected_tools = {"read": ["Read"], "agent": ["Agent", "Read"], "nested_agent": ["Agent", "Agent", "Read"], "skill": ["Skill"], "mcp": ["mcp__probe__read"], "mcp_fail": ["mcp__probe__read"], "tool_fail": ["Read"], "denied": ["Read"], "skill_invalid": ["Skill"], "slash_skill": [], "mcp_connect_fail": [], "mcp_disconnect": ["mcp__probe__read"], "details_off": ["Read"], "content_on": ["Read"], "interrupted": []}[scenario]
if scenario in ("skill", "skill_invalid", "slash_skill"):
    skill = root / ".claude" / "skills" / "probe" / "SKILL.md"
    skill.parent.mkdir(parents=True)
    skill.write_text("---\nname: probe\ndescription: Native telemetry acceptance fixture\n---\nReply briefly with SKILL_PROBE_ACTIVATED.\n")

if scenario == "denied":
    hook = root / "deny.py"
    hook.write_text('import json; print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "Native telemetry denial fixture"}}))')
    settings = root / ".claude" / "settings.json"
    settings.parent.mkdir(exist_ok=True)
    settings.write_text(json.dumps({"hooks": {"PreToolUse": [{"matcher": "Read", "hooks": [{"type": "command", "command": shlex.quote(sys.executable) + " " + shlex.quote(str(hook))}]}]}}))


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
        names = {t.get("name") for t in data.get("tools", [])}
        selected, payload = None, {}
        if scenario in ("agent", "nested_agent") and not tool_calls and "Agent" in names:
            selected = "Agent"
            payload = {"description": "native delegation probe", "prompt": "Read sample.txt and return briefly", "subagent_type": "general-purpose", "run_in_background": True}
        elif scenario == "nested_agent" and tool_calls == ["Agent"] and "Agent" in names:
            selected = "Agent"
            payload = {"description": "nested native delegation", "prompt": "Read sample.txt and return briefly", "subagent_type": "general-purpose", "run_in_background": True}
        elif scenario in ("skill", "skill_invalid") and not tool_calls and "Skill" in names:
            selected, payload = "Skill", {"skill": "" if scenario == "skill_invalid" else "probe"}
        elif scenario in ("mcp", "mcp_fail", "mcp_disconnect") and not tool_calls and "mcp__probe__read" in names:
            selected = "mcp__probe__read"
        elif scenario in ("read", "agent", "nested_agent", "tool_fail", "denied", "details_off", "content_on") and "Read" in names and tool_calls == (["Agent", "Agent"] if scenario == "nested_agent" else ["Agent"] if scenario == "agent" else []):
            selected = "Read"
            payload = {"file_path": str(root / ("missing.txt" if scenario == "tool_fail" else "sample.txt"))}
        if scenario == "mcp_disconnect" and selected is None and "mcp__probe__read" in tool_calls:
            deadline = time.monotonic() + 5
            while not (root / "sample.txt.disconnected").exists():
                assert time.monotonic() < deadline, "MCP fixture did not disconnect"
                time.sleep(0.01)
        tool = selected is not None
        if tool:
            tool_calls.append(selected)
        tool_id = "agent-tool-" + str(tool_calls.count("Agent")) if selected == "Agent" else "read-tool"
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
                    "usage": {"input_tokens": 10, "output_tokens": 0, "cache_read_input_tokens": 7, "cache_creation_input_tokens": 11},
                },
            },
            {
                "type": "content_block_start",
                "index": 0,
                "content_block": (
                    {"type": "tool_use", "id": tool_id, "name": selected, "input": {}}
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
                        "partial_json": json.dumps(payload),
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
        interrupt_this_request = scenario == "interrupted" and bool(names)
        if interrupt_this_request:
            events = events[:3]  # Headers and partial content, no final usage.
        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.end_headers()
        for e in events:
            self.wfile.write(
                ("event: " + e["type"] + "\ndata: " + json.dumps(e) + "\n\n").encode()
            )
            self.wfile.flush()
        if interrupt_this_request:
            interrupt_ready.set()
            interrupt_release.wait(15)


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
url = "http://127.0.0.1:" + str(server.server_port)
env = {
    "PATH": os.environ["PATH"],
    "HOME": str(root),
    "ANTHROPIC_BASE_URL": url,
    "ANTHROPIC_API_KEY": "local-test-only",
    "DISABLE_AUTOUPDATER": "1",
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "ENABLE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_METRICS_EXPORTER": "none",
    "OTEL_LOG_TOOL_DETAILS": os.environ.get("OTEL_LOG_TOOL_DETAILS", "1"),
    "OTEL_LOG_TOOL_CONTENT": os.environ.get("OTEL_LOG_TOOL_CONTENT", "0"),
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_ENDPOINT": url,
    "OTEL_TRACES_EXPORT_INTERVAL": "100",
    "OTEL_LOGS_EXPORT_INTERVAL": "100",
    "OTEL_METRIC_EXPORT_INTERVAL": "100",
    "TRACEPARENT": "00-12345678901234567890123456789012-1234567890123456-01",
}
if scenario == "details_off":
    env["OTEL_LOG_TOOL_DETAILS"] = "0"
    env["OTEL_LOG_TOOL_CONTENT"] = "0"
elif scenario == "content_on":
    env["OTEL_LOG_TOOL_CONTENT"] = "1"
injected = {
    "talgent.user_id": "user-probe",
    "talgent.owner_context": "case",
    "talgent.owner_id": "case-probe",
    "talgent.runtime_purpose": "work",
    # "<kind>:<id>", percent-encoded as Agent writes it.
    "talgent.work_caller": "person%3Auser-probe",
    "talgent.agent_work_id": "session-probe",
}
if os.environ.get("NATIVE_TRACE_SCOPE") == "employee":
    injected.pop("talgent.user_id")
    injected.pop("talgent.owner_context")
    injected.pop("talgent.owner_id")
    injected["talgent.runtime_purpose"] = "employee"
    injected["talgent.agent_work_id"] = "employee-session-probe"
    injected["talgent.agent_id"] = "employee-agent-probe"
if scenario != "read":
    injected["talgent.agent_work_id"] += "-" + scenario
env["OTEL_RESOURCE_ATTRIBUTES"] = ",".join(
    key + "=" + value for key, value in injected.items()
)
external_endpoint = os.environ.get("NATIVE_OTLP_ENDPOINT")
if external_endpoint:
    env["OTEL_EXPORTER_OTLP_ENDPOINT"] = external_endpoint
    env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/protobuf"
    env["OTEL_METRICS_EXPORTER"] = "none"
exe = os.environ["CLAUDE_BINARY"]
version = subprocess.check_output([exe, "--version"], text=True).strip()
assert version == "2.1.266 (Claude Code)", version
arguments = [exe, "-p", {"read": "Read sample.txt and reply briefly.", "agent": "Delegate reading sample.txt to a general-purpose agent and reply briefly.", "nested_agent": "Delegate to an agent that delegates reading sample.txt again.", "skill": "Invoke the probe skill.", "mcp": "Use the probe MCP read tool.", "mcp_fail": "Use the probe MCP read tool.", "tool_fail": "Read missing.txt.", "denied": "Read sample.txt.", "skill_invalid": "Invoke the probe skill.", "slash_skill": "/probe", "mcp_connect_fail": "Reply briefly.", "mcp_disconnect": "Use the probe MCP read tool.", "details_off": "Read sample.txt.", "content_on": "Read sample.txt.", "interrupted": "Reply briefly."}[scenario], "--output-format", "json"]
if scenario in ("skill", "skill_invalid", "slash_skill"):
    arguments += ["--allowedTools", "Skill"]
if scenario in ("mcp", "mcp_fail", "mcp_disconnect"):
    config = {"mcpServers": {"probe": {"command": "node", "args": [str(pathlib.Path(__file__).with_name("telemetry-mcp.mjs").resolve()), str(root / ("missing.txt" if scenario == "mcp_fail" else "sample.txt"))]}}}
    if scenario == "mcp_disconnect":
        config["mcpServers"]["probe"]["args"].append("--disconnect")
    arguments += ["--mcp-config", json.dumps(config), "--allowedTools", "mcp__probe__read"]
if scenario == "mcp_connect_fail":
    arguments += ["--mcp-config", json.dumps({"mcpServers": {"probe": {"command": "node", "args": ["-e", "process.exit(1)"]}}})]
try:
    if scenario == "interrupted":
        process = subprocess.Popen(arguments, env=env, cwd=root, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        try:
            assert interrupt_ready.wait(10), "native model stream did not start"
            process.send_signal(signal.SIGINT)
            stdout, stderr = process.communicate(timeout=15)
            result = subprocess.CompletedProcess(arguments, process.returncode, stdout, stderr)
        finally:
            interrupt_release.set()
            if process.poll() is None:
                process.kill()
                process.communicate()
    else:
        result = subprocess.run(arguments, env=env, cwd=root, capture_output=True, text=True, timeout=45)
    (root / "stdout.json").write_text(result.stdout)
    (root / "stderr.log").write_text(result.stderr)
    (root / "telemetry.json").write_text(json.dumps(received))
    (root / "model-requests.json").write_text(json.dumps(calls))
    results = [
        block
        for call in calls
        for message in call.get("messages", [])
        for block in (
            message.get("content", [])
            if isinstance(message.get("content"), list)
            else []
        )
        if block.get("type") == "tool_result"
        and block.get("tool_use_id") == "read-tool"
    ]
    if scenario in ("agent", "nested_agent"):
        background_ids = {
            block["tool_use_id"]
            for call in calls for message in call.get("messages", [])
            for block in (message.get("content", []) if isinstance(message.get("content"), list) else [])
            if block.get("type") == "tool_result" and str(block.get("tool_use_id", "")).startswith("agent-tool-")
            and "Async agent launched successfully" in json.dumps(block.get("content"))
        }
        assert len(background_ids) == (2 if scenario == "nested_agent" else 1), background_ids
    if scenario == "slash_skill":
        assert any("SKILL_PROBE_ACTIVATED" in json.dumps(call.get("messages")) for call in calls)
    assert scenario in ("slash_skill", "mcp_connect_fail", "interrupted") or any(
        (bool(block.get("is_error")) if failed_tool else (
            not block.get("is_error") and ("Launching skill: probe" if scenario == "skill" else "native tool probe") in json.dumps(block.get("content"))
        ))
        for block in results
    )
    if scenario == "interrupted":
        assert json.loads(result.stdout)["terminal_reason"] == "aborted_streaming"
    if external_endpoint:
        assert scenario == "interrupted" or result.returncode == 0, str(root / "stderr.log")
        assert tool_calls == expected_tools, tool_calls
        print(json.dumps({"evidence": str(root), "model_requests": len(calls), "tools": tool_calls, "interrupted_model_requests": int(scenario == "interrupted")}))
        raise SystemExit(0)
    spans = []

    def walk(x):
        if isinstance(x, dict):
            if "spans" in x:
                spans.extend(x["spans"])
            for y in x.values():
                walk(y)
        elif isinstance(x, list):
            for y in x:
                walk(y)

    walk(received)
    resources = [
        resource
        for item in received
        if item["path"] == "/v1/traces"
        for resource in item["data"].get("resourceSpans", [])
    ]
    assert resources
    for resource in resources:
        attributes = {
            entry["key"]: entry["value"].get("stringValue")
            for entry in resource["resource"]["attributes"]
        }
        assert all(
            attributes.get(key) == value for key, value in injected.items()
        ), attributes
    span_injection = [
        all(
            {
                entry["key"]: entry["value"].get("stringValue")
                for entry in span.get("attributes", [])
            }.get(key)
            == value
            for key, value in injected.items()
        )
        for span in spans
    ]
    names = {span.get("name") for span in spans}
    assert scenario == "interrupted" or result.returncode == 0, str(root / "stderr.log")
    assert {
        "claude_code.interaction",
        "claude_code.llm_request",
    } <= names, names
    assert scenario in ("skill_invalid", "slash_skill", "mcp_connect_fail", "interrupted") or "claude_code.tool" in names, names
    assert all(
        span.get("traceId") == "12345678901234567890123456789012" for span in spans if span.get("name") == "claude_code.interaction"
    )
    interaction = next(
        span for span in spans if span.get("name") == "claude_code.interaction"
    )
    if scenario not in ("skill_invalid", "slash_skill", "mcp_connect_fail", "interrupted"):
        tool = next(span for span in spans if span.get("name") == "claude_code.tool")
        assert interaction.get("parentSpanId") == "1234567890123456"
        assert tool.get("parentSpanId") == interaction.get("spanId")
        tool_span_ids = {span.get("spanId") for span in spans if span.get("name") == "claude_code.tool"}
        assert all(
            span.get("parentSpanId") in tool_span_ids
            for span in spans
            if span.get("name")
            in ["claude_code.tool.execution", "claude_code.tool.blocked_on_user"]
        )
    print(
        json.dumps(
            {
                "injected_resource_attributes": injected,
                "all_spans_also_have_attributes": all(span_injection),
                "exit": result.returncode,
                "model_requests": len(calls),
                "received_paths": sorted(set(x["path"] for x in received)),
                "spans": [
                    {
                        "name": x.get("name"),
                        "traceId": x.get("traceId"),
                        "parentSpanId": x.get("parentSpanId"),
                    }
                    for x in spans
                ],
                "evidence": str(root),
            }
        )
    )
finally:
    server.shutdown()
    server.server_close()
