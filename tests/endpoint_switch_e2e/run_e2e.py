"""Exercise endpoint scheduling switches against a running, isolated gateway.

Python standard library only. Both gateway and mock URLs must be loopback URLs.
Run only against a disposable instance; setup creates a local test administrator.
No upstream request leaves the local machine and no API keys are printed.
"""

import argparse
import http.cookiejar
import json
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


MODEL = "gpt-4o-mini"
CLAUDE_MODEL = "claude-3-5-sonnet-20240620"
ENDPOINTS = {
    "/v1/messages": "messages_enabled",
    "/v1/chat/completions": "chat_completions_enabled",
    "/v1/responses": "responses_enabled",
}


class MockState:
    def __init__(self):
        self.lock = threading.Lock()
        self.records = []
        self.fail_labels = set()

    def reset(self, fail_labels=()):
        with self.lock:
            self.records.clear()
            self.fail_labels = set(fail_labels)

    def snapshot(self):
        with self.lock:
            return list(self.records)


MOCK = MockState()


def completion(model, label):
    return {
        "id": "chatcmpl-endpoint-e2e",
        "object": "chat.completion",
        "created": int(time.time()),
        "model": model,
        "choices": [{"index": 0, "message": {"role": "assistant", "content": f"mock answer {label}"}, "finish_reason": "stop"}],
        "usage": {"prompt_tokens": 5, "completion_tokens": 4, "total_tokens": 9},
    }


def responses(model, label):
    return {
        "id": "resp_endpoint_e2e",
        "object": "response",
        "created_at": int(time.time()),
        "status": "completed",
        "model": model,
        "output": [{"type": "message", "id": "msg_endpoint_e2e", "status": "completed", "role": "assistant", "content": [{"type": "output_text", "text": f"mock answer {label}", "annotations": []}]}],
        "usage": {"input_tokens": 5, "output_tokens": 4, "total_tokens": 9},
    }


def message(model, label):
    return {
        "id": "msg_endpoint_e2e", "type": "message", "role": "assistant", "model": model,
        "content": [{"type": "text", "text": f"mock answer {label}"}],
        "stop_reason": "end_turn", "stop_sequence": None,
        "usage": {"input_tokens": 5, "output_tokens": 4},
    }


class MockHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *_args):
        return

    def send_json(self, status, payload):
        raw = json.dumps(payload, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def send_sse(self, events, done=False):
        raw = b"".join(("data: " + json.dumps(event, separators=(",", ":")) + "\n\n").encode() for event in events)
        if done:
            raw += b"data: [DONE]\n\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(raw)))
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        self.wfile.write(raw)
        self.wfile.flush()

    def do_GET(self):
        if self.path == "/health":
            self.send_json(200, {"ok": True})
        elif self.path == "/records":
            self.send_json(200, {"records": MOCK.snapshot()})
        else:
            self.send_json(404, {"error": "not found"})

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        payload = json.loads(raw or b"{}")
        parts = self.path.strip("/").split("/")
        label = parts[0]
        model = payload.get("model", MODEL)
        stream = payload.get("stream") is True
        with MOCK.lock:
            fail = label in MOCK.fail_labels
            MOCK.records.append({"label": label, "path": "/" + "/".join(parts[1:]), "model": model, "stream": stream, "status": 500 if fail else 200})
        if fail:
            self.send_json(500, {"error": {"message": "Intentional local mock failure", "type": "server_error", "code": "mock_retry"}})
            return
        if self.path.endswith("/chat/completions"):
            if stream:
                base = {"id": "chatcmpl-endpoint-e2e", "object": "chat.completion.chunk", "created": int(time.time()), "model": model}
                self.send_sse([
                    {**base, "choices": [{"index": 0, "delta": {"role": "assistant", "content": f"mock answer {label}"}, "finish_reason": None}]},
                    {**base, "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]},
                    {**base, "choices": [], "usage": {"prompt_tokens": 5, "completion_tokens": 4, "total_tokens": 9}},
                ], done=True)
            else:
                self.send_json(200, completion(model, label))
        elif self.path.endswith("/responses"):
            response = responses(model, label)
            if stream:
                self.send_sse([
                    {"type": "response.created", "response": {**response, "status": "in_progress", "output": []}},
                    {"type": "response.output_text.delta", "item_id": "msg_endpoint_e2e", "output_index": 0, "content_index": 0, "delta": f"mock answer {label}"},
                    {"type": "response.completed", "response": response},
                ])
            else:
                self.send_json(200, response)
        elif self.path.endswith("/messages"):
            if stream:
                self.send_sse([
                    {"type": "message_start", "message": {**message(model, label), "content": [], "stop_reason": None}},
                    {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}},
                    {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": f"mock answer {label}"}},
                    {"type": "content_block_stop", "index": 0},
                    {"type": "message_delta", "delta": {"stop_reason": "end_turn"}, "usage": {"output_tokens": 4}},
                    {"type": "message_stop"},
                ])
            else:
                self.send_json(200, message(model, label))
        else:
            self.send_json(404, {"error": {"message": "Unsupported local mock path", "type": "not_found"}})


class E2E:
    def __init__(self, base_url, mock_url, report):
        self.base_url = base_url.rstrip("/")
        self.mock_url = mock_url.rstrip("/")
        self.report = Path(report)
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.user_id = 1
        self.token = ""
        self.channels = {}
        self.results = []

    def request(self, method, path, payload=None, headers=None):
        raw = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
        request_headers = dict(headers or {})
        if raw is not None:
            request_headers["Content-Type"] = "application/json"
        req = urllib.request.Request(self.base_url + path, raw, request_headers, method=method)
        try:
            with self.opener.open(req, timeout=30) as response:
                return response.status, dict(response.headers), response.read()
        except urllib.error.HTTPError as error:
            return error.code, dict(error.headers), error.read()

    def api(self, method, path, payload=None, auth=True):
        for attempt in range(5):
            status, _, raw = self.request(method, path, payload, {"New-Api-User": str(self.user_id)} if auth else {})
            result = json.loads(raw)
            if method == "PUT" and "database is locked" in result.get("message", "") and attempt < 4:
                # Another browser test may save an independent channel while
                # this disposable SQLite instance writes its background logs.
                time.sleep(0.15 * (attempt + 1))
                continue
            break
        assert status == 200 and result.get("success") is True, f"{method} {path}: {status} {result.get('message', '')}"
        return result.get("data")

    def prepare(self):
        setup = self.api("GET", "/api/setup", auth=False)
        if not setup["status"]:
            self.api("POST", "/api/setup", {"username": "epadmin", "password": "EndpointLocalTest2026!", "confirmPassword": "EndpointLocalTest2026!", "SelfUseModeEnabled": True, "DemoSiteEnabled": False}, auth=False)
        login = self.api("POST", "/api/user/login", {"username": "epadmin", "password": "EndpointLocalTest2026!"}, auth=False)
        self.user_id = login["id"]
        # An existing instance with unrelated channels must never be mutated.
        existing = self.api("GET", "/api/channel/?p=1&page_size=100&id_sort=true")["items"]
        assert all(item["name"].startswith("endpoint-e2e-") or item["name"] == "endpoint-ui-test" for item in existing), "Refusing to run with unrelated channels present; use a fresh test database"
        for option, value in [("RetryTimes", "2")]:
            self.api("PUT", "/api/option/", {"key": option, "value": value})
        for label, priority, channel_type, model in [("high", 300, 1, MODEL), ("middle", 200, 1, MODEL), ("low", 100, 1, MODEL), ("claude", 300, 14, CLAUDE_MODEL)]:
            name = "endpoint-e2e-" + label
            channel = {
                "type": channel_type, "key": "local-mock-only", "status": 1, "name": name,
                "weight": 1, "priority": priority, "base_url": self.mock_url + "/" + label,
                "models": model, "group": "default", "auto_ban": 0,
                "setting": "{}", "settings": "{}", "other": "",
            }
            old = next((item for item in existing if item["name"] == name), None)
            if old:
                channel["id"] = old["id"]
                self.api("PUT", "/api/channel/", channel)
            else:
                self.api("POST", "/api/channel/", {"mode": "single", "channel": channel})
        channel_page = self.api("GET", "/api/channel/?p=1&page_size=100&id_sort=true")
        self.channels = {item["name"].removeprefix("endpoint-e2e-"): item for item in channel_page["items"] if item["name"].startswith("endpoint-e2e-")}
        page = self.api("GET", "/api/token/?p=1&size=100")
        token = next((item for item in page["items"] if item["name"] == "endpoint-e2e-local"), None)
        if token is None:
            self.api("POST", "/api/token/", {"name": "endpoint-e2e-local", "expired_time": -1, "unlimited_quota": True, "model_limits_enabled": False, "group": "default"})
            token = next(item for item in self.api("GET", "/api/token/?p=1&size=100")["items"] if item["name"] == "endpoint-e2e-local")
        self.token = "sk-" + self.api("POST", f"/api/token/{token['id']}/key")["key"]
        # Channel creation is picked up by the normal background cache sync.
        # The isolated test instance uses SYNC_FREQUENCY=2.
        time.sleep(2.5)
        print("Initialized isolated endpoint test administrator, token, and 4 loopback-only channels.", flush=True)
        print("Channel IDs: " + json.dumps({label: channel["id"] for label, channel in self.channels.items()}), flush=True)

    def configure(self, label, settings, priority=None):
        channel = dict(self.channels[label])
        channel.pop("key", None)
        channel["setting"] = json.dumps(settings, separators=(",", ":"))
        if priority is not None:
            channel["priority"] = priority
        self.api("PUT", "/api/channel/", channel)
        saved = self.api("GET", f"/api/channel/{channel['id']}")
        assert json.loads(saved.get("setting") or "{}") == settings, "Settings were not persisted exactly"
        self.channels[label] = saved

    def reset(self):
        for label in self.channels:
            self.configure(label, {}, {"high": 300, "middle": 200, "low": 100, "claude": 300}[label])
        MOCK.reset()

    def check_affinity(self):
        original = {item["key"]: item["value"] for item in self.api("GET", "/api/option/")}
        keys = ["channel_affinity_setting.rules", "channel_affinity_setting.enabled"]
        assert all(key in original for key in keys), "Could not snapshot affinity settings"
        rules = [{
            "name": "endpoint local e2e", "model_regex": ["^gpt-4o-mini$"],
            "path_regex": ["^/v1/(messages|chat/completions|responses)$"],
            "key_sources": [{"type": "context_int", "key": "token_id"}],
            "ttl_seconds": 120, "skip_retry_on_failure": False,
            "include_using_group": True, "include_model_name": True, "include_rule_name": True,
        }]
        try:
            self.api("PUT", "/api/option/", {"key": keys[0], "value": json.dumps(rules)})
            self.api("PUT", "/api/option/", {"key": keys[1], "value": "true"})
            for path, setting in ENDPOINTS.items():
                self.api("DELETE", "/api/option/channel_affinity_cache?all=true")
                self.check("affinity cache warmed on high channel: " + path, lambda path=path: self.invoke(path, ["high"]))
                self.configure("middle", {}, 400)
                self.check("affinity stays on high despite higher priority alternative: " + path, lambda path=path: self.invoke(path, ["high"]))
                self.configure("high", {setting: False})
                self.check("disabled endpoint overrides cached affinity: " + path, lambda path=path: self.invoke(path, ["middle"]))
                self.reset()
        finally:
            for key in keys:
                self.api("PUT", "/api/option/", {"key": key, "value": original[key]})
            self.api("DELETE", "/api/option/channel_affinity_cache?all=true")
            self.reset()

    def check_ui_saved_channel(self):
        page = self.api("GET", "/api/channel/?p=1&page_size=100&id_sort=true")
        channel = next(item for item in page["items"] if item["name"] == "endpoint-ui-test")
        assert channel["models"] == "gpt-4o", "UI verification channel must use isolated gpt-4o model"
        assert channel["base_url"] == self.mock_url + "/ui", "UI verification channel must use the loopback mock"
        settings = json.loads(channel["setting"])
        assert settings["responses_enabled"] is True and settings["messages_enabled"] is False and settings["chat_completions_enabled"] is False, "UI-saved switch values do not match the requested manual test state"
        for path in ["/v1/messages", "/v1/chat/completions"]:
            self.check("UI saved disabled switch produces 503 without upstream: " + path, lambda path=path: self.invoke(path, [], 503, model="gpt-4o"))
        self.check("UI saved Responses remains enabled", lambda: self.invoke("/v1/responses", ["ui"], model="gpt-4o"))
        saved = self.api("GET", f"/api/channel/{channel['id']}")
        assert saved["setting"] == channel["setting"], "UI channel settings changed unexpectedly"

    def invoke(self, path, expected_labels, expected_status=200, stream=False, specific=None, model=MODEL, fail_labels=()):
        MOCK.reset(fail_labels)
        payload = {"model": model, "stream": stream, "max_tokens": 32}
        if path == "/v1/responses":
            payload.pop("max_tokens")
            payload.update({"input": "local endpoint switch test", "max_output_tokens": 32})
        else:
            payload["messages"] = [{"role": "user", "content": "local endpoint switch test"}]
        auth = self.token if specific is None else self.token + "-" + str(self.channels[specific]["id"])
        status, headers, raw = self.request("POST", path, payload, {"Authorization": "Bearer " + auth, "anthropic-version": "2023-06-01", "Accept": "text/event-stream" if stream else "application/json"})
        records = MOCK.snapshot()
        labels = [record["label"] for record in records]
        assert status == expected_status, f"{path}: expected HTTP {expected_status}, got {status}: {raw[:700].decode(errors='replace')}"
        assert labels == expected_labels, f"{path}: expected upstream sequence {expected_labels}, got {labels}"
        if status == 200:
            assert b"mock answer" in raw, f"{path}: missing completion text: {raw[:500]!r}"
            if stream:
                assert any(key.lower() == "content-type" and value.startswith("text/event-stream") for key, value in headers.items()), f"{path}: expected SSE Content-Type"
                assert b"data:" in raw, f"{path}: expected SSE data"
        else:
            result = json.loads(raw)
            assert result.get("error"), f"{path}: expected protocol error response"
        return {"http_status": status, "upstream_requests": records, "stream": stream, "specific_channel": specific}

    def check(self, name, function):
        started = time.monotonic()
        try:
            detail = function()
        except Exception as error:
            self.results.append({"case": name, "status": "FAIL", "error": str(error), "seconds": round(time.monotonic() - started, 3)})
            self.write_report()
            raise
        self.results.append({"case": name, "status": "PASS", "detail": detail, "seconds": round(time.monotonic() - started, 3)})
        self.write_report()
        print("PASS " + name, flush=True)

    def write_report(self):
        self.report.parent.mkdir(parents=True, exist_ok=True)
        self.report.write_text(json.dumps({"run_at_utc": datetime.now(timezone.utc).isoformat(), "base_url": self.base_url, "mock_url": self.mock_url, "model": MODEL, "channels": {label: channel["id"] for label, channel in self.channels.items()}, "results": self.results}, indent=2, ensure_ascii=False) + "\n")

    def run(self, verify_ui=False):
        self.prepare()
        for path in ENDPOINTS:
            self.check("omitted setting defaults enabled: " + path, lambda path=path: self.invoke(path, ["high"]))

        for path, setting in ENDPOINTS.items():
            self.configure("high", {setting: False})
            self.check("disabled high priority channel skipped: " + path, lambda path=path: self.invoke(path, ["middle"]))
            for other in ENDPOINTS:
                if other != path:
                    self.check("independent switches: " + path + " disabled; " + other + " remains enabled", lambda other=other: self.invoke(other, ["high"]))
            self.check("streaming skips disabled channel: " + path, lambda path=path: self.invoke(path, ["middle"], stream=True))
            self.check("specific disabled channel rejected: " + path, lambda path=path: self.invoke(path, [], 503, specific="high"))
            self.configure("middle", {setting: False})
            self.configure("low", {setting: False})
            self.check("all channels disabled produces 503 without upstream: " + path, lambda path=path: self.invoke(path, [], 503))
            self.check("all channels disabled rejects streaming before upstream: " + path, lambda path=path: self.invoke(path, [], 503, stream=True))
            self.configure("high", {setting: True})
            self.check("explicit re-enable immediately restores channel: " + path, lambda path=path: self.invoke(path, ["high"]))
            self.check("specific enabled channel works: " + path, lambda path=path: self.invoke(path, ["high"], specific="high"))
            self.reset()

        for path, setting in ENDPOINTS.items():
            self.configure("middle", {setting: False})
            self.check("500 retry skips disabled middle channel: " + path, lambda path=path: self.invoke(path, ["high", "low"], fail_labels=["high"]))
            self.configure("high", {"fallback_channel_ids": [self.channels["middle"]["id"], self.channels["low"]["id"]]})
            self.check("500 explicit fallback skips disabled middle channel: " + path, lambda path=path: self.invoke(path, ["high", "low"], fail_labels=["high"]))
            self.reset()

        self.check("native Claude messages defaults enabled", lambda: self.invoke("/v1/messages", ["claude"], model=CLAUDE_MODEL))
        self.configure("claude", {"messages_enabled": False})
        self.check("native Claude messages disabled has zero upstream calls", lambda: self.invoke("/v1/messages", [], 503, model=CLAUDE_MODEL))
        self.configure("claude", {"messages_enabled": True})
        self.check("native Claude messages re-enabled streaming", lambda: self.invoke("/v1/messages", ["claude"], stream=True, model=CLAUDE_MODEL))
        self.reset()
        self.check_affinity()
        if verify_ui:
            self.check_ui_saved_channel()
        print(f"All {len(self.results)} HTTP end-to-end cases passed. Report: {self.report}", flush=True)


def loopback_url(value):
    url = urllib.parse.urlsplit(value)
    if url.scheme != "http" or url.hostname not in {"127.0.0.1", "localhost", "::1"}:
        raise argparse.ArgumentTypeError("Only loopback http URLs are accepted")
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:3000", type=loopback_url)
    parser.add_argument("--mock-port", default=33091, type=int)
    parser.add_argument("--report", default="/tmp/onesapi-endpoint-switch-e2e-report.json")
    parser.add_argument("--hold-mock", action="store_true", help="Keep local mock running after tests for manual browser verification")
    parser.add_argument("--verify-ui", action="store_true", help="Also verify the separately browser-created endpoint-ui-test channel")
    args = parser.parse_args()
    server = ThreadingHTTPServer(("127.0.0.1", args.mock_port), MockHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        E2E(args.base_url, f"http://127.0.0.1:{args.mock_port}", args.report).run(verify_ui=args.verify_ui)
        if args.hold_mock:
            print("Mock remains available for manual verification; press Ctrl-C to stop.", flush=True)
            while True:
                time.sleep(1)
    except KeyboardInterrupt:
        pass
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
