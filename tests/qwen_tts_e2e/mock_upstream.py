import base64
import io
import json
import os
import pathlib
import struct
import threading
import time
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


PCM = bytes(range(256)) * 40
buffer = io.BytesIO()
with wave.open(buffer, 'wb') as audio:
    audio.setnchannels(1)
    audio.setsampwidth(2)
    audio.setframerate(24000)
    audio.writeframes(PCM)
WAV = buffer.getvalue()
stream_wave = bytearray(WAV)
struct.pack_into('<I', stream_wave, 4, 0x7fffffbf)
struct.pack_into('<I', stream_wave, 40, 0x7fffff99)
STREAM_WAV = bytes(stream_wave)
records = []
lock = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'

    def log_message(self, *_):
        pass

    def send(self, status, data, content_type='application/json'):
        if not isinstance(data, bytes):
            data = json.dumps(data, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == '/health':
            self.send(200, {'ok': True})
        elif self.path == '/records':
            with lock:
                snapshot = list(records)
            self.send(200, snapshot)
        elif self.path == '/audio.wav':
            with lock:
                records.append({'path': self.path, 'authorization': self.headers.get('Authorization', '')})
            self.send(200, WAV, 'audio/wav')
        elif self.path == '/not-wave':
            self.send(200, b'invalid audio', 'audio/wav')
        else:
            self.send(404, {'error': 'not found'})

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        with lock:
            records.append({'path': self.path, 'body': body, 'sse': self.headers.get('X-DashScope-SSE'),
                            'authorization': self.headers.get('Authorization')})
        if self.path.endswith('/chat/completions'):
            reply = {'id': 'mock-chat', 'object': 'chat.completion', 'model': body['model'],
                     'choices': [{'index': 0, 'message': {'role': 'assistant', 'content': 'normal chat',
                                                         'reasoning_content': 'normal reasoning'}, 'finish_reason': 'stop'}],
                     'usage': {'prompt_tokens': 100, 'completion_tokens': 20, 'total_tokens': 120}}
            if body.get('stream'):
                self.sse_start()
                self.event({'id': 'mock-chat', 'model': body['model'], 'choices': [{'index': 0, 'delta': reply['choices'][0]['message']}]})
                self.event({'id': 'mock-chat', 'model': body['model'], 'choices': [{'index': 0, 'delta': {}, 'finish_reason': 'stop'}], 'usage': reply['usage']})
                self.wfile.write(b'data: [DONE]\n\n')
                self.wfile.flush()
            else:
                self.send(200, reply)
            return
        if self.path == '/v1/audio/speech':
            self.send(200, WAV, 'audio/wav')
            return
        if self.path != '/api/v1/services/aigc/multimodal-generation/generation':
            self.send(404, {'error': 'wrong upstream URL'})
            return
        if body['model'] not in ('qwen3-tts-flash', 'qwen3-tts-flash-2025-11-27') or not isinstance(body.get('input'), dict):
            self.send(400, {'code': 'InvalidParameter', 'message': 'wrong request conversion'})
            return
        text = body['input']['text']
        if '[http-error]' in text:
            self.send(400, {'code': 'InvalidParameter', 'message': 'invalid voice'})
            return
        usage = {'input_tokens': 0, 'output_tokens': 0, 'characters': 37}
        if '[missing-usage]' in text:
            usage = {}
        if '[zero-usage]' in text:
            usage['characters'] = 0
        if '[negative-usage]' in text:
            usage['characters'] = -1
        url = 'http://mock-upstream:18080/audio.wav'
        if '[download-error]' in text:
            url = 'http://mock-upstream:18080/missing'
        if '[invalid-wave]' in text:
            url = 'http://mock-upstream:18080/not-wave'
        if '[private-url]' in text:
            url = 'http://127.0.0.1/audio.wav'
        output = {'status_code': 200, 'output': {'finish_reason': 'stop', 'audio': {'data': '', 'url': url}}, 'usage': usage}
        if '[body-error]' in text:
            output = {'code': 'InvalidParameter', 'message': 'invalid voice', 'status_code': 400}
        if '[empty-audio]' in text:
            output['output']['audio']['url'] = ''
        if self.headers.get('X-DashScope-SSE') != 'enable':
            if '[malformed]' in text:
                self.send(200, b'{invalid')
            else:
                self.send(200, output)
            return
        self.sse_start()
        if '[body-error]' in text or '[negative-usage]' in text:
            self.event(output)
            return
        if '[malformed]' in text:
            self.wfile.write(b'data: {invalid\n\n')
            self.wfile.flush()
            return
        if '[invalid-base64]' in text:
            self.event({'output': {'audio': {'data': '%%%'}}})
            return
        if '[recorded-wav]' in text:
            recorded = pathlib.Path(os.environ['QWEN_TTS_RECORDED_SSE']).read_text()
            events = [json.loads(line[5:]) for line in recorded.splitlines() if line.startswith('data:')]
            chunks = [base64.b64decode(event['audio']) for event in events if event.get('type') == 'audio.delta']
            recorded_usage = next(event['usage'] for event in events if event.get('type') == 'audio.done')
            output['usage'] = {'input_tokens': 0, 'output_tokens': 0, 'characters': recorded_usage['characters']}
        else:
            audio = PCM if '[raw-pcm]' in text else STREAM_WAV
            if '[wav-finite]' in text:
                audio = WAV
            if '[wav-metadata]' in text:
                audio = STREAM_WAV[:12] + b'JUNK\x03\x00\x00\x00abc\x00' + STREAM_WAV[12:]
            if '[extended-wav]' in text:
                audio = bytearray(STREAM_WAV[:36] + b'\x00\x00' + STREAM_WAV[36:])
                struct.pack_into('<I', audio, 16, 18)
                audio = bytes(audio)
            if '[bad-wav]' in text:
                audio = STREAM_WAV[:20] + b'\x03\x00' + STREAM_WAV[22:]
            if '[huge-wav-header]' in text:
                audio = STREAM_WAV[:16] + b'\xff\xff\xff\xff' + STREAM_WAV[20:]
            if '[truncated-wav-header]' in text:
                audio = STREAM_WAV[:30]
            if '[wav-header-only]' in text:
                audio = STREAM_WAV[:44]
            if '[split-wav]' in text:
                cuts = [0, 3, 9, 21, 43, 300, len(audio)]
                chunks = [audio[start:end] for start, end in zip(cuts, cuts[1:])]
            else:
                chunks = [audio[:len(audio) // 2], audio[len(audio) // 2:]]
        if '[empty-audio]' not in text:
            for chunk in chunks:
                self.event({'output': {'audio': {'data': base64.b64encode(chunk).decode()}}})
                if '[delayed]' in text:
                    time.sleep(0.3)
        if '[truncated]' not in text:
            self.event(output)

    def sse_start(self):
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.send_header('Connection', 'close')
        self.end_headers()
        self.close_connection = True

    def event(self, data):
        self.wfile.write(('data: ' + json.dumps(data, separators=(',', ':')) + '\n\n').encode())
        self.wfile.flush()


if __name__ == '__main__':
    ThreadingHTTPServer(('0.0.0.0', 18080), Handler).serve_forever()
