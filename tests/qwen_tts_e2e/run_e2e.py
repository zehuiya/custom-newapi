import argparse
import base64
import concurrent.futures
import hashlib
import http.cookiejar
import io
import json
import pathlib
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import wave
from decimal import Decimal, ROUND_HALF_UP


PASSWORD = 'QwenTts123!'
PCM = bytes(range(256)) * 40
buffer = io.BytesIO()
with wave.open(buffer, 'wb') as audio:
    audio.setnchannels(1)
    audio.setsampwidth(2)
    audio.setframerate(24000)
    audio.writeframes(PCM)
WAV = buffer.getvalue()
MODELS = ['qwen3-tts-flash', 'qwen3-tts-flash-2025-11-27', 'e2e-tts-alias', 'e2e-tts-price', 'e2e-tts-expr']


class E2E:
    def __init__(self, base_url, mock_url, output, recorded_sse=None):
        self.base_url = base_url.rstrip('/')
        self.mock_url = mock_url.rstrip('/')
        self.output = pathlib.Path(output)
        self.output.mkdir(parents=True, exist_ok=True)
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        self.token = ''
        self.results = []
        self.counter = 0
        self.recorded_sse = recorded_sse

    def request(self, method, path, body=None, headers=None, admin=False, timeout=30):
        headers = dict(headers or {})
        if body is not None:
            headers['Content-Type'] = 'application/json'
        if admin:
            headers['New-Api-User'] = '1'
        request = urllib.request.Request(self.base_url + path, method=method, headers=headers,
                                        data=None if body is None else json.dumps(body, ensure_ascii=False).encode())
        try:
            opener = self.opener if admin or path in ('/api/setup', '/api/user/login') else urllib.request.build_opener()
            with opener.open(request, timeout=timeout) as response:
                return response.status, dict(response.headers), response.read()
        except urllib.error.HTTPError as error:
            return error.code, dict(error.headers), error.read()

    def api(self, method, path, body=None):
        status, _, raw = self.request(method, path, body, admin=True)
        result = json.loads(raw)
        assert status == 200 and result.get('success'), (path, status, result)
        return result.get('data')

    def option(self, key, value):
        self.api('PUT', '/api/option/', {'key': key, 'value': json.dumps(value, separators=(',', ':'))})

    def setup(self):
        deadline = time.time() + 120
        while time.time() < deadline:
            try:
                status, _, raw = self.request('GET', '/api/status')
                if status == 200 and json.loads(raw)['success']:
                    break
            except Exception:
                time.sleep(1)
        else:
            raise AssertionError('NewAPI did not start')
        status, _, raw = self.request('GET', '/api/setup')
        if not json.loads(raw)['data']['status']:
            status, _, raw = self.request('POST', '/api/setup', {
                'username': 'root', 'password': PASSWORD, 'confirmPassword': PASSWORD,
                'SelfUseModeEnabled': True, 'DemoSiteEnabled': False})
            assert status == 200 and json.loads(raw)['success'], raw
        status, _, raw = self.request('POST', '/api/user/login', {'username': 'root', 'password': PASSWORD})
        assert status == 200 and json.loads(raw)['success'], raw
        page = self.api('GET', '/api/token/?p=1&size=100')
        token = next((item for item in page['items'] if item['name'] == 'qwen-tts-e2e'), None)
        if token is None:
            self.api('POST', '/api/token/', {'name': 'qwen-tts-e2e', 'expired_time': -1,
                                           'unlimited_quota': True, 'model_limits_enabled': False, 'group': 'default'})
            page = self.api('GET', '/api/token/?p=1&size=100')
            token = next(item for item in page['items'] if item['name'] == 'qwen-tts-e2e')
        self.token_id = token['id']
        self.token = 'sk-' + self.api('POST', f'/api/token/{self.token_id}/key')['key']
        self.option('ModelRatio', {**{model: 10 for model in MODELS}, 'qwen-plus': 1, 'tts-1': 7.5,
                                  'gpt-4o-mini': 1, 'gpt-4o': 1, 'claude-3-5-sonnet-20240620': 1,
                                  'claude-3-opus-20240229': 1})
        self.option('CompletionRatio', {'qwen-plus': 2, 'tts-1': 0})
        self.option('ModelPrice', {'e2e-tts-price': 0.02})
        self.option('GroupRatio', {'default': 0.65})
        self.option('ModelBillingMode', {'e2e-tts-expr': 'tiered_expr'})
        self.option('ModelTieredExpression', {'e2e-tts-expr': 'tier("characters", p * 20 + c * 0)'})
        self.option('fetch_setting.allow_private_ip', True)
        self.option('fetch_setting.allowed_ports', ['80', '443', '18080'])
        page = self.api('GET', '/api/channel/?p=1&page_size=100')
        names = {item['name'] for item in page['items']}
        for name, channel_type, models, mapping in [
            ('e2e-qwen-tts', 17, MODELS, {model: 'qwen3-tts-flash' for model in MODELS if model.startswith('e2e-')}),
            ('e2e-ali-chat', 17, ['qwen-plus'], {}),
            ('e2e-openai-tts', 1, ['tts-1'], {}),
        ]:
            if name not in names:
                channel = {'type': channel_type, 'key': 'mock-upstream-key', 'status': 1, 'name': name,
                           'weight': 10, 'priority': 100, 'base_url': 'http://mock-upstream:18080',
                           'models': ','.join(models), 'group': 'default', 'auto_ban': 0,
                           'model_mapping': json.dumps(mapping), 'setting': '{}', 'settings': '{}', 'other': ''}
                self.api('POST', '/api/channel/', {'mode': 'single', 'channel': channel})
        time.sleep(2)

    def invoke(self, body, path='/v1/audio/speech'):
        status, headers, raw = self.request('POST', path, body, {'Authorization': 'Bearer ' + self.token})
        rid = next((value for name, value in headers.items() if name.lower() == 'x-oneapi-request-id'), '')
        if not rid:
            rid = next((value for name, value in headers.items() if 'request-id' in name.lower()), '')
        self.counter += 1
        prefix = self.output / f'{self.counter:03d}'
        prefix.with_suffix('.request.json').write_text(json.dumps(body, ensure_ascii=False, indent=2), encoding='utf-8')
        prefix.with_suffix('.response.bin').write_bytes(raw)
        prefix.with_suffix('.meta.json').write_text(json.dumps({'status': status, 'headers': headers, 'request_id': rid}, indent=2), encoding='utf-8')
        return status, headers, raw, rid

    def payload(self, marker='', **kwargs):
        return {'model': 'qwen3-tts-flash', 'input': marker + 'hello world ' + uuid.uuid4().hex,
                'voice': 'Cherry', **kwargs}

    def records(self):
        with urllib.request.urlopen(self.mock_url + '/records', timeout=10) as response:
            return json.loads(response.read())

    def consume_log(self, rid):
        assert rid, 'response is missing Request ID'
        for _ in range(30):
            page = self.api('GET', '/api/log/?p=1&page_size=100&request_id=' + urllib.parse.quote(rid))
            log = next((item for item in page['items'] if item['type'] == 2), None)
            if log is not None:
                (self.output / (rid + '.billing.json')).write_text(json.dumps(log, ensure_ascii=False, indent=2), encoding='utf-8')
                return log
            time.sleep(0.1)
        raise AssertionError('Consume log was not saved: ' + rid)

    def assert_billing(self, rid, characters=37, quota=None):
        log = self.consume_log(rid)
        if quota is None:
            quota = int((Decimal(characters) * Decimal('10') * Decimal('0.65')).quantize(Decimal('1'), rounding=ROUND_HALF_UP))
        assert log['prompt_tokens'] == characters and log['completion_tokens'] == 0, log
        assert log['quota'] == quota, (quota, log)
        assert f'TTS usage: {characters} characters' in log['content'], log
        assert json.loads(log['other'])['group_ratio'] == 0.65, log
        return log

    @staticmethod
    def events(raw):
        return [json.loads(line[5:]) for line in raw.decode().splitlines()
                if line.startswith('data:') and line[5:].strip().startswith('{')]

    def audio_case(self, name, marker='', model='qwen3-tts-flash', stream=False, pcm=False, count=37, quota=None, expected_pcm=PCM, **kwargs):
        body = self.payload(marker, model=model, **kwargs)
        if stream:
            body.update(stream_format='sse', response_format='pcm')
        elif pcm:
            body['response_format'] = 'pcm'
        status, headers, raw, rid = self.invoke(body)
        assert status == 200, (status, raw[:700])
        if stream:
            assert headers['Content-Type'].startswith('text/event-stream'), headers
            events = self.events(raw)
            audio = b''.join(base64.b64decode(event['audio']) for event in events if event.get('type') == 'audio.delta')
            assert audio == expected_pcm, 'audio data changed or WAV header not removed'
            assert events[-1]['type'] == 'audio.done', events
            assert events[-1]['usage']['characters'] == (len(body['input']) if count is None else count), events[-1]
        else:
            assert raw == (expected_pcm if pcm else WAV), (len(raw), hashlib.sha256(raw).hexdigest())
            assert headers['Content-Type'].startswith('audio/pcm' if pcm else 'audio/wav'), headers
        record = next(item for item in reversed(self.records())
                      if isinstance(item.get('body', {}).get('input'), dict)
                      and item['body']['input']['text'] == body['input'])
        assert record['body']['model'] == ('qwen3-tts-flash' if model.startswith('e2e-') else model), record
        assert record['body']['input']['voice'] == body['voice'], record
        assert record['authorization'] == 'Bearer mock-upstream-key', record
        assert (record['sse'] == 'enable') == (stream or pcm), record
        if 'language_type' in body:
            assert record['body']['input']['language_type'] == body['language_type'], record
        log = self.assert_billing(rid, len(body['input']) if count is None else count, quota)
        assert log['is_stream'] == stream, log
        return {'request_id': rid, 'characters': log['prompt_tokens'], 'quota': log['quota'], 'response_bytes': len(raw)}

    def error_case(self, marker, stream=False, expected=502, **kwargs):
        body = self.payload(marker, **kwargs)
        if stream:
            body['stream_format'] = 'sse'
        status, _, raw, rid = self.invoke(body)
        if status == 200:
            assert stream and b'"type":"error"' in raw and b'audio.done' not in raw, raw[:700]
        else:
            assert status == expected and 'error' in json.loads(raw), (status, raw[:700])
        page = self.api('GET', '/api/log/?p=1&page_size=100&request_id=' + rid)
        assert not any(item['type'] == 2 for item in page['items']), page
        return {'request_id': rid, 'http_status': status, 'false_audio_done': False}

    def chat(self, stream):
        status, _, raw, rid = self.invoke({'model': 'qwen-plus', 'messages': [{'role': 'user', 'content': 'test'}],
                                          'stream': stream, 'stream_options': {'include_usage': True}}, '/v1/chat/completions')
        assert status == 200, (status, raw[:500])
        assert b'normal chat' in raw and b'normal reasoning' in raw, raw
        log = self.consume_log(rid)
        assert log['prompt_tokens'] == 100 and log['completion_tokens'] == 20 and log['quota'] == 91, log
        return {'request_id': rid, 'quota': log['quota']}

    def concurrency(self):
        before = self.api('GET', f'/api/token/{self.token_id}')['used_quota']
        bodies = [self.payload(f'[concurrent-{i}]' + ('[raw-pcm]' if i % 4 < 2 else '[split-wav]'),
                               response_format='pcm', stream_format='sse' if i % 2 else '') for i in range(20)]
        with concurrent.futures.ThreadPoolExecutor(max_workers=20) as pool:
            outcomes = list(pool.map(lambda body: self.request('POST', '/v1/audio/speech', body, {'Authorization': 'Bearer ' + self.token}), bodies))
        ids = []
        for i, (status, headers, raw) in enumerate(outcomes):
            assert status == 200, (status, raw[:400])
            if i % 2:
                assert b'audio.done' in raw
                assert b''.join(base64.b64decode(event['audio']) for event in self.events(raw) if event.get('type') == 'audio.delta') == PCM
            else:
                assert raw == PCM
            rid = next(value for name, value in headers.items() if 'request-id' in name.lower())
            self.assert_billing(rid)
            ids.append(rid)
            (self.output / f'concurrent-{i:02d}.response.bin').write_bytes(raw)
            (self.output / f'concurrent-{i:02d}.request.json').write_text(json.dumps(bodies[i], indent=2), encoding='utf-8')
        after = self.api('GET', f'/api/token/{self.token_id}')['used_quota']
        assert after - before == 20 * 241, (before, after)
        assert len(set(ids)) == 20
        return {'requests': 20, 'concurrency': 20, 'quota_delta': after - before, 'request_ids': ids}

    def progressive_stream(self):
        body = self.payload('[delayed]', stream_format='sse', response_format='pcm')
        request = urllib.request.Request(self.base_url + '/v1/audio/speech',
                                        data=json.dumps(body).encode(),
                                        headers={'Authorization': 'Bearer ' + self.token, 'Content-Type': 'application/json'})
        started = time.monotonic()
        with urllib.request.urlopen(request, timeout=10) as response:
            first = response.readline()
            first_seconds = time.monotonic() - started
            raw = first + response.read()
            total_seconds = time.monotonic() - started
            rid = next(value for name, value in response.headers.items() if 'request-id' in name.lower())
        assert b'audio.delta' in first and b'audio.done' in raw, raw[:700]
        assert first_seconds < total_seconds - 0.3, (first_seconds, total_seconds)
        self.assert_billing(rid)
        (self.output / 'progressive.response.sse').write_bytes(raw)
        return {'first_chunk_seconds': round(first_seconds, 3), 'total_seconds': round(total_seconds, 3), 'request_id': rid}

    def openai_tts_control(self):
        status, headers, raw, rid = self.invoke({'model': 'tts-1', 'input': 'OpenAI TTS control', 'voice': 'alloy', 'response_format': 'wav'})
        assert status == 200 and raw == WAV and headers['Content-Type'].startswith('audio/wav'), (status, raw[:500])
        log = self.consume_log(rid)
        assert log['prompt_tokens'] == len('OpenAI TTS control') and log['quota'] > 0, log
        assert 'TTS usage:' not in log['content'], log
        return {'request_id': rid, 'quota': log['quota'], 'prompt_tokens': log['prompt_tokens']}

    def run(self):
        self.setup()
        cases = []
        for stream in (False, True):
            cases.extend([
                (f'native {"SSE" if stream else "WAV"} and character billing', lambda s=stream: self.audio_case('', stream=s)),
                (f'snapshot stream={stream}', lambda s=stream: self.audio_case('', model=MODELS[1], stream=s)),
                (f'model mapping stream={stream}', lambda s=stream: self.audio_case('', model='e2e-tts-alias', stream=s)),
                (f'per-call billing stream={stream}', lambda s=stream: self.audio_case('', model='e2e-tts-price', stream=s, quota=6500)),
                (f'expression character billing stream={stream}', lambda s=stream: self.audio_case('', model='e2e-tts-expr', stream=s)),
                (f'missing usage Unicode fallback stream={stream}', lambda s=stream: self.audio_case('', '[missing-usage]世界', stream=s, count=None)),
                (f'explicit zero characters stream={stream}', lambda s=stream: self.audio_case('', '[zero-usage]', stream=s, count=0, quota=0)),
            ])
            for marker in ('[body-error]', '[malformed]', '[negative-usage]', '[empty-audio]'):
                cases.append((f'{marker} stream={stream}', lambda m=marker, s=stream: self.error_case(m, stream=s)))
            for marker in ('[raw-pcm]', '[wav-finite]', '[split-wav]', '[wav-metadata]', '[extended-wav]'):
                cases.append((f'PCM normalization {marker} stream={stream}',
                              lambda m=marker, s=stream: self.audio_case('', m, stream=s, pcm=True)))
            for marker in ('[bad-wav]', '[huge-wav-header]', '[truncated-wav-header]', '[wav-header-only]'):
                cases.append((f'invalid WAV {marker} stream={stream}',
                              lambda m=marker, s=stream: self.error_case(m, stream=s, response_format='pcm')))
        if self.recorded_sse:
            source_events = self.events(pathlib.Path(self.recorded_sse).read_bytes())
            source_wave = b''.join(base64.b64decode(event['audio']) for event in source_events if event.get('type') == 'audio.delta')
            with wave.open(io.BytesIO(source_wave), 'rb') as reader:
                source_pcm = reader.readframes(reader.getnframes())
            assert source_wave[:4] == b'RIFF' and len(source_pcm) > 0
            source_characters = next(event['usage']['characters'] for event in source_events if event.get('type') == 'audio.done')
            for stream in (False, True):
                cases.append((f'recorded real channel WAV stream={stream}',
                              lambda s=stream: self.audio_case('', '[recorded-wav]', stream=s, pcm=True,
                                                             expected_pcm=source_pcm, count=source_characters)))
        cases.extend([
            ('non-stream PCM aggregation', lambda: self.audio_case('', pcm=True)),
            ('language and speed=1', lambda: self.audio_case('', language_type='Chinese', speed=1)),
            ('upstream HTTP error', lambda: self.error_case('[http-error]', expected=400)),
            ('download 404', lambda: self.error_case('[download-error]')),
            ('invalid WAV download', lambda: self.error_case('[invalid-wave]')),
            ('invalid base64', lambda: self.error_case('[invalid-base64]', stream=True)),
            ('truncated stream is not successful', lambda: self.error_case('[truncated]', stream=True)),
            ('truncated buffered PCM is rejected', lambda: self.error_case('[truncated]', response_format='pcm')),
            ('unsupported MP3', lambda: self.error_case('', response_format='mp3', expected=400)),
            ('unsupported speed including explicit zero', lambda: self.error_case('', speed=0, expected=400)),
            ('missing voice', lambda: self.error_case('', voice='', expected=400)),
            ('unsupported instruction', lambda: self.error_case('', instructions='fast', expected=400)),
            ('SSE cannot claim WAV format', lambda: self.error_case('', stream=True, response_format='wav', expected=400)),
            ('Ali existing chat non-stream', lambda: self.chat(False)),
            ('Ali existing chat stream', lambda: self.chat(True)),
            ('OpenAI existing TTS unchanged', self.openai_tts_control),
            ('SSE audio arrives before upstream completion', self.progressive_stream),
            ('20 concurrent audio requests and exact quota delta', self.concurrency),
        ])
        for name, function in cases:
            started = time.time()
            try:
                evidence = function()
                self.results.append({'case': name, 'status': 'PASS', 'seconds': round(time.time() - started, 3), 'evidence': evidence})
                print('PASS ' + name, flush=True)
            except Exception as error:
                self.results.append({'case': name, 'status': 'FAIL', 'seconds': round(time.time() - started, 3), 'error': str(error)[:1500]})
                print('FAIL ' + name + ': ' + str(error)[:1500], flush=True)
        # Reject private URL using production defaults, then restore the mock-only allowance.
        self.option('fetch_setting.allow_private_ip', False)
        try:
            evidence = self.error_case('[private-url]')
            self.results.append({'case': 'SSRF private audio URL rejected', 'status': 'PASS', 'evidence': evidence})
        except Exception as error:
            self.results.append({'case': 'SSRF private audio URL rejected', 'status': 'FAIL', 'error': str(error)[:1500]})
        finally:
            self.option('fetch_setting.allow_private_ip', True)
        assert all(not item.get('authorization') for item in self.records() if item['path'] == '/audio.wav'), 'API key leaked on audio download'
        self.results.append({'case': 'audio download does not forward channel API key', 'status': 'PASS'})
        passed = sum(item['status'] == 'PASS' for item in self.results)
        report = {'base_url': self.base_url, 'mock_url': self.mock_url, 'total': len(self.results),
                  'passed': passed, 'failed': len(self.results) - passed, 'results': self.results}
        (self.output / 'report.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
        print(json.dumps({'total': report['total'], 'passed': report['passed'], 'failed': report['failed']}))
        return report['failed']


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--base-url', default='http://127.0.0.1:13009')
    parser.add_argument('--mock-url', default='http://127.0.0.1:18089')
    parser.add_argument('--output', default='.test/qwen-tts-e2e/results')
    parser.add_argument('--recorded-sse', help='Optional saved SSE from the real channel; mount it in the mock and set QWEN_TTS_RECORDED_SSE')
    args = parser.parse_args()
    raise SystemExit(bool(E2E(args.base_url, args.mock_url, args.output, args.recorded_sse).run()))
