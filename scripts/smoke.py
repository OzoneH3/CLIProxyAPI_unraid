#!/usr/bin/env python3
"""Exercise the real image on isolated, disposable Docker storage. No OAuth account."""
import http.cookiejar
import json
import os
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.request

image = sys.argv[1] if len(sys.argv) > 1 else 'cliproxyapi-unraid:local'
name = 'cpa-smoke-' + secrets.token_hex(5)
volume = name + '-data'
password = secrets.token_urlsafe(24)
legacy = '--legacy' in sys.argv[2:]
legacy_key = 'sk-cpa-' + secrets.token_hex(32)
legacy_config = ("# retained migration comment\nhost: ''\nport: 8317\n"
    "auth-dir: /root/.cli-proxy-api\napi-keys: [" + legacy_key + "]\n"
    "remote-management: {allow-remote: false, secret-key: ''}\n"
    "logging-to-file: true\nplugins: {enabled: false, dir: /CLIProxyAPI/plugins}\n"
    "unknown-future-option: preserve-me\n")
env = dict(os.environ, WEBUI_PASSWORD=password)


def docker(*args):
    p = subprocess.run(['docker', *args], env=env, capture_output=True, text=True)
    if p.returncode:
        raise RuntimeError('Docker command failed: ' + args[0])
    return p.stdout.strip()


def port(number):
    return json.loads(docker('inspect', name))[0]['NetworkSettings']['Ports'][str(number) + '/tcp'][0]['HostPort']


def wait_ready(base):
    for _ in range(90):
        try:
            with urllib.request.urlopen(base + '/readyz', timeout=2) as r:
                if r.status == 200:
                    return
        except (urllib.error.URLError, OSError, TimeoutError):
            pass
        time.sleep(1)
    raise RuntimeError('Container readiness timed out; review local container logs privately')


try:
    docker('volume', 'create', volume)
    if legacy:
        seed = subprocess.run(['docker', 'run', '--rm', '-i', '--entrypoint', 'sh',
            '-v', volume + ':/data', image, '-c',
            'umask 077; mkdir -p /data/auths; printf retained > /data/auths/retained.txt; cat > /data/config.yaml'],
            input=legacy_config, text=True, capture_output=True)
        assert seed.returncode == 0, 'Could not seed migration fixture'
    docker('run', '-d', '--name', name, '-e', 'WEBUI_PASSWORD',
           '-p', '127.0.0.1::8317', '-p', '127.0.0.1::8318',
           '-v', volume + ':/data', image)
    base = 'http://127.0.0.1:' + port(8318)
    proxy = 'http://127.0.0.1:' + port(8317)
    wait_ready(base)
    # --password enables the upstream v8 ten-second local-management watchdog.
    # Stay idle longer than that to prove the wrapper's internal keep-alive works.
    time.sleep(12)
    wait_ready(base)
    jar = http.cookiejar.CookieJar()
    client = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

    def request(path, method='GET', body=None, csrf='', expected=200):
        req = urllib.request.Request(base + path, method=method,
              data=None if body is None else json.dumps(body).encode(),
              headers={'Content-Type': 'application/json', 'Origin': base, 'X-CSRF-Token': csrf})
        try:
            response = client.open(req, timeout=40)
        except urllib.error.HTTPError as error:
            response = error
        assert response.code == expected, (path, response.code, expected)
        return json.load(response)

    request('/api/key', expected=401)
    login = request('/api/login', 'POST', {'password': password})
    csrf = login['csrf']
    assert next(iter(jar)).has_nonstandard_attr('HttpOnly')
    first = request('/api/key')['key']
    assert first.startswith('sk-cpa-') and len(first) == 71
    if legacy:
        assert first == legacy_key, 'Migration changed the client key'
        assert docker('exec', name, 'cat', '/data/auths/retained.txt') == 'retained'
        migrated = docker('exec', name, 'cat', '/data/config.yaml')
        assert 'unknown-future-option: preserve-me' in migrated
        assert '# retained migration comment' in migrated
        assert '/root/.cli-proxy-api' not in migrated
        backup = docker('exec', name, 'cat', '/data/state/config.before-wrapper-migration.yaml')
        assert backup == legacy_config.strip()

    dashboard = request('/api/dashboard')
    assert dashboard['status'] == 'Running'
    assert request('/api/accounts') == []
    req = urllib.request.Request(proxy + '/v1/models', headers={'Authorization': 'Bearer ' + first})
    with urllib.request.urlopen(req) as response:
        assert isinstance(json.load(response)['data'], list)
    try:
        urllib.request.urlopen(proxy + '/v1/models')
        raise AssertionError('API allows missing credentials')
    except urllib.error.HTTPError as error:
        assert error.code == 401
    # Inspect only inside this process; no secret values are printed.
    local_secret = docker('exec', name, 'cat', '/data/state/local-management.key')
    req = urllib.request.Request(proxy + '/v8/management/config/access/api-keys', headers={'Authorization': 'Bearer ' + local_secret})
    try:
        urllib.request.urlopen(req)
        raise AssertionError('Remote management accepted local-only credential')
    except urllib.error.HTTPError as error:
        assert error.code in (401, 403)
    for path in ('/api/dashboard', '/api/accounts', '/api/logs', '/api/providers'):
        body = json.dumps(request(path))
        assert local_secret not in body and password not in body
    request('/api/key/rotate', 'POST', {'confirm': True}, expected=403)
    request('/api/key/rotate', 'POST', {'confirm': True}, csrf)
    second = request('/api/key')['key']
    assert second != first
    config = docker('exec', name, 'cat', '/data/config.yaml')
    assert second in config and first not in config and password not in config
    assert '/data/auths' in config and '/data/plugins' in config
    modes = docker('exec', name, 'stat', '-c', '%a', '/data/config.yaml', '/data/state/password.hash', '/data/state/local-management.key')
    assert modes.splitlines() == ['600', '600', '600']
    password_hash = docker('exec', name, 'cat', '/data/state/password.hash')
    docker('exec', name, '/usr/local/bin/unraid-wrapper', 'healthcheck')
    docker('restart', '-t', '15', name)
    base = 'http://127.0.0.1:' + port(8318)
    proxy = 'http://127.0.0.1:' + port(8317)
    wait_ready(base)
    request('/api/key', expected=401)  # restart invalidates all sessions
    csrf = request('/api/login', 'POST', {'password': password})['csrf']
    assert request('/api/key')['key'] == second
    assert docker('exec', name, 'cat', '/data/state/password.hash') == password_hash
    logs = docker('logs', name)
    assert all(s not in logs for s in (password, first, second, local_secret))
    # A child crash must cause PID 1/container failure; do not leak its cmdline.
    docker('exec', name, 'sh', '-c', 'kill -KILL $(pidof CLIProxyAPI)')
    exit_code = docker('wait', name)
    assert exit_code != '0'
    print(('PASS legacy migration: ' if legacy else 'PASS fresh install: ') + 'bootstrap, API, login, management isolation, CSRF, rotation, persistence, health, child-crash supervision; no live OAuth attempted.')
finally:
    subprocess.run(['docker', 'rm', '-f', name], capture_output=True)
    subprocess.run(['docker', 'volume', 'rm', volume], capture_output=True)
