"""Response identity E2E. Runs in the disposable gateway network namespace."""

import json
import sys
import threading
from http.server import ThreadingHTTPServer
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "response_model_e2e"))
import run_e2e as base


class MockHandler(base.MockHandler):
    def send_response(self, code, message=None):
        super().send_response(code, message)
        self.send_header("X-Oneapi-Request-Id", "upstream-header-must-not-win")

    def send_json(self, status, payload):
        if payload.get("object") == "chat.completion":
            message = payload["choices"][0]["message"]
            message["reasoning_content"] = "reasoning kept"
            message["tool_calls"] = [{"id": "call_keep", "type": "function", "function": {"name": "lookup", "arguments": '{"id":"argument_keep"}'}}]
            payload["choices"][0]["finish_reason"] = "tool_calls"
        if payload.get("type") == "message":
            payload["content"].append({"type": "tool_use", "id": "tool_keep", "name": "lookup", "input": {"id": "argument_keep"}})
        super().send_json(status, payload)

    def send_sse(self, events, done=False):
        if done:
            first = events[0]
            events.insert(1, {**first, "choices": [{"index": 0, "delta": {"reasoning_content": "reasoning kept", "tool_calls": [{"index": 0, "id": "call_keep", "type": "function", "function": {"name": "lookup", "arguments": '{"id":"argument_keep"}'}}]}, "finish_reason": None}]})
        elif events and events[0].get("type") == "message_start":
            index = next(i for i, e in enumerate(events) if e["type"] == "message_delta")
            events[index:index] = [
                {"type": "content_block_start", "index": 2, "content_block": {"type": "tool_use", "id": "tool_keep", "name": "lookup", "input": {}}},
                {"type": "content_block_delta", "index": 2, "delta": {"type": "input_json_delta", "partial_json": '{"id":"argument_keep"}'}},
                {"type": "content_block_stop", "index": 2},
            ]
        super().send_sse(events, done)


class IdentityE2E(base.E2E):
    def request(self, method, path, payload=None, headers=None):
        status, response_headers, raw = super().request(method, path, payload, headers)
        if method == "POST" and path.startswith("/v1/") and status == 200:
            request_id = next((v for k, v in response_headers.items() if k.lower() == "x-oneapi-request-id"), "")
            assert request_id and request_id != "upstream-header-must-not-win"
            if payload.get("stream"):
                events = self.events(raw)
                if path.endswith("/chat/completions"):
                    assert events and all(e["id"] == request_id for e in events)
                    tools = [t for e in events for c in e.get("choices", []) for t in c.get("delta", {}).get("tool_calls", [])]
                    assert any(t.get("id") == "call_keep" for t in tools)
                    assert any(c.get("delta", {}).get("reasoning_content") == "reasoning kept" for e in events for c in e.get("choices", []))
                    response_id = events[0]["id"]
                elif path.endswith("/messages"):
                    start = next(e for e in events if e.get("type") == "message_start")
                    response_id = start["message"]["id"]
                    assert response_id == request_id
                    assert any(e.get("content_block", {}).get("id") == "tool_keep" for e in events)
                else:
                    ids = [e["response"]["id"] for e in events if "response" in e]
                    assert ids and all(i == "resp_restore_e2e" for i in ids)
                    assert all(e.get("item_id", "msg_restore_e2e") == "msg_restore_e2e" for e in events)
                    response_id = ids[0]
            else:
                result = json.loads(raw)
                response_id = result["id"]
                if path.endswith("/responses"):
                    assert response_id == "resp_restore_e2e"
                    assert result["output"][0]["id"] == "msg_restore_e2e"
                elif path.endswith("/chat/completions"):
                    assert response_id == request_id
                    message = result["choices"][0]["message"]
                    assert message["reasoning_content"] == "reasoning kept"
                    assert message["tool_calls"][0]["id"] == "call_keep"
                else:
                    assert response_id == request_id
                    assert any(b.get("id") == "tool_keep" for b in result["content"])
            self.last_identity = {"response_id": response_id, "response_id_matches_request_id": response_id == request_id, "responses_ids_preserved": path.endswith("/responses"), "tool_ids_preserved": True}
        return status, response_headers, raw

    def check(self, *args, **kwargs):
        super().check(*args, **kwargs)
        self.results[-1].update(self.last_identity)


def main():
    mock = ThreadingHTTPServer(("127.0.0.1", 0), MockHandler)
    mock.records = []
    mock.records_lock = threading.Lock()
    threading.Thread(target=mock.serve_forever, daemon=True).start()
    try:
        IdentityE2E("http://127.0.0.1:3000", mock, "/report/report.json").run()
    finally:
        mock.shutdown()
        mock.server_close()


if __name__ == "__main__":
    main()
