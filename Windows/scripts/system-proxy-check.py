"""Authorized Windows integration. Uses the existing secret-safe account loader.

Only child processes started here are controlled. WinINET state is kept in
memory; stdout contains allowlisted outcomes. No credentials enter argv/logs.
"""
import ctypes as c
from ctypes import wintypes as w
import importlib.util
import ipaddress
import json
from pathlib import Path
import queue
import subprocess
import sys
import threading
import time
import uuid

spec = importlib.util.spec_from_file_location('live_check', Path(__file__).with_name('live-socks-check.py'))
live = importlib.util.module_from_spec(spec)
spec.loader.exec_module(live)

inet = c.WinDLL('wininet', use_last_error=True)
kernel = c.WinDLL('kernel32', use_last_error=True)
inet.InternetQueryOptionW.argtypes = [w.HANDLE, w.DWORD, w.LPVOID, c.POINTER(w.DWORD)]
inet.InternetSetOptionW.argtypes = [w.HANDLE, w.DWORD, w.LPVOID, w.DWORD]
inet.InternetOpenW.argtypes = [w.LPCWSTR, w.DWORD, w.LPCWSTR, w.LPCWSTR, w.DWORD]
inet.InternetOpenW.restype = w.HANDLE
inet.InternetOpenUrlW.argtypes = [w.HANDLE, w.LPCWSTR, w.LPCWSTR, w.DWORD, w.DWORD, c.c_size_t]
inet.InternetOpenUrlW.restype = w.HANDLE
inet.InternetReadFile.argtypes = [w.HANDLE, w.LPVOID, w.DWORD, c.POINTER(w.DWORD)]
inet.HttpQueryInfoW.argtypes = [w.HANDLE, w.DWORD, w.LPVOID, c.POINTER(w.DWORD), c.POINTER(w.DWORD)]
inet.InternetCloseHandle.argtypes = [w.HANDLE]
kernel.GlobalFree.argtypes = [w.HANDLE]


class Option(c.Structure):
    _fields_ = [('option', w.DWORD), ('value', c.c_uint64)]


class Options(c.Structure):
    _fields_ = [('size', w.DWORD), ('connection', w.LPWSTR), ('count', w.DWORD),
                ('error', w.DWORD), ('options', c.POINTER(Option))]


def check(result):
    if not result:
        raise OSError(c.get_last_error(), 'Windows proxy check failed')
    return result


def settings():
    values = (Option * 4)(Option(10, 0), Option(2, 0), Option(3, 0), Option(4, 0))
    options = Options(c.sizeof(Options), None, 4, 0, values)
    size = w.DWORD(c.sizeof(options))
    try:
        check(inet.InternetQueryOptionW(None, 75, c.byref(options), c.byref(size)))
        return (values[0].value,) + tuple(c.wstring_at(v.value) if v.value else '' for v in values[1:])
    finally:
        for value in values[1:]:
            if value.value:
                kernel.GlobalFree(value.value)


def set_settings(value):
    strings = [c.create_unicode_buffer(text) for text in value[1:]]
    values = (Option * 4)(Option(1, value[0]), *(Option(i + 2, c.addressof(text)) for i, text in enumerate(strings)))
    options = Options(c.sizeof(Options), None, 4, 0, values)
    check(inet.InternetSetOptionW(None, 75, c.byref(options), c.sizeof(options)))
    check(inet.InternetSetOptionW(None, 39, None, 0))
    check(inet.InternetSetOptionW(None, 37, None, 0))
    assert settings() == value, 'test settings did not take effect'


def system_request():
    # INTERNET_OPEN_TYPE_PRECONFIG reads Windows configuration; there is no
    # explicit proxy override on this request and no TLS validation bypass.
    session = check(inet.InternetOpenW('BlueGrass live verification', 0, None, None, 0))
    request = None
    try:
        timeout = w.DWORD(15000)
        for option in (2, 5, 6):
            check(inet.InternetSetOptionW(session, option, c.byref(timeout), c.sizeof(timeout)))
        request = check(inet.InternetOpenUrlW(session, 'https://api.ipify.org', None, 0, 0x80000000 | 0x04000000 | 0x00080000, 0))
        status, length = w.DWORD(), w.DWORD(c.sizeof(w.DWORD))
        check(inet.HttpQueryInfoW(request, 19 | 0x20000000, c.byref(status), c.byref(length), None))
        if status.value != 200:
            report('windows_http_status', status=status.value)
            raise ValueError('probe HTTP failure')
        data = bytearray()
        while True:
            buf, size = c.create_string_buffer(4096), w.DWORD()
            check(inet.InternetReadFile(request, buf, len(buf), c.byref(size)))
            if not size.value:
                break
            data.extend(buf.raw[:size.value])
            if len(data) > 4096:
                raise ValueError('oversized response')
        address = ipaddress.IPv4Address(data.decode('ascii').strip())
        if not address.is_global or address.is_multicast:
            raise ValueError('invalid public IPv4')
        return str(address)
    finally:
        if request:
            inet.InternetCloseHandle(request)
        inet.InternetCloseHandle(session)


class Host:
    def __init__(self, executable):
        self.process = subprocess.Popen([str(Path(executable).resolve()), '--system-proxy'],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            text=True, encoding='utf-8', creationflags=subprocess.CREATE_NO_WINDOW)
        self.responses = queue.Queue()
        def read():
            try:
                for line in self.process.stdout:
                    self.responses.put(json.loads(line))
            finally:
                self.responses.put(None)
        threading.Thread(target=read, daemon=True).start()

    def command(self, name, timeout=35, **kwargs):
        request_id = str(uuid.uuid4())
        self.process.stdin.write(json.dumps({'id': request_id, 'command': name, **kwargs}) + '\n')
        self.process.stdin.flush()
        response = self.responses.get(timeout=timeout)
        if response is None or response.get('id') != request_id:
            raise RuntimeError('private host unavailable')
        return response

    def ok(self, name, timeout=35, **kwargs):
        response = self.command(name, timeout=timeout, **kwargs)
        if response.get('ok') is not True:
            raise RuntimeError('native command rejected')
        return response['snapshot']

    def connect(self, profile):
        self.ok('Connect', profile=profile)
        deadline = time.monotonic() + 35
        while time.monotonic() < deadline:
            state = self.ok('GetStatus')
            if state['connection'] in ('connected', 'failed', 'paused'):
                return state
            time.sleep(.1)
        raise TimeoutError('connection deadline')

    def eof(self):
        self.process.stdin.close()
        assert self.process.wait(timeout=15) == 0, 'EOF cleanup failed'


def report(stage, **values):
    print(json.dumps({'stage': stage, **values}), flush=True)


def wait_observed_exit(host, connected, timeout=30):
    """Read advisory results after connection, without issuing lifecycle commands.

    A timeout means this live check could not verify detector reporting. It does
    not mean the native connection failed or needs an expected-IP admission gate.
    """
    context = connected.get('context')
    if not isinstance(context, dict) or not all(context.get(k) for k in
            ('runtimeEpoch', 'logicalSessionId', 'attemptId', 'resourceKey')):
        raise RuntimeError('connected attempt identity unavailable')
    deadline = time.monotonic() + timeout
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            report('system_exit_observation_timeout', connection='connected',
                   observationAvailable=False, detectorAdvisory=True)
            raise TimeoutError('exit observation unavailable while connection remained connected')
        state = host.ok('GetStatus', timeout=min(5, remaining))
        if (state.get('context') != context or state.get('connection') != 'connected'
                or state.get('gateKnown') is not True or state.get('businessGateOpen') is not True
                or state.get('systemProxyApplied') is not True or state.get('proxyRecoveryRequired')
                or state.get('proxyAddress') != connected.get('proxyAddress')):
            raise RuntimeError('native observation context or forwarding changed')
        value = state.get('verifiedExitIpv4')  # Legacy wire name; actual observation only.
        if value is not None and value != '':
            if not isinstance(value, str):
                raise ValueError('invalid observed IPv4 field type')
            address = ipaddress.IPv4Address(value)
            if not address.is_global or address.is_multicast:
                raise ValueError('invalid observed public IPv4')
            return str(address)
        time.sleep(min(.1, max(0, deadline - time.monotonic())))


def native_check(executable, node, key):
    original = settings()
    host = Host(executable)
    own_settings = None
    injected = None
    profile = {'key': {field: key[field] for field in ('environment', 'dataSource', 'accountKey', 'sourceId', 'kind')},
        'protocol': node['protocol'], 'entry': {'host': node['host'], 'port': node['port']},
        'auth': {'username': node['username'], 'password': node['password']},
        'validUntilUtc': node['expires_at'], 'configVersion': 1,
        'configFingerprint': str(uuid.uuid4()), 'mode': 'system_proxy'}
    try:
        hello = host.ok('Hello')
        assert settings() == original and not hello['systemProxyApplied']
        assert 'system_proxy' in hello['capabilities']
        competing = Host(executable)
        assert competing.process.wait(timeout=8) != 0, 'competing proxy writer accepted'
        competing.process.stdin.close()
        report('system_single_owner', rejectedSecondHost=True, startupUnchanged=True)
        good = host.connect(profile)
        report('system_connection_status', connection=good.get('connection'), errorCode=good.get('errorCode'),
               businessGateOpen=good.get('businessGateOpen'), gateKnown=good.get('gateKnown'),
               systemProxyApplied=good.get('systemProxyApplied'), recoveryRequired=good.get('proxyRecoveryRequired'))
        assert good['connection'] == 'connected' and good['businessGateOpen'] and good['systemProxyApplied'] and not good['proxyRecoveryRequired']
        assert good['context']['resourceKey'] == profile['key']
        own_settings = settings()
        assert own_settings[0] == 3 and own_settings[1] == good['proxyAddress']
        assert own_settings[1] != '127.0.0.1:10808'
        observed = system_request()
        reported = wait_observed_exit(host, good)
        # Each channel must produce a public IPv4; rotating/NAT exits may differ.
        # Neither address is a configured admission condition for the session.
        report('windows_default_http', observed=observed, reportedExit=reported,
               systemProxyApplied=True, detectorAdvisory=True)
        host.ok('Pause')
        assert settings() == own_settings
        try:
            system_request()
        except (OSError, ValueError, KeyError):
            report('system_pause', businessBlocked=True, proxyRetained=True)
        else:
            raise RuntimeError('paused Windows-default request escaped')
        retried = host.connect(profile)
        assert retried['connection'] == 'connected' and retried['businessGateOpen']
        assert retried['context']['logicalSessionId'] == good['context']['logicalSessionId']
        assert retried['context']['attemptId'] != good['context']['attemptId']
        assert settings() == own_settings and retried['proxyAddress'] == good['proxyAddress']
        retry_http = system_request()
        retry_observed = wait_observed_exit(host, retried)
        report('system_protected_retry', sameLogicalSession=True, freshAttempt=True,
               listenerAndSettingsRetained=True, observed=retry_http, reportedExit=retry_observed)
        stopped = host.ok('Disconnect')
        assert settings() == original and not stopped['systemProxyApplied'] and not stopped['proxyRecoveryRequired']
        report('system_disconnect', exactOriginalRestored=True)
        assert host.connect(profile)['connection'] == 'connected'
        host.eof()
        assert settings() == original
        report('system_parent_eof', exactOriginalRestored=True)
        host = Host(executable)
        host.ok('Hello')
        assert host.connect(profile)['connection'] == 'connected'
        own_settings = settings()
        host.process.kill()
        host.process.wait(timeout=10)
        host.process.stdin.close()
        assert settings() == own_settings
        host = Host(executable)
        host.ok('Hello')
        assert settings() == original
        report('system_native_crash_restart', recoveredOwnedState=True)
        assert host.connect(profile)['connection'] == 'connected'
        own_settings = settings()
        injected = (own_settings[0], '127.0.0.1:9', own_settings[2], own_settings[3])
        set_settings(injected)
        lost = host.ok('GetStatus')
        assert lost['connection'] == 'paused' and lost['proxyRecoveryRequired']
        host.process.kill()
        host.process.wait(timeout=10)
        host.process.stdin.close()
        host = Host(executable)
        assert host.ok('Hello')['proxyRecoveryRequired']
        released = host.ok('Disconnect')
        assert released['proxySettingsPreserved'] and not released['proxyRecoveryRequired']
        assert settings() == injected
        host.eof()
        host = Host(executable)
        assert not host.ok('Hello')['proxyRecoveryRequired']
        report('system_ownership_loss', externalChangePreserved=True, forwardingClosed=True, restartAndReleaseSucceeded=True)
        # Undo only our deliberate competing-writer injection after the host
        # has relinquished its obsolete backup, without claiming it restored it.
        if settings() != injected:
            raise RuntimeError('another writer changed test state')
        set_settings(original)
        injected = None
        host.ok('Disconnect')
        host.eof()
        assert settings() == original
        report('system_final_restore', exactOriginalRestored=True)
    finally:
        if injected is not None and settings() == injected:
            set_settings(original)
        if host.process.poll() is None:
            if not host.process.stdin.closed:
                host.process.stdin.close()
            try:
                host.process.wait(timeout=20)
            except subprocess.TimeoutExpired:
                host.process.kill()
                host.process.wait(timeout=5)
        if own_settings is not None and settings() == own_settings:
            recovery = Host(executable)
            try:
                recovery.ok('Hello')
                recovery.ok('Disconnect')
            finally:
                recovery.eof()
        report('cleanup', originalSettingsMatch=settings() == original)


if __name__ == '__main__':
    live.native_check = native_check
    try:
        sys.exit(live.main())
    except Exception as error:
        report('failure', errorType=type(error).__name__)
        sys.exit(1)
