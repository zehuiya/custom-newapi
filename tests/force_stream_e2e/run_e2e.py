import argparse
import http.cookiejar
import json
import time
import urllib.error
import urllib.parse
import urllib.request


OPENAI_FORCE_MODEL = "gpt-4o-mini"
OPENAI_CONTROL_MODEL = "gpt-4o"
CLAUDE_FORCE_MODEL = "claude-3-5-sonnet-20240620"
CLAUDE_CONTROL_MODEL = "claude-3-opus-20240229"


class E2E:
    def __init__(self, base_url, mock_url):
        self.base_url = base_url.rstrip("/")
        self.mock_url = mock_url.rstrip("/")
        self.cookies = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cookies))
        self.token = ""
        self.results = []
        self.channel_ids = {}

    def request(self, method, path, payload=None, headers=None, timeout=30):
        body = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
        request_headers = dict(headers or {})
        if payload is not None:
            request_headers.setdefault("Content-Type", "application/json")
        request = urllib.request.Request(self.base_url + path, data=body, headers=request_headers, method=method)
        try:
            with self.opener.open(request, timeout=timeout) as response:
                raw = response.read()
                return response.status, dict(response.headers.items()), raw
        except urllib.error.HTTPError as error:
            return error.code, dict(error.headers.items()), error.read()

    def mock_request(self, method, path, payload=None):
        body = None if payload is None else json.dumps(payload).encode()
        request = urllib.request.Request(self.mock_url + path, data=body, method=method)
        with urllib.request.urlopen(request, timeout=10) as response:
            return json.loads(response.read())

    @staticmethod
    def decode_json(raw):
        return json.loads(raw.decode())

    def api_json(self, method, path, payload=None, auth=True):
        headers = {}
        if auth:
            headers["New-Api-User"] = "1"
        status, _, raw = self.request(method, path, payload, headers)
        data = self.decode_json(raw)
        assert status == 200, (path, status, data)
        assert data.get("success") is True, (path, data)
        return data.get("data")

    def wait_ready(self):
        deadline = time.time() + 180
        last_error = None
        while time.time() < deadline:
            try:
                status, _, raw = self.request("GET", "/api/status", timeout=3)
                if status == 200 and self.decode_json(raw).get("success") is True:
                    return
            except Exception as error:  # noqa: BLE001
                last_error = error
            time.sleep(1)
        raise AssertionError(f"NewAPI did not become ready: {last_error}")

    def setup(self):
        self.wait_ready()
        _, _, raw = self.request("GET", "/api/setup")
        setup = self.decode_json(raw)
        if not setup["data"]["status"]:
            status, _, raw = self.request(
                "POST",
                "/api/setup",
                {
                    "username": "root",
                    "password": "ForceStream123!",
                    "confirmPassword": "ForceStream123!",
                    "SelfUseModeEnabled": True,
                    "DemoSiteEnabled": False,
                },
            )
            result = self.decode_json(raw)
            assert status == 200 and result.get("success") is True, result
        status, _, raw = self.request("POST", "/api/user/login", {"username": "root", "password": "ForceStream123!"})
        login = self.decode_json(raw)
        assert status == 200 and login.get("success") is True, login

        token_page = self.api_json("GET", "/api/token/?p=1&size=100")
        existing = next((item for item in token_page["items"] if item["name"] == "force-stream-e2e"), None)
        if existing is None:
            self.api_json(
                "POST",
                "/api/token/",
                {
                    "name": "force-stream-e2e",
                    "expired_time": -1,
                    "unlimited_quota": True,
                    "model_limits_enabled": False,
                    "model_limits": "",
                    "group": "default",
                },
            )
            token_page = self.api_json("GET", "/api/token/?p=1&size=100")
            existing = next(item for item in token_page["items"] if item["name"] == "force-stream-e2e")
        key_data = self.api_json("POST", f"/api/token/{existing['id']}/key")
        self.token = "sk-" + key_data["key"]

    def create_channels(self):
        channel_page = self.api_json("GET", "/api/channel/?p=1&page_size=100&id_sort=true")
        existing_names = {item["name"] for item in channel_page["items"]}
        specs = [
            ("e2e-openai-force", 1, OPENAI_FORCE_MODEL, True, {"stream": False}),
            ("e2e-openai-control", 1, OPENAI_CONTROL_MODEL, False, None),
            ("e2e-claude-force", 14, CLAUDE_FORCE_MODEL, True, {"stream": False}),
            ("e2e-claude-control", 14, CLAUDE_CONTROL_MODEL, False, None),
        ]
        for name, channel_type, model, enabled, override in specs:
            if name in existing_names:
                continue
            setting = {"force_stream": enabled, "proxy": ""}
            channel = {
                "type": channel_type,
                "key": "mock-upstream-key",
                "status": 1,
                "name": name,
                "weight": 1,
                "priority": 100,
                "base_url": "http://mock-upstream:18080",
                "models": model,
                "group": "default",
                "auto_ban": 0,
                "setting": json.dumps(setting, separators=(",", ":")),
                "settings": "{}",
                "other": "",
            }
            if override is not None:
                channel["param_override"] = json.dumps(override, separators=(",", ":"))
                channel["header_override"] = json.dumps({"Accept": "application/json"}, separators=(",", ":"))
            result = self.api_json("POST", "/api/channel/", {"mode": "single", "channel": channel})
            assert result is None
        time.sleep(2.5)
        channel_page = self.api_json("GET", "/api/channel/?p=1&page_size=100&id_sort=true")
        self.channel_ids = {item["name"]: item["id"] for item in channel_page["items"]}
        for name, *_ in specs:
            assert name in self.channel_ids, f"channel was not created: {name}"

    def clear_records(self):
        self.mock_request("DELETE", "/records")

    def latest_record(self):
        records = self.mock_request("GET", "/records")["records"]
        assert records, "mock upstream did not receive a request"
        return records[-1]

    def invoke(self, path, payload, accept="application/json"):
        self.clear_records()
        status, headers, raw = self.request(
            "POST",
            path,
            payload,
            {"Authorization": "Bearer " + self.token, "Accept": accept, "anthropic-version": "2023-06-01"},
            timeout=45,
        )
        assert status == 200, (path, status, raw.decode(errors="replace"))
        return headers, raw, self.latest_record()

    def invoke_channel_test(self, channel_name, model, endpoint_type=""):
        self.clear_records()
        query = {"model": model}
        if endpoint_type:
            query["endpoint_type"] = endpoint_type
        path = f"/api/channel/test/{self.channel_ids[channel_name]}?{urllib.parse.urlencode(query)}"
        status, _, raw = self.request("GET", path, headers={"New-Api-User": "1"})
        response = self.decode_json(raw)
        assert status == 200 and response.get("success") is True, response
        return self.latest_record()

    def check(self, name, function):
        started = time.time()
        function()
        self.results.append({"case": name, "status": "PASS", "seconds": round(time.time() - started, 3)})

    @staticmethod
    def assert_sse(raw, expected):
        text = raw.decode()
        assert "data:" in text, text[:800]
        if expected in text:
            return

        content = []
        for line in text.splitlines():
            if not line.startswith("data:"):
                continue
            payload = line[5:].strip()
            if not payload or payload == "[DONE]":
                continue
            try:
                event = json.loads(payload)
            except json.JSONDecodeError:
                continue
            for choice in event.get("choices", []):
                delta = choice.get("delta") or {}
                if isinstance(delta.get("content"), str):
                    content.append(delta["content"])

        assert expected == "".join(content), text[:800]

    def run(self):
        self.setup()
        self.create_channels()

        before_logs = self.api_json("GET", "/api/log/?p=1&page_size=100")
        before_log_ids = {item["id"] for item in before_logs["items"]}

        def openai_force_nonstream():
            headers, raw, upstream = self.invoke(
                "/v1/chat/completions",
                {"model": OPENAI_FORCE_MODEL, "messages": [{"role": "user", "content": "test"}], "stream": False, "max_tokens": 64},
            )
            response = self.decode_json(raw)
            assert not headers.get("Content-Type", "").startswith("text/event-stream")
            assert response["choices"][0]["message"]["reasoning_content"] == "mock reasoning"
            assert response["choices"][0]["message"]["content"] == "mock answer"
            assert response["choices"][0]["message"]["tool_calls"][0]["function"]["arguments"] == '{"q":"force"}'
            assert response["usage"]["prompt_tokens"] == 11 and response["usage"]["completion_tokens"] == 9
            assert upstream["stream"] is True and upstream["accept"] == "text/event-stream"

        def openai_force_stream():
            headers, raw, upstream = self.invoke(
                "/v1/chat/completions",
                {"model": OPENAI_FORCE_MODEL, "messages": [{"role": "user", "content": "test"}], "stream": True, "stream_options": {"include_usage": True}},
                "text/event-stream",
            )
            assert headers.get("Content-Type", "").startswith("text/event-stream")
            self.assert_sse(raw, "mock answer")
            assert upstream["stream"] is True

        def openai_control_nonstream():
            _, raw, upstream = self.invoke(
                "/v1/chat/completions",
                {"model": OPENAI_CONTROL_MODEL, "messages": [{"role": "user", "content": "test"}], "stream": False},
            )
            assert self.decode_json(raw)["choices"][0]["message"]["content"] == "mock answer"
            assert upstream["stream"] is False

        def claude_force_nonstream():
            headers, raw, upstream = self.invoke(
                "/v1/messages",
                {"model": CLAUDE_FORCE_MODEL, "max_tokens": 64, "messages": [{"role": "user", "content": "test"}], "stream": False},
            )
            response = self.decode_json(raw)
            assert not headers.get("Content-Type", "").startswith("text/event-stream")
            assert response["content"][0]["thinking"] == "mock reasoning"
            assert response["content"][0]["signature"] == "sig-force-e2e"
            assert response["content"][1]["text"] == "mock answer"
            assert response["content"][2]["input"] == {"q": "force"}
            assert response["usage"]["input_tokens"] == 13 and response["usage"]["output_tokens"] == 10
            assert upstream["stream"] is True and upstream["accept"] == "text/event-stream"

        def claude_force_stream():
            headers, raw, upstream = self.invoke(
                "/v1/messages",
                {"model": CLAUDE_FORCE_MODEL, "max_tokens": 64, "messages": [{"role": "user", "content": "test"}], "stream": True},
                "text/event-stream",
            )
            assert headers.get("Content-Type", "").startswith("text/event-stream")
            self.assert_sse(raw, "thinking_delta")
            assert upstream["stream"] is True

        def claude_control_nonstream():
            _, raw, upstream = self.invoke(
                "/v1/messages",
                {"model": CLAUDE_CONTROL_MODEL, "max_tokens": 64, "messages": [{"role": "user", "content": "test"}], "stream": False},
            )
            assert self.decode_json(raw)["content"][1]["text"] == "mock answer"
            assert upstream["stream"] is False

        def responses_force_nonstream():
            headers, raw, upstream = self.invoke(
                "/v1/responses",
                {"model": OPENAI_FORCE_MODEL, "input": "test", "stream": False},
            )
            response = self.decode_json(raw)
            assert not headers.get("Content-Type", "").startswith("text/event-stream")
            assert response["status"] == "completed"
            assert response["output"][1]["content"][0]["text"] == "mock answer"
            assert response["output"][2]["arguments"] == '{"q":"force"}'
            assert response["provider_extension"]["preserved"] is True
            assert response["usage"]["input_tokens"] == 17 and response["usage"]["output_tokens"] == 12
            assert upstream["stream"] is True and upstream["accept"] == "text/event-stream"

        def responses_force_stream():
            headers, raw, upstream = self.invoke(
                "/v1/responses",
                {"model": OPENAI_FORCE_MODEL, "input": "test", "stream": True},
                "text/event-stream",
            )
            assert headers.get("Content-Type", "").startswith("text/event-stream")
            self.assert_sse(raw, "response.completed")
            assert upstream["stream"] is True

        def responses_control_nonstream():
            _, raw, upstream = self.invoke(
                "/v1/responses",
                {"model": OPENAI_CONTROL_MODEL, "input": "test", "stream": False},
            )
            assert self.decode_json(raw)["output"][1]["content"][0]["text"] == "mock answer"
            assert upstream["stream"] is False

        def claude_client_openai_upstream():
            _, raw, upstream = self.invoke(
                "/v1/messages",
                {"model": OPENAI_FORCE_MODEL, "max_tokens": 64, "messages": [{"role": "user", "content": "test"}], "stream": False},
            )
            response = self.decode_json(raw)
            assert upstream["path"].endswith("/chat/completions") and upstream["stream"] is True
            assert any(block.get("type") == "thinking" and block.get("thinking") == "mock reasoning" for block in response["content"])
            assert any(block.get("type") == "tool_use" for block in response["content"])
            assert response["stop_reason"] == "tool_use"

        def openai_client_claude_upstream():
            _, raw, upstream = self.invoke(
                "/v1/chat/completions",
                {"model": CLAUDE_FORCE_MODEL, "messages": [{"role": "user", "content": "test"}], "stream": False, "max_tokens": 64},
            )
            response = self.decode_json(raw)
            assert upstream["path"].endswith("/messages") and upstream["stream"] is True
            assert response["choices"][0]["message"]["content"] == "mock answer"
            assert response["choices"][0]["message"]["tool_calls"][0]["function"]["name"] == "lookup"

        def openai_channel_test():
            upstream = self.invoke_channel_test("e2e-openai-force", OPENAI_FORCE_MODEL)
            assert upstream["path"].endswith("/chat/completions")
            assert upstream["stream"] is True and upstream["accept"] == "text/event-stream"

        def claude_channel_test():
            upstream = self.invoke_channel_test("e2e-claude-force", CLAUDE_FORCE_MODEL, "anthropic")
            assert upstream["path"].endswith("/messages")
            assert upstream["stream"] is True and upstream["accept"] == "text/event-stream"

        def responses_channel_test():
            upstream = self.invoke_channel_test("e2e-openai-force", OPENAI_FORCE_MODEL, "openai-response")
            assert upstream["path"].endswith("/responses")
            assert upstream["stream"] is True and upstream["accept"] == "text/event-stream"

        cases = [
            ("OpenAI Chat force/non-stream", openai_force_nonstream),
            ("OpenAI Chat force/stream", openai_force_stream),
            ("OpenAI Chat control/non-stream", openai_control_nonstream),
            ("Anthropic force/non-stream", claude_force_nonstream),
            ("Anthropic force/stream", claude_force_stream),
            ("Anthropic control/non-stream", claude_control_nonstream),
            ("Responses force/non-stream", responses_force_nonstream),
            ("Responses force/stream", responses_force_stream),
            ("Responses control/non-stream", responses_control_nonstream),
            ("Anthropic client -> OpenAI upstream", claude_client_openai_upstream),
            ("OpenAI client -> Anthropic upstream", openai_client_claude_upstream),
            ("Channel test/OpenAI Chat", openai_channel_test),
            ("Channel test/Anthropic", claude_channel_test),
            ("Channel test/Responses", responses_channel_test),
        ]
        for name, function in cases:
            self.check(name, function)

        deadline = time.time() + 10
        successful = []
        while time.time() < deadline:
            logs = self.api_json("GET", "/api/log/?p=1&page_size=100")
            successful = [
                item
                for item in logs["items"]
                if item["type"] == 2 and item["id"] not in before_log_ids
            ]
            if len(successful) >= len(cases):
                break
            time.sleep(0.2)
        assert len(successful) >= len(cases), f"expected at least {len(cases)} consume logs, got {len(successful)}"
        for item in successful[: len(cases)]:
            assert item["prompt_tokens"] > 0, item
            assert item["completion_tokens"] > 0, item

        report = {
            "base_url": self.base_url,
            "mock_url": self.mock_url,
            "total": len(self.results),
            "passed": len(self.results),
            "failed": 0,
            "billing_logs_checked": len(successful),
            "results": self.results,
        }
        print(json.dumps(report, ensure_ascii=False, indent=2))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base-url", default="http://127.0.0.1:13006")
    parser.add_argument("--mock-url", default="http://127.0.0.1:18086")
    args = parser.parse_args()
    E2E(args.base_url, args.mock_url).run()


if __name__ == "__main__":
    main()
