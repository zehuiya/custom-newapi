"""Exercise upstream errors over real HTTP with a disposable Compose gateway."""

import json
import sys
import threading
import time
from http.server import ThreadingHTTPServer
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "response_model_e2e"))
import run_e2e as base


class MockHandler(base.MockHandler):
    def do_POST(self):
        if self.server.error is None:
            return super().do_POST()
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        status, message = self.server.error
        error = {"message": message, "type": "upstream_error", "code": "mock_error", "param": "input"}
        self.send_json(status, {"type": "error", "error": error})


class ErrorE2E(base.E2E):
    def configure_mapping(self, label, mapping):
        channel = dict(self.channels[label])
        channel.pop("key", None)
        channel["status_code_mapping"] = json.dumps(mapping)
        self.api("PUT", "/api/channel/", channel)
        saved = self.api("GET", f"/api/channel/{channel['id']}")
        assert json.loads(saved["status_code_mapping"]) == mapping
        self.channels[label] = saved

    def check_error(self, label, upstream, original, status, expected, stream=False):
        _, path, model = base.SPECS[label]
        body = {"model": model, "stream": stream}
        if label == "responses":
            body["input"] = "hello"
        else:
            body.update(messages=[{"role": "user", "content": "hello"}], max_tokens=64)
        self.mock.error = (upstream, original)
        try:
            code, headers, raw = self.request("POST", path, body, {
                "Authorization": "Bearer " + self.token, "anthropic-version": "2023-06-01",
            })
        finally:
            self.mock.error = None
        assert code == status, (label, upstream, status, code, raw)
        rid = next(value for key, value in headers.items() if key.lower() == "x-oneapi-request-id")
        error = json.loads(raw)["error"]
        message = error["message"]
        if label == "claude":
            message = message.removesuffix(f" (request id: {rid})") if upstream in (429, 502) else message
        else:
            assert error["code"] == "mock_error" and error["param"] == "input", error
        assert message == expected, (label, repr(expected), repr(message), raw)
        self.results.append({"protocol": label, "stream_requested": stream, "upstream_status": upstream,
                             "http_status": code, "original": original, "message": message, "request_id": rid})

    def check_payloads(self):
        errors = {r["request_id"]: r for r in self.results if "original" in r}
        pending = set(errors)
        for _ in range(60):
            for path in (self.report.parent / "payload").glob("*.jsonl"):
                with path.open(encoding="utf-8") as f:
                    for line in f:
                        try:
                            row = json.loads(line)
                        except json.JSONDecodeError:
                            continue
                        rid = row.get("request_id")
                        if rid not in pending:
                            continue
                        check = errors[rid]
                        assert row["upstream_status_code"] == check["upstream_status"], row
                        assert row["response"]["error"]["message"] == check["original"], row
                        check["original_payload_preserved"] = True
                        pending.remove(rid)
            if not pending:
                return
            time.sleep(0.1)
        raise AssertionError(f"Missing upstream payloads: {pending}")

    def run(self):
        self.prepare()
        for label in base.SPECS:
            self.configure_mapping(label, {})
            for stream in (False, True):
                self.check_error(label, 400, "limit 1M token, request_id: upstream-400", 400, "limit 1M token, ", stream)
            self.check_error(label, 403, "\u9650\u989d; ReQuEsT ID: abc", 400, "\u9650\u989d; ")
            self.check_error(label, 422, "invalid temperature", 400, "invalid temperature")
            self.check_error(label, 400, "REQUEST abc", 400, "")
            rate = "capacity full, Request ID: upstream-429"
            failure = "upstream error, Request ID: upstream-502"
            self.check_error(label, 429, rate, 429, rate)
            self.check_error(label, 502, failure, 502, failure)
            self.configure_mapping(label, {"400": 429, "413": 429, "429": 503, "502": 503})
            self.check_error(label, 401, "invalid field, REQUEST_ID=abc", 429, "invalid field, ")
            self.check_error(label, 429, rate, 503, rate)
            self.check_error(label, 400, "Entry invalid", 429, "Entry invalid")
            oversized = "Entry too big, request_id: mock-size-error"
            for stream in (False, True):
                self.check_error(label, 400, oversized, 400, "Entry too big, ", stream)
            self.check_error(label, 413, "Payload TOO BIG", 413, "Payload TOO BIG")
            for status in (429, 502):
                self.check_error(label, status, "Entry too big", status, "Entry too big")
            self.check(label, enabled=False)
            self.check(label, enabled=False, stream=True)
        self.check_payloads()
        self.report.write_text(json.dumps({"cases_passed": len(self.results), "results": self.results},
                                         ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"Passed all {len(self.results)} relay error and success cases", flush=True)


def main():
    with ThreadingHTTPServer(("127.0.0.1", 0), MockHandler) as mock:
        mock.records, mock.records_lock, mock.error = [], threading.Lock(), None
        threading.Thread(target=mock.serve_forever, daemon=True).start()
        try:
            ErrorE2E("http://127.0.0.1:3000", mock, "/report/e2e-report.json").run()
        finally:
            mock.shutdown()


if __name__ == "__main__":
    main()
