"""Check response-model restoration against a fresh, disposable local gateway.

Uses only Python's standard library. All upstream traffic goes to an embedded
loopback mock. Refuses an initialized gateway, so existing accounts/channels
cannot be overwritten. No production API keys are required or printed.
"""

import argparse
import http.cookiejar
import ipaddress
import json
import secrets
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


UPSTREAM_REQUEST_MODEL = "upstream-B"
UPSTREAM_RESPONSE_MODEL = "upstream-C"
ANSWER = 'answer mentions upstream-C and {"model":"upstream-C"}'
SIGNATURE = "existing-upstream-signature"
SPECS = {
    "chat": (1, "/v1/chat/completions", "restore-e2e-chat-A"),
    "responses": (1, "/v1/responses", "restore-e2e-responses-A"),
    "claude": (14, "/v1/messages", "restore-e2e-claude-A"),
}


def loopback_url(value):
    parsed = urllib.parse.urlsplit(value)
    assert parsed.scheme in ("http", "https"), "Only HTTP(S) loopback URLs are allowed"
    assert not parsed.username and not parsed.password, "URL credentials are not allowed"
    assert parsed.path in ("", "/") and not parsed.query and not parsed.fragment, "Use a gateway origin URL"
    host = parsed.hostname or ""
    try:
        is_loopback = ipaddress.ip_address(host).is_loopback
    except ValueError:
        is_loopback = host.lower() == "localhost"
    assert is_loopback, "Refusing to use a non-loopback gateway"
    return value.rstrip("/")


def chat_response():
    return {
        "id": "chatcmpl-restore-e2e", "object": "chat.completion",
        "created": int(time.time()), "model": UPSTREAM_RESPONSE_MODEL,
        "choices": [{"index": 0, "message": {"role": "assistant", "content": ANSWER}, "finish_reason": "stop"}],
        "usage": {"prompt_tokens": 7, "completion_tokens": 5, "total_tokens": 12},
    }


def responses_response():
    return {
        "id": "resp_restore_e2e", "object": "response", "status": "completed",
        "created_at": int(time.time()), "model": UPSTREAM_RESPONSE_MODEL,
        "output": [{"type": "message", "id": "msg_restore_e2e", "status": "completed", "role": "assistant", "content": [{"type": "output_text", "text": ANSWER, "annotations": []}]}],
        "usage": {"input_tokens": 7, "output_tokens": 5, "total_tokens": 12},
    }


def claude_response():
    return {
        "id": "msg_restore_e2e", "type": "message", "role": "assistant", "model": UPSTREAM_RESPONSE_MODEL,
        "content": [{"type": "thinking", "thinking": "reason mentions upstream-C", "signature": SIGNATURE}, {"type": "text", "text": ANSWER}],
        "stop_reason": "end_turn", "stop_sequence": None,
        "usage": {"input_tokens": 7, "output_tokens": 5},
    }


class MockHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *_args):
        pass

    def send_json(self, status, payload):
        raw = json.dumps(payload, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def send_sse(self, events, done=False):
        lines = []
        for event in events:
            if event.get("type"):
                lines.append("event: " + event["type"] + "\n")
            lines.append("data: " + json.dumps(event, separators=(",", ":")) + "\n\n")
        if done:
            lines.append("data: [DONE]\n\n")
        raw = "".join(lines).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(raw)))
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()
        self.wfile.write(raw)
        self.wfile.flush()

    def do_POST(self):
        payload = json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))) or b"{}")
        label = self.path.strip("/").split("/")[0]
        with self.server.records_lock:
            self.server.records.append({"label": label, "path": self.path, "model": payload.get("model"), "stream": payload.get("stream", False)})
        stream = payload.get("stream") is True
        if self.path.endswith("/chat/completions"):
            if stream:
                base = {"id": "chatcmpl-restore-e2e", "object": "chat.completion.chunk", "created": int(time.time()), "model": UPSTREAM_RESPONSE_MODEL}
                self.send_sse([
                    {**base, "choices": [{"index": 0, "delta": {"role": "assistant", "content": ANSWER}, "finish_reason": None}]},
                    {**base, "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]},
                    {**base, "choices": [], "usage": {"prompt_tokens": 7, "completion_tokens": 5, "total_tokens": 12}},
                ], done=True)
            else:
                self.send_json(200, chat_response())
        elif self.path.endswith("/responses"):
            response = responses_response()
            if stream:
                self.send_sse([
                    {"type": "response.created", "response": {**response, "status": "in_progress", "output": []}},
                    {"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "item_id": "msg_restore_e2e", "delta": ANSWER},
                    {"type": "response.completed", "response": response},
                ])
            else:
                self.send_json(200, response)
        elif self.path.endswith("/messages"):
            response = claude_response()
            if stream:
                self.send_sse([
                    {"type": "message_start", "message": {**response, "content": [], "stop_reason": None}},
                    {"type": "content_block_start", "index": 0, "content_block": {"type": "thinking", "thinking": ""}},
                    {"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": "reason mentions upstream-C"}},
                    {"type": "content_block_delta", "index": 0, "delta": {"type": "signature_delta", "signature": SIGNATURE}},
                    {"type": "content_block_stop", "index": 0},
                    {"type": "content_block_start", "index": 1, "content_block": {"type": "text", "text": ""}},
                    {"type": "content_block_delta", "index": 1, "delta": {"type": "text_delta", "text": ANSWER}},
                    {"type": "content_block_stop", "index": 1},
                    {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 5}},
                    {"type": "message_stop"},
                ])
            else:
                self.send_json(200, response)
        else:
            self.send_json(404, {"error": {"message": "Unknown mock endpoint"}})


class E2E:
    def __init__(self, base_url, mock, report):
        self.base_url = loopback_url(base_url)
        self.mock = mock
        self.mock_url = f"http://127.0.0.1:{mock.server_port}"
        self.report = Path(report)
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.user_id = 1
        self.token = ""
        self.token_id = None
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
            if "database is locked" in result.get("message", "") and attempt < 4:
                time.sleep(0.1 * (attempt + 1))
                continue
            break
        assert status == 200 and result.get("success") is True, f"{method} {path}: {status} {result.get('message', '')}"
        return result.get("data")

    def prepare(self):
        setup = self.api("GET", "/api/setup", auth=False)
        assert not setup["status"] and not setup.get("root_init"), "Refusing to modify an initialized gateway; start with a fresh disposable database"
        password = secrets.token_urlsafe(18)
        self.api("POST", "/api/setup", {"username": "restoretest", "password": password, "confirmPassword": password, "SelfUseModeEnabled": True, "DemoSiteEnabled": False}, auth=False)
        login = self.api("POST", "/api/user/login", {"username": "restoretest", "password": password}, auth=False)
        self.user_id = login["id"]
        assert not self.api("GET", "/api/channel/?p=1&page_size=100")["items"], "Refusing a database with unrelated channels"
        assert not self.api("GET", "/api/token/?p=1&size=100")["items"], "Refusing a database with unrelated tokens"
        for key, value in [("RetryTimes", "0"), ("LogConsumeEnabled", "true"), ("ModelRatio", json.dumps({spec[2]: 0 for spec in SPECS.values()}))]:
            self.api("PUT", "/api/option/", {"key": key, "value": value})
        for label, (channel_type, _, model) in SPECS.items():
            channel = {"name": "response-model-e2e-" + label, "type": channel_type, "key": "loopback-mock-only", "status": 1, "weight": 1, "priority": 100, "base_url": self.mock_url + "/" + label, "models": model, "group": "default", "model_mapping": json.dumps({model: UPSTREAM_REQUEST_MODEL}), "setting": "{}", "settings": "{}", "other": "", "auto_ban": 0}
            self.api("POST", "/api/channel/", {"mode": "single", "channel": channel})
        page = self.api("GET", "/api/channel/?p=1&page_size=100")
        self.channels = {c["name"].removeprefix("response-model-e2e-"): c for c in page["items"]}
        self.api("POST", "/api/token/", {"name": "response-model-e2e", "expired_time": -1, "unlimited_quota": True, "model_limits_enabled": False, "group": "default"})
        self.token_id = self.api("GET", "/api/token/?p=1&size=100")["items"][0]["id"]
        self.token = "sk-" + self.api("POST", f"/api/token/{self.token_id}/key")["key"]

    def configure(self, label, enabled, buffered, explicit=False):
        channel = dict(self.channels[label])
        channel.pop("key", None)
        setting = {"force_stream": True} if buffered else {}
        if enabled or explicit:
            setting["restore_response_model"] = enabled
        channel["setting"] = json.dumps(setting)
        self.api("PUT", "/api/channel/", channel)
        saved = self.api("GET", f"/api/channel/{channel['id']}")
        assert json.loads(saved.get("setting") or "{}") == setting, "Channel settings were not persisted"
        self.channels[label] = saved

    def consume_log(self, request_id):
        assert request_id, "Relay response omitted Request ID"
        query = urllib.parse.urlencode({"p": 1, "page_size": 100, "request_id": request_id})
        for _ in range(50):
            page = self.api("GET", "/api/log/?" + query)
            log = next((row for row in page["items"] if row["type"] == 2), None)
            if log is not None:
                return log
            time.sleep(0.1)
        raise AssertionError("Consume log did not appear: " + request_id)

    @staticmethod
    def events(raw):
        return [json.loads(line[5:].strip()) for line in raw.decode().splitlines() if line.startswith("data:") and line[5:].strip() != "[DONE]"]

    def check(self, label, enabled, stream=False, buffered=False, explicit=False):
        self.configure(label, enabled, buffered, explicit)
        _, path, alias = SPECS[label]
        payload = {"model": alias, "stream": stream}
        if label == "responses":
            payload["input"] = "hello"
        else:
            payload["messages"] = [{"role": "user", "content": "hello"}]
            payload["max_tokens"] = 2048
        with self.mock.records_lock:
            before = len(self.mock.records)
        status, headers, raw = self.request("POST", path, payload, {"Authorization": "Bearer " + self.token, "anthropic-version": "2023-06-01"})
        assert status == 200, f"{label}: HTTP {status}: {raw[:500]!r}"
        with self.mock.records_lock:
            upstream = self.mock.records[before:]
        assert len(upstream) == 1 and upstream[0]["label"] == label, upstream
        assert upstream[0]["model"] == UPSTREAM_REQUEST_MODEL, upstream
        assert upstream[0]["stream"] is (stream or buffered), upstream
        expected_model = alias if enabled else UPSTREAM_RESPONSE_MODEL
        if stream:
            events = self.events(raw)
            if label == "chat":
                models = [event["model"] for event in events if "model" in event]
                text = "".join(choice.get("delta", {}).get("content", "") for event in events for choice in event.get("choices", []))
                assert b"data: [DONE]" in raw
            elif label == "responses":
                models = [event["response"]["model"] for event in events if event.get("response", {}).get("model")]
                text = "".join(event.get("delta", "") for event in events if event.get("type") == "response.output_text.delta")
                assert any(event.get("type") == "response.completed" for event in events)
            else:
                models = [event["message"]["model"] for event in events if event.get("message", {}).get("model")]
                text = "".join(event.get("delta", {}).get("text", "") for event in events)
                signatures = [event["delta"]["signature"] for event in events if event.get("delta", {}).get("type") == "signature_delta"]
                assert signatures == [SIGNATURE], "Existing upstream signature was overwritten"
                assert any(event.get("type") == "message_stop" for event in events)
            assert models and all(model == expected_model for model in models), (expected_model, models)
        else:
            response = json.loads(raw)
            assert response["model"] == expected_model, (expected_model, response)
            if label == "chat":
                text = response["choices"][0]["message"]["content"]
            elif label == "responses":
                text = response["output"][0]["content"][0]["text"]
            else:
                text = next(block["text"] for block in response["content"] if block["type"] == "text")
                thinking = next(block for block in response["content"] if block["type"] == "thinking")
                assert thinking["signature"] == SIGNATURE
                assert thinking["thinking"] == "reason mentions upstream-C"
            content_length = next((value for key, value in headers.items() if key.lower() == "content-length"), None)
            if content_length is not None:
                assert int(content_length) == len(raw), "Rewritten Content-Length is incorrect"
        assert text == ANSWER, "Model rewriting changed generated content"
        request_id = next((value for key, value in headers.items() if key.lower() == "x-oneapi-request-id"), "")
        log = self.consume_log(request_id)
        assert log["model_name"] == alias, log
        other = json.loads(log["other"] or "{}")
        if enabled:
            assert "is_model_mapped" not in other and "upstream_model_name" not in other, other
        else:
            assert other.get("is_model_mapped") is True and other.get("upstream_model_name"), other
        result = {"protocol": label, "enabled": enabled, "stream": stream, "force_stream_buffer": buffered, "default_setting": not enabled and not explicit, "http": status, "upstream_received_model": UPSTREAM_REQUEST_MODEL, "client_response_model": expected_model, "redirect_marker_hidden": enabled, "content_preserved": True, "request_id": request_id}
        self.results.append(result)
        print(json.dumps(result, ensure_ascii=False), flush=True)

    def run(self):
        self.prepare()
        try:
            for label in SPECS:
                for enabled in (False, True):
                    self.check(label, enabled)
                    self.check(label, enabled, stream=True)
                    self.check(label, enabled, buffered=True)
                self.check(label, False, explicit=True)
        finally:
            if self.token_id is not None:
                self.api("DELETE", f"/api/token/{self.token_id}")
            for channel in self.channels.values():
                self.api("DELETE", f"/api/channel/{channel['id']}")
            report = {"base_url": self.base_url, "all_upstream_urls_loopback": True, "cases_passed": len(self.results), "expected_cases": 21, "temporary_tokens_and_channels_deleted": True, "results": self.results}
            self.report.parent.mkdir(parents=True, exist_ok=True)
            self.report.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
        assert len(self.results) == 21
        print(f"Passed all 21 response-model cases; report: {self.report}", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True, help="Fresh disposable loopback gateway origin")
    parser.add_argument("--report", default="logs/response-model-e2e-report.json")
    args = parser.parse_args()
    base_url = loopback_url(args.base_url)
    mock = ThreadingHTTPServer(("127.0.0.1", 0), MockHandler)
    mock.records = []
    mock.records_lock = threading.Lock()
    threading.Thread(target=mock.serve_forever, daemon=True).start()
    try:
        E2E(base_url, mock, args.report).run()
    finally:
        mock.shutdown()
        mock.server_close()


if __name__ == "__main__":
    main()
