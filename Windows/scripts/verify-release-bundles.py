"""Read-only inventory/hash verification for the current Windows ZIP.

Never reads account credentials, decrypts session stores or executes payloads.
"""
import hashlib
import json
import os
import re
import shutil
import subprocess
from pathlib import Path
import struct
import zipfile

ROOT = Path(__file__).resolve().parents[1]
ROUTING_ASSETS = ROOT.parent / 'core/routingpolicy/assets'
TOOLCHAIN = json.loads((ROOT / 'toolchain.json').read_text(encoding='utf-8-sig'))
DRIVER = TOOLCHAIN['wintun']
RETIRED_MODULE = re.compile(r'^(github\.com/sagernet/(sing-box|sing-tun|gvisor)(/|$)|gvisor\.dev(/|$))')
NATIVE = (
    "BlueGrass.Native.exe", "BlueGrass.Service.exe", "BlueGrass.Repair.exe",
    "BlueGrass.Update.exe", "BlueGrass.Diagnostics.exe",
)
RUNTIME = set(NATIVE) | {
    "BlueGrass.exe", "flutter_windows.dll", "data/app.so", "data/icudtl.dat",
    "wintun.dll", "LICENSE.Xray-core", "LICENSE.wintun", "LICENSE.sing", "msvcp140.dll", "vcruntime140.dll",
}
REQUIRED = {"BlueGrass.exe"} | {"runtime/" + name for name in RUNTIME}


def verify_driver_bytes(dll, license_text):
    assert hashlib.sha256(dll).hexdigest() == DRIVER['dllSHA256'], 'unapproved_wintun_dll'
    assert hashlib.sha256(license_text).hexdigest() == DRIVER['licenseSHA256'], 'unapproved_wintun_license'
    assert dll == (ROOT / DRIVER['dllSourcePath']).read_bytes(), 'stale_wintun_source'
    assert license_text == (ROOT / DRIVER['licenseSourcePath']).read_bytes(), 'stale_wintun_license'
    pe = struct.unpack_from('<I', dll, 0x3c)[0]
    assert dll[pe:pe+4] == b'PE\0\0' and struct.unpack_from('<H', dll, pe+4)[0] == 0x8664, 'wintun_not_amd64'


def verify_driver(directory):
    directory = Path(directory).absolute()
    verify_driver_bytes((directory / 'wintun.dll').read_bytes(), (directory / 'LICENSE.wintun').read_bytes())
    powershell = shutil.which('pwsh') or str(Path(os.environ['SystemRoot']) / 'System32/WindowsPowerShell/v1.0/powershell.exe')
    subprocess.run([powershell, '-NoProfile', '-NonInteractive', '-File',
                    str(ROOT / 'scripts/prepare-wintun.ps1'), '-Destination', str(directory), '-VerifyOnly'], check=True)
    return {'version': DRIVER['version'], 'architecture': DRIVER['architecture'],
            'sha256': DRIVER['dllSHA256'], 'official_authenticode_verified': True}


def verify_native_build_info(info):
    has_core = False
    previous_module = ''
    for line in info.splitlines():
        fields = line.split()
        if len(fields) >= 2 and fields[0] in ('dep', '=>'):
            assert not RETIRED_MODULE.match(fields[1]), 'retired_runtime_dependency_in_binary'
            if fields[0] == '=>':
                assert previous_module != 'github.com/xtls/xray-core', 'replaced_xray_core'
            else:
                previous_module = fields[1]
                if fields[1] == 'github.com/xtls/xray-core':
                    assert len(fields) >= 3 and fields[2] == TOOLCHAIN['embeddedCoreModule'], 'unapproved_xray_core'
                    has_core = True
    assert re.search(r'^\s*build\s+GOOS=windows\s*$', info, re.M), 'native_not_windows'
    assert re.search(r'^\s*build\s+GOARCH=amd64\s*$', info, re.M), 'native_not_amd64'
    return has_core


def verify_native_directory(directory):
    go = shutil.which('go') or 'D:/Program Files/Go/bin/go.exe'
    build_info = {}
    for helper in NATIVE:
        info = subprocess.check_output([go, 'version', '-m', str(Path(directory) / helper)], text=True)
        build_info[helper] = verify_native_build_info(info)
    assert build_info['BlueGrass.Native.exe'] and build_info['BlueGrass.Service.exe'], 'missing_pinned_xray_in_engine'
    return build_info


def pe_imports(binary):
    pe = struct.unpack_from('<I', binary, 0x3c)[0]
    assert binary[pe:pe+4] == b'PE\0\0', 'invalid_launcher_pe'
    count = struct.unpack_from('<H', binary, pe+6)[0]
    optional_size = struct.unpack_from('<H', binary, pe+20)[0]
    optional = pe + 24
    assert struct.unpack_from('<H', binary, optional)[0] == 0x20b, 'launcher_not_x64'
    sections = optional + optional_size

    def offset(rva):
        for i in range(count):
            virtual_size, address, raw_size, raw = struct.unpack_from('<IIII', binary, sections+i*40+8)
            if address <= rva < address + max(virtual_size, raw_size):
                return raw + rva - address
        raise AssertionError('invalid_import_rva')

    table = offset(struct.unpack_from('<I', binary, optional+120)[0])
    result = []
    while any(binary[table:table+20]):
        name = offset(struct.unpack_from('<I', binary, table+12)[0])
        result.append(binary[name:binary.index(b'\0', name)].decode('ascii').lower())
        table += 20
    return result


def verify(name):
    path = ROOT / "dist" / name
    with zipfile.ZipFile(path) as archive:
        entries = archive.namelist()
        names = {entry.replace("\\", "/") for entry in entries}
        assert len(entries) == len(names), "duplicate_archive_entry"
        assert REQUIRED <= names, "missing_runtime_payload"
        assert (ROUTING_ASSETS / 'metadata.json').is_file(), 'missing_routing_source_assets'
        for source in ROUTING_ASSETS.rglob('*'):
            if source.is_file():
                relative = source.relative_to(ROUTING_ASSETS).as_posix()
                assert archive.read('runtime/data/routing-policy/' + relative) == source.read_bytes(), 'stale_routing_data_or_notice'
        assert {name.split('/')[0] for name in names} == {"BlueGrass.exe", "runtime"}, "cluttered_bundle_root"
        assert not any(Path(name).suffix.lower() in ('.lib', '.pdb', '.obj', '.exp') for name in names), "build_only_payload"
        assert not any(
            n.startswith("/") or ".." in n.split("/") or any(
                value in n.lower() for value in (
                    "testaccount", ".dpapi", ".private.json", ".artifacts/",
                    "integration_test/", "main_fixture", "/test/",
                )
            ) for n in names
        ), "unexpected_private_or_test_payload"
        retired_files = {'license.sing-box', 'license.sing-tun', 'license.gvisor', 'sing-box.exe', 'sing-tun.dll', 'gvisor.dll'}
        assert not any(Path(n).name.lower() in retired_files for n in names), "retired_runtime_payload"
        assert not any(n.endswith("BlueGrass.test-binding.json") for n in names), "obsolete_test_binding"
        assert {n for n in names if Path(n).name.lower() == 'wintun.dll'} == {'runtime/wintun.dll'}, 'wintun_must_be_app_local'
        verify_driver_bytes(archive.read('runtime/wintun.dll'), archive.read('runtime/LICENSE.wintun'))
        for license_name in ('LICENSE.Xray-core', 'LICENSE.wintun', 'LICENSE.sing'):
            original = (ROOT / 'app/assets' / license_name).read_bytes()
            assert archive.read('runtime/' + license_name) == original, 'stale_runtime_license'
            assert archive.read('runtime/data/flutter_assets/assets/' + license_name) == original, 'stale_flutter_license'
        launcher = archive.read('BlueGrass.exe')
        assert launcher == (ROOT / '.artifacts/launcher/Release/BlueGrassLauncher.exe').read_bytes(), 'stale_launcher'
        imports = pe_imports(launcher)
        assert set(imports) <= {'kernel32.dll', 'user32.dll'}, 'launcher_requires_non_system_dll'
        assert archive.read('runtime/BlueGrass.exe') == (ROOT / 'app/build/windows/x64/runner/Release/BlueGrass.exe').read_bytes(), 'stale_gui'
        icon = (ROOT / 'app/windows/runner/resources/app_icon.ico').read_bytes()
        for index in range(struct.unpack_from('<H', icon, 4)[0]):
            size, offset = struct.unpack_from('<II', icon, 6 + index * 16 + 8)
            pixels = icon[offset:offset + size]
            assert pixels in launcher and pixels in archive.read('runtime/BlueGrass.exe'), 'stale_packaged_icon'
        for helper in NATIVE:
            assert hashlib.sha256(archive.read("runtime/" + helper)).digest() == hashlib.sha256(
                (ROOT / "native" / "bin" / helper).read_bytes()
            ).digest(), "stale_native_payload"
        assert any(
            "https://client-api.sunip.vip/client-config/config.json".encode(encoding)
            in archive.read("runtime/data/app.so") for encoding in ("ascii", "utf-16-le")
        ), "missing_shared_config_endpoint"
        for payload in ("BlueGrass.exe", "data/app.so", "BlueGrass.Service.exe"):
            binary = archive.read("runtime/" + payload)
            assert any("client-api.sunip.vip".encode(encoding) in binary for encoding in (
                "ascii", "utf-16-le"
            )), "missing_compiled_production_origin"
            assert not any(host.encode(encoding) in binary
                           for host in ("test-api.dos.im", "client-api.sunip.cc")
                           for encoding in ("ascii", "utf-16-le")), "retired_origin_in_runtime_payload"
        build_info = verify_native_directory(ROOT / 'native/bin')
        expanded = ROOT / 'dist/BlueGrass-Windows-x64'
        archive_files = {n for n in names if not n.endswith('/')}
        assert {p.relative_to(expanded).as_posix() for p in expanded.rglob('*') if p.is_file()} == archive_files, 'stale_extracted_inventory'
        for entry in archive.infolist():
            if not entry.is_dir():
                relative = entry.filename.replace('\\', '/')
                assert (expanded / relative).read_bytes() == archive.read(entry), 'stale_extracted_payload'
        driver = verify_driver(expanded / 'runtime')
        return {
            "wintun": driver, "retired_runtime_dependencies_absent": True,
            "xray_engines_verified": build_info,
            "updated_icon_verified": True, "extracted_payload_matches": True,
            "file": str(path), "bytes": path.stat().st_size,
            "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
            "entry_count": len(names), "current_native_hashes_match": True,
            "root_entries": ["BlueGrass.exe", "runtime/"], "build_only_files_excluded": True,
            "launcher_imports": imports,
            "compiled_production_origin_verified": True,
            "test_binding": False, "private_test_files_excluded": True,
            "signed_release": False,
        }


if __name__ == "__main__":
    records = [verify("BlueGrass-Windows-x64-dev.zip")]
    output = ROOT / ".artifacts" / "verification" / "observed-exit-bundle.json"
    output.write_text(json.dumps(records, indent=2), encoding="utf-8")
    print(json.dumps(records, indent=2))
