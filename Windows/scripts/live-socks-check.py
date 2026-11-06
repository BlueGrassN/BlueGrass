"""Opt-in live test. Secrets stay in memory; stdout contains only allowlisted facts.

Never changes OS proxy, routes or the service used by --api-proxy.
"""
import argparse
import hashlib
import http.client
import ipaddress
import json
import socket
import subprocess
import queue
import threading
import time
import ssl
import struct
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path

PROBE = 'https://api.ipify.org?format=json'


class NoApiRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Never forward login bodies or bearer tokens to a redirect target.
        return None


def native_check(executable, node, key):
    process = subprocess.Popen([str(Path(executable).resolve())], stdin=subprocess.PIPE,
        stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding='utf-8',
        creationflags=subprocess.CREATE_NO_WINDOW)
    responses = queue.Queue()
    def read():
        try:
            for line in process.stdout:
                responses.put(json.loads(line))
        finally:
            responses.put(None)
    threading.Thread(target=read, daemon=True).start()
    def command(name, **kwargs):
        request_id = str(uuid.uuid4())
        process.stdin.write(json.dumps({'id': request_id, 'command': name, **kwargs}) + '\n')
        process.stdin.flush()
        response = responses.get(timeout=30)
        if response is None or response.get('id') != request_id or response.get('ok') is not True:
            raise RuntimeError('native command failed')
        return response['snapshot']
    def await_terminal():
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            state = command('GetStatus')
            if state['connection'] in ('connected', 'failed', 'paused'):
                return state
            time.sleep(.2)
        raise TimeoutError('native connection deadline')
    def local_request(address):
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({'https': 'http://' + address}))
        with opener.open(PROBE, timeout=15) as response:
            return json.loads(response.read(4096))['ip']
    try:
        hello = command('Hello')
        address = hello['proxyAddress']
        if address.endswith(':10808') or hello['systemProxyApplied'] is not False:
            raise RuntimeError('unsafe listener or OS proxy claim')
        profile = {'key': key,
            'protocol': node['protocol'], 'entry': {'host': node['host'], 'port': node['port']},
            'auth': {'username': node['username'], 'password': node['password']},
            'validUntilUtc': node['expires_at'], 'configVersion': 1,
            'configFingerprint': hashlib.sha256(json.dumps(node, sort_keys=True).encode()).hexdigest(),
            'mode': 'system_proxy'}
        command('Connect', profile=profile)
        good = await_terminal()
        if good['connection'] != 'connected' or not good['businessGateOpen']:
            raise RuntimeError('native connection failed')
        observed = local_request(address)
        if not ipaddress.IPv4Address(observed).is_global:
            raise RuntimeError('invalid observed public IPv4')
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            latest = command('GetStatus')
            if latest.get('verifiedExitIpv4'):
                break
            time.sleep(.2)
        if not ipaddress.IPv4Address(latest.get('verifiedExitIpv4', '')).is_global:
            raise RuntimeError('native exit observation unavailable')
        metrics = latest.get('traffic', {})
        print(json.dumps({'stage': 'native_local_http_proxy', 'subscriptionId': node['id'],
            'observed': observed, 'reportedExit': latest['verifiedExitIpv4'],
            'expectedIpRequired': False,
            'systemProxyApplied': good['systemProxyApplied'],
            'trafficAvailable': metrics.get('available') is True,
            'downloadBytesPositive': int(metrics.get('downloadBytes', '0')) > 0}), flush=True)
        command('Pause')
        try:
            local_request(address)
        except Exception:
            print(json.dumps({'stage': 'native_pause', 'businessRequestBlocked': True}), flush=True)
        else:
            raise RuntimeError('paused proxy forwarded business')
        command('Disconnect')
        process.stdin.close()
        if process.wait(timeout=10) != 0:
            raise RuntimeError('native process did not exit cleanly')
        print(json.dumps({'stage': 'native_shutdown', 'cleanExit': True}), flush=True)
    finally:
        if process.poll() is None:
            process.stdin.close()
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)


def exact(sock, length):
    value = b''
    while len(value) < length:
        piece = sock.recv(length - len(value))
        if not piece:
            raise ConnectionError('short SOCKS response')
        value += piece
    return value


def socks_connection(node, host, port):
    sock = socket.create_connection((node['host'], node['port']), timeout=12)
    try:
        username, password = node['username'].encode(), node['password'].encode()
        if len(username) > 255 or len(password) > 255:
            raise ValueError('unsupported credential length')
        sock.sendall(b'\x05\x02\x00\x02')
        version, method = exact(sock, 2)
        if version != 5 or method not in (0, 2):
            raise ConnectionError('SOCKS authentication negotiation failed')
        if method == 2:
            sock.sendall(b'\x01' + bytes([len(username)]) + username + bytes([len(password)]) + password)
            if exact(sock, 2) != b'\x01\x00':
                raise ConnectionError('SOCKS authentication failed')
        address = host.encode('idna')
        sock.sendall(b'\x05\x01\x00\x03' + bytes([len(address)]) + address + struct.pack('!H', port))
        version, status, _, family = exact(sock, 4)
        if version != 5 or status != 0:
            raise ConnectionError('SOCKS connect failed')
        length = 4 if family == 1 else 16 if family == 4 else exact(sock, 1)[0] if family == 3 else 0
        if not length:
            raise ConnectionError('invalid SOCKS address')
        exact(sock, length + 2)
        return sock
    except BaseException:
        sock.close()
        raise


def probe(node, endpoint):
    url = urllib.parse.urlsplit(endpoint)
    if url.scheme != 'https' or url.username or url.password:
        raise ValueError('HTTPS probe required')
    raw = socks_connection(node, url.hostname, url.port or 443)
    with ssl.create_default_context().wrap_socket(raw, server_hostname=url.hostname) as conn:
        path = url.path or '/'
        if url.query:
            path += '?' + url.query
        conn.sendall(('GET ' + path + ' HTTP/1.1\r\nHost: ' + url.hostname +
                      '\r\nConnection: close\r\nAccept: application/json\r\n\r\n').encode())
        response = http.client.HTTPResponse(conn)
        response.begin()
        if response.status != 200:
            raise ConnectionError('probe HTTP failure')
        data = response.read(4097)
        if len(data) > 4096:
            raise ValueError('probe response too large')
        value = json.loads(data)['ip']
        ip = ipaddress.ip_address(value)
        if ip.version != 4 or not ip.is_global:
            raise ValueError('invalid IPv4 result')
        return str(ip)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--account-file', required=True)
    parser.add_argument('--probe-all', action='store_true', help='Diagnose every returned SOCKS node and report its actual exit.')
    parser.add_argument('--api', default='https://client-api.sunip.vip/api/client/v1')
    parser.add_argument('--api-proxy')
    parser.add_argument('--native-executable')
    args = parser.parse_args()
    api_url = urllib.parse.urlsplit(args.api)
    if api_url.scheme != 'https' or api_url.username or api_url.password:
        raise ValueError('HTTPS API required')
    lines = [line for line in Path(args.account_file).read_text(encoding='utf-8-sig').splitlines() if line.strip()]
    if len(lines) != 2:
        raise ValueError('account file must have two nonempty lines')
    opener = urllib.request.build_opener(urllib.request.ProxyHandler(
        {'https': args.api_proxy} if args.api_proxy else {}), NoApiRedirect())
    token = None

    def request(path, body=None):
        headers = {'Accept': 'application/json', 'Content-Type': 'application/json'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        req = urllib.request.Request(args.api.rstrip('/') + path,
            data=None if body is None else json.dumps(body).encode(), headers=headers)
        with opener.open(req, timeout=20) as response:
            payload = response.read(2 * 1024 * 1024 + 1)
        if len(payload) > 2 * 1024 * 1024:
            raise ValueError('API response too large')
        result = json.loads(payload)
        if result.get('success') is not True:
            raise ValueError('API rejected request')
        return result['data']

    try:
        login = request('/auth/login', {'username': lines[0].strip(), 'password': lines[1],
            'device_id': str(uuid.uuid4()), 'device_name': 'BlueGrass isolated live check'})
        token = login['access_token']
        account_key = str(login['customer']['id'])
        if 'login_ip' in login or 'client_ip' in login:
            raise ValueError('unexpected login IP response fields')
        print(json.dumps({'stage': 'login', 'success': True}), flush=True)
        capabilities = request('/capabilities')
        print(json.dumps({'stage': 'capabilities', **{
            key: capabilities.get(key) is True for key in (
                'resource_queries', 'expiry_summaries', 'all_owned_resources',
                'authoritative_expected_exit', 'sms_login',
            )
        }}), flush=True)
        print(json.dumps({'stage': 'capability_contract',
            'fieldTypes': {key: type(capabilities.get(key)).__name__ for key in (
                'connectable_nodes', 'resource_queries', 'all_owned_resources',
                'expiry_summaries', 'sms_login', 'authoritative_expected_exit')},
            'conservativeOwnership': capabilities.get('ownership_coverage') == 'conservative',
            'subscriptionAndAssetKinds': capabilities.get('resource_kinds') == ['subscription', 'asset']}), flush=True)
        if capabilities.get('resource_queries') is True:
            for kind in ('subscription', 'asset'):
                query = {'kind': kind, 'per_page': 100}
                count = 0
                seen = set()
                for _ in range(100):
                    page = request('/resources?' + urllib.parse.urlencode(query))
                    count += len(page['items'])
                    if not page['pagination']['has_more']:
                        break
                    cursor = page['pagination']['next_cursor']
                    if not cursor or cursor in seen:
                        raise ValueError('invalid resource cursor')
                    seen.add(cursor)
                    query['cursor'] = cursor
                else:
                    raise ValueError('resource pagination limit exceeded')
                print(json.dumps({'stage': 'resources', 'kind': kind,
                    'items': count, 'paginationComplete': True,
                    'snapshotComplete': page['coverage']['snapshot_complete']}), flush=True)
        cursor = None
        candidates = []
        seen = set()
        for _ in range(100):
            query = {'per_page': 100}
            if cursor:
                query['cursor'] = cursor
            page = request('/nodes?' + urllib.parse.urlencode(query))
            candidates.extend(page['items'])
            if not page['pagination']['has_more']:
                break
            cursor = page['pagination']['next_cursor']
            if not cursor or cursor in seen:
                raise ValueError('invalid node cursor')
            seen.add(cursor)
        else:
            raise ValueError('node pagination limit exceeded')
        print(json.dumps({'stage': 'node_contract', 'items': len(candidates),
            'paginationComplete': True,
            'withExpectedExit': sum(bool(item.get('expected_exit_ip')) for item in candidates)}), flush=True)
        failures = 0
        for item in candidates:
            node = request('/nodes/' + urllib.parse.quote(str(item['id']), safe=''))
            print(json.dumps({'stage': 'node_detail', 'subscriptionId': node['id'],
                'protocol': node['protocol'], 'countryCode': node['country_code'],
                'expiresAt': node['expires_at'],
                'hasExpectedExit': bool(node.get('expected_exit_ip'))}), flush=True)
            if args.native_executable:
                native_check(args.native_executable, node, {
                    'environment': args.api.rstrip('/'), 'dataSource': 'api_v2',
                    'accountKey': account_key, 'sourceId': node['id'], 'kind': 'subscription',
                })
            if not args.probe_all:
                continue
            try:
                observed = probe(node, PROBE)
                print(json.dumps({'stage': 'socks_https_probe', 'subscriptionId': node['id'],
                    'success': True, 'observed': observed, 'diagnosticOnly': True}), flush=True)
            except Exception as error:
                failures += 1
                print(json.dumps({'stage': 'socks_https_probe', 'subscriptionId': node['id'],
                    'success': False, 'errorType': type(error).__name__}), flush=True)
        print(json.dumps({'stage': 'resource_list', 'items': len(candidates),
            'filteredByExit': False, 'probeFailures': failures,
            'nativeConnections': len(candidates) if args.native_executable else 0}), flush=True)
        return 0 if failures == 0 else 1
    finally:
        if token:
            try:
                request('/auth/logout', {})
                print(json.dumps({'stage': 'logout', 'success': True}), flush=True)
            except Exception:
                print(json.dumps({'stage': 'logout', 'success': False}), flush=True)


if __name__ == '__main__':
    try:
        sys.exit(main())
    except urllib.error.HTTPError as error:
        code = None
        try:
            value = json.loads(error.read(4096)).get('error', {}).get('code')
            if value in {'invalid_credentials', 'account_disabled', 'rate_limited',
                         'validation_failed', 'service_busy', 'service_unavailable',
                         'https_required', 'not_found', 'capability_unavailable'}:
                code = value
        except (ValueError, AttributeError):
            pass
        print(json.dumps({'success': False, 'errorType': 'HTTPError',
                          'httpStatus': error.code, 'errorCode': code}), flush=True)
        sys.exit(1)
    except Exception as error:
        print(json.dumps({'success': False, 'errorType': type(error).__name__}), flush=True)
        sys.exit(1)
