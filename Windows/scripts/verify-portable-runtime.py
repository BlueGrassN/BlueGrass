"""Exercise packaged helpers without connecting a resource or changing proxy settings."""
import json
import os
from pathlib import Path
import socket
import subprocess
import winreg
import importlib.util

ROOT = Path(__file__).resolve().parents[1]
RUNTIME = ROOT / 'dist/BlueGrass-Windows-x64/runtime'


def proxy_settings():
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER,
                       r'Software\Microsoft\Windows\CurrentVersion\Internet Settings') as key:
        result = {}
        for name in ('ProxyEnable', 'ProxyServer', 'ProxyOverride', 'AutoConfigURL'):
            try:
                result[name] = winreg.QueryValueEx(key, name)
            except FileNotFoundError:
                result[name] = None
        return result


def invoke(name, data, arguments=()):
    process = subprocess.run([str(RUNTIME / name), *arguments], input=data,
                             capture_output=True, text=True, timeout=15,
                             cwd=os.environ['SystemRoot'],
                             creationflags=subprocess.CREATE_NO_WINDOW)
    assert process.returncode == 0, 'helper_process_failed'
    return [json.loads(line) for line in process.stdout.splitlines() if line]


def verify():
    spec = importlib.util.spec_from_file_location('release_bundles', ROOT / 'scripts/verify-release-bundles.py')
    release = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(release)
    driver = release.verify_driver(RUNTIME)
    build_info = release.verify_native_directory(RUNTIME)
    before = proxy_settings()
    with socket.socket() as port:
        port.bind(('127.0.0.1', 12808))  # Refuse to compete with an existing client.
    try:
        replies = invoke('BlueGrass.Native.exe',
                         '{"id":"portable-hello","command":"Hello"}\n'
                         '{"id":"portable-status","command":"GetStatus"}\n')
        for request_id in ('portable-hello', 'portable-status'):
            reply = next(item for item in replies if item.get('id') == request_id)
            assert reply['ok'] is True
            assert reply['snapshot']['proxyAddress'] == '127.0.0.1:12808'
            assert reply['snapshot']['systemProxyApplied'] is False
            assert reply['snapshot']['businessGateOpen'] is False
        diagnostic = invoke('BlueGrass.Diagnostics.exe', '{}')[0]
        assert diagnostic['version'] == 1
        assert all(item['status'] == 'skipped' for item in diagnostic['results'])
        update = invoke('BlueGrass.Update.exe', json.dumps({
            'v': 1, 'id': '0123456789abcdef0123456789abcdef', 'op': 'startup_policy',
        }), ['--rpc'])[0]
        assert update['v'] == 1 and update['id'] == '0123456789abcdef0123456789abcdef'
        # This unsigned portable build must preserve the existing release boundary.
        assert update['ok'] is False and update['error'] == 'UPDATE_UNCONFIGURED'
        with socket.socket() as port:
            port.bind(('127.0.0.1', 12808))  # Native EOF released its listener.
    finally:
        assert proxy_settings() == before, 'system_proxy_settings_changed'
    report = {'wintun': driver, 'xray_engines_verified': build_info,
              'retired_runtime_dependencies_absent': True,
              'native_hello_status': 'passed', 'fixed_port': 12808,
              'listener_released': True, 'diagnostics_helper': 'passed_without_network_probes',
              'update_helper': update['error'], 'system_proxy_unchanged': True}
    (ROOT / '.artifacts/verification/portable-runtime.json').write_text(
        json.dumps(report, indent=2), encoding='utf-8')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    verify()
