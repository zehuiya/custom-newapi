import json
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


RECORDS = []
RECORDS_LOCK = threading.Lock()


def write_sse(handler, events):
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream; charset=utf-8")
    handler.send_header("Cache-Control", "no-cache")
    handler.end_headers()
    for event in events:
        handler.wfile.write(("data: " + json.dumps(event, separators=(",", ":")) + "\n\n").encode())
        handler.wfile.flush()
    handler.wfile.write(b"data: [DONE]\n\n")
    handler.wfile.flush()


def openai_response(model):
    return {
        "id": "chatcmpl-force-e2e",
        "object": "chat.completion",
        "created": 1786500000,
        "model": model,
        "choices": [
            {
                "index": 0,
                "message": {
                    "role": "assistant",
                    "reasoning_content": "mock reasoning",
                    "content": "mock answer",
                    "tool_calls": [
                        {
                            "id": "call_force_e2e",
                            "type": "function",
                            "function": {"name": "lookup", "arguments": "{\"q\":\"force\"}"},
                        }
                    ],
                },
                "finish_reason": "tool_calls",
            }
        ],
        "usage": {
            "prompt_tokens": 11,
            "completion_tokens": 9,
            "total_tokens": 20,
            "completion_tokens_details": {"reasoning_tokens": 4},
        },
    }


def openai_stream(model):
    base = {
        "id": "chatcmpl-force-e2e",
        "object": "chat.completion.chunk",
        "created": 1786500000,
        "model": model,
    }
    return [
        {**base, "choices": [{"index": 0, "delta": {"role": "assistant", "reasoning_content": "mock "}, "finish_reason": None}]},
        {**base, "choices": [{"index": 0, "delta": {"reasoning": "reasoning", "content": "mock ", "tool_calls": [{"index": 0, "id": "call_force_e2e", "type": "function", "function": {"name": "lookup", "arguments": "{\"q\":"}}]}, "finish_reason": None}]},
        {**base, "choices": [{"index": 0, "delta": {"content": "answer", "tool_calls": [{"index": 0, "function": {"arguments": "\"force\"}"}}]}, "finish_reason": "tool_calls"}]},
        {**base, "choices": [], "usage": {"prompt_tokens": 11, "completion_tokens": 9, "total_tokens": 20, "completion_tokens_details": {"reasoning_tokens": 4}}},
    ]


def claude_response(model):
    return {
        "id": "msg_force_e2e",
        "type": "message",
        "role": "assistant",
        "model": model,
        "content": [
            {"type": "thinking", "thinking": "mock reasoning", "signature": "sig-force-e2e"},
            {"type": "text", "text": "mock answer"},
            {"type": "tool_use", "id": "tool_force_e2e", "name": "lookup", "input": {"q": "force"}},
        ],
        "stop_reason": "tool_use",
        "usage": {"input_tokens": 13, "output_tokens": 10, "cache_read_input_tokens": 3},
    }


def claude_stream(model):
    return [
        {"type": "message_start", "message": {"id": "msg_force_e2e", "type": "message", "role": "assistant", "model": model, "content": [], "usage": {"input_tokens": 13, "output_tokens": 1, "cache_read_input_tokens": 3}}},
        {"type": "content_block_start", "index": 0, "content_block": {"type": "thinking", "thinking": ""}},
        {"type": "content_block_delta", "index": 0, "delta": {"type": "thinking_delta", "thinking": "mock reasoning"}},
        {"type": "content_block_delta", "index": 0, "delta": {"type": "signature_delta", "signature": "sig-force-e2e"}},
        {"type": "content_block_stop", "index": 0},
        {"type": "content_block_start", "index": 1, "content_block": {"type": "text", "text": ""}},
        {"type": "content_block_delta", "index": 1, "delta": {"type": "text_delta", "text": "mock answer"}},
        {"type": "content_block_stop", "index": 1},
        {"type": "content_block_start", "index": 2, "content_block": {"type": "tool_use", "id": "tool_force_e2e", "name": "lookup", "input": {}}},
        {"type": "content_block_delta", "index": 2, "delta": {"type": "input_json_delta", "partial_json": "{\"q\":\"force\"}"}},
        {"type": "content_block_stop", "index": 2},
        {"type": "message_delta", "delta": {"stop_reason": "tool_use"}, "usage": {"output_tokens": 10}},
        {"type": "message_stop"},
    ]


def responses_response(model):
    return {
        "id": "resp_force_e2e",
        "object": "response",
        "created_at": 1786500000,
        "status": "completed",
        "model": model,
        "output": [
            {"type": "reasoning", "id": "rs_force_e2e", "status": "completed", "summary": [{"type": "summary_text", "text": "mock reasoning"}]},
            {"type": "message", "id": "msg_force_e2e", "status": "completed", "role": "assistant", "content": [{"type": "output_text", "text": "mock answer", "annotations": []}]},
            {"type": "function_call", "id": "fc_force_e2e", "status": "completed", "call_id": "call_force_e2e", "name": "lookup", "arguments": "{\"q\":\"force\"}"},
        ],
        "parallel_tool_calls": True,
        "tools": [],
        "usage": {"input_tokens": 17, "output_tokens": 12, "total_tokens": 29, "output_tokens_details": {"reasoning_tokens": 5}},
        "provider_extension": {"preserved": True},
    }


def responses_stream(model):
    response = responses_response(model)
    return [
        {"type": "response.created", "response": {"id": response["id"], "object": "response", "created_at": response["created_at"], "status": "in_progress", "model": model, "output": []}},
        {"type": "response.reasoning_summary_text.delta", "item_id": "rs_force_e2e", "output_index": 0, "summary_index": 0, "delta": "mock reasoning"},
        {"type": "response.output_text.delta", "item_id": "msg_force_e2e", "output_index": 1, "content_index": 0, "delta": "mock answer"},
        {"type": "response.function_call_arguments.delta", "item_id": "fc_force_e2e", "output_index": 2, "delta": "{\"q\":\"force\"}"},
        {"type": "response.completed", "response": response},
    ]


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *_args):
        return

    def send_json(self, status, payload):
        body = json.dumps(payload, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/health":
            self.send_json(200, {"ok": True})
            return
        if self.path == "/records":
            with RECORDS_LOCK:
                records = list(RECORDS)
            self.send_json(200, {"records": records})
            return
        self.send_json(404, {"error": "not found"})

    def do_DELETE(self):
        if self.path == "/records":
            with RECORDS_LOCK:
                RECORDS.clear()
            self.send_json(200, {"ok": True})
            return
        self.send_json(404, {"error": "not found"})

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        try:
            payload = json.loads(raw or b"{}")
        except json.JSONDecodeError:
            self.send_json(400, {"error": {"message": "invalid json", "type": "invalid_request_error"}})
            return

        model = payload.get("model", "mock-model")
        stream = payload.get("stream") is True
        with RECORDS_LOCK:
            RECORDS.append({
                "at": time.time(),
                "path": self.path,
                "model": model,
                "stream": stream,
                "accept": self.headers.get("Accept", ""),
                "body": payload,
            })

        if self.path.endswith("/chat/completions"):
            if stream:
                write_sse(self, openai_stream(model))
            else:
                self.send_json(200, openai_response(model))
            return
        if self.path.endswith("/messages"):
            if stream:
                write_sse(self, claude_stream(model))
            else:
                self.send_json(200, claude_response(model))
            return
        if self.path.endswith("/responses"):
            if stream:
                write_sse(self, responses_stream(model))
            else:
                self.send_json(200, responses_response(model))
            return
        self.send_json(404, {"error": {"message": "unsupported path", "type": "not_found"}})


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 18080), Handler).serve_forever()
