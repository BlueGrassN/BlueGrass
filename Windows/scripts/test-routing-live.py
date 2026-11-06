"""Opt-in Windows routing smoke; preparing this file does not run live probes.

From the repository root, after build-native.ps1 and review:
  python Windows/scripts/test-routing-live.py --executable Windows/native/bin/BlueGrass.Native.exe

Only this command starts the test. The default account file is root testaccount.txt
(two nonempty lines: username, password). No account/profile/token/address/ID is
written to disk or printed. JSONL output contains fixed codes, mode, ordinal,
count and booleans only. Do not import live-socks-check.py: its old diagnostics
print addresses and resource IDs.

Source contract inspected: cmd/bluegrass-native/main.go selects nativehost.Run
with NO arguments. That creates localengine.New(12808) without a proxy manager;
only --system-proxy selects RunSystemProxy and Internet Settings management.
Never pass --system-proxy or --tun here. No service, elevation, routes or firewall
operations, and no access to the existing 10808 proxy. Registry access is read-only.

Domestic parsers mirror winservice/elevated_probes_windows_test.go's strict
parseAcceptanceChinaResponse; TLS verification remains enabled. Baselines use
ProxyHandler({}); explicit local-proxy requests override urllib's otherwise
implicit environment/Windows proxy-bypass lookup. No global opener is installed.

The work deadline defaults to 300 seconds. Each socket operation has a timeout
(at most 20s); cleanup has separate bounded budgets. A watchdog at work deadline
plus 60s covers OS DNS/library stalls, attempts logout for at most 9s, terminates
only our child, then exits unsuccessfully. No result here asserts TUN/WFP or
OS-proxy acceptance. Importing this module starts no network/process operations.
"""

import argparse
import datetime
import hashlib
import http.client
import ipaddress
import json
import os
from pathlib import Path
import queue
import socket
import ssl
import struct
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[2]
ADDRESS = "127.0.0.1:12808"
FOREIGN = "https://www.cloudflare.com/cdn-cgi/trace"
DOMESTIC = ("https://myip.ipip.net/", "https://4.ipw.cn/")
MODES = ("global", "bypass_cn")
CODES = frozenset("""
ARGUMENTS_INVALID WINDOWS_REQUIRED EXECUTABLE_UNAVAILABLE ACCOUNT_INVALID
API_URL_INVALID API_FAILED API_CONTRACT_INVALID API_PAGINATION_INVALID LOGIN_OK
LOGOUT_OK LOGOUT_FAILED INVENTORY_COMPLETE DETECTOR_UNAVAILABLE BASELINE_READY
BASELINE_UNAVAILABLE BASELINE_CHANGED NODE_SKIPPED_PROTOCOL NODE_SKIPPED_PROFILE
NODE_SKIPPED_UNAVAILABLE NODE_SKIPPED_NOT_COMPARABLE NODE_SELECTED NO_WORKING_NODE
SOCKS_FAILED PROBE_FAILED PROBE_FORMAT_INVALID NATIVE_START_FAILED NATIVE_WIRE_FAILED
NATIVE_REJECTED NATIVE_TIMEOUT NATIVE_UNSAFE NATIVE_CONNECT_FAILED NATIVE_EXIT_MISSING
LISTENER_BUSY PROXY_SNAPSHOT_FAILED PROXY_SETTINGS_CHANGED MODE_PROBES MODE_FAILED
COUNTERS_INVALID PAUSE_CHECK PAUSE_FAILED DISCONNECT_FAILED NATIVE_SHUTDOWN
CLEANUP_FAILED SMOKE_TIMEOUT INTERRUPTED INTERNAL_ERROR COMPLETE
MODE_STARTED MODE_CONNECTED MODE_FOREIGN_OK MODE_DOMESTIC_OK PROBE_HTTP_ERROR PROBE_TRANSPORT_ERROR
""".split())


class SmokeFailure(Exception):
    def __init__(self, code):
        self.code = code if code in CODES else "INTERNAL_ERROR"
        super().__init__(self.code)


def emit(code, *, mode=None, ordinal=None, count=None, **checks):
    """The sole output boundary. Never pass wire data or exceptions here."""
    if code not in CODES or (mode is not None and mode not in MODES):
        raise SmokeFailure("INTERNAL_ERROR")
    record = {"code": code}
    if mode is not None:
        record["mode"] = mode
    for key, value in (("ordinal", ordinal), ("count", count)):
        if value is not None:
            if type(value) is not int or value < 0:
                raise SmokeFailure("INTERNAL_ERROR")
            record[key] = value
    if any(type(value) is not bool for value in checks.values()):
        raise SmokeFailure("INTERNAL_ERROR")
    record.update(checks)
    print(json.dumps(record, sort_keys=True), flush=True)


def require(condition, code):
    if not condition:
        raise SmokeFailure(code)


class Budget:
    def __init__(self, seconds):
        self.end = time.monotonic() + seconds

    def remaining(self, maximum=20):
        left = self.end - time.monotonic()
        require(left > 0, "SMOKE_TIMEOUT")
        return min(maximum, left)

    def sleep(self):
        time.sleep(self.remaining(0.15))


class NoApiRedirect(urllib.request.HTTPRedirectHandler):
    # Also used for probes: a detector must not redirect to a different policy.
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class ExplicitLocalProxy(urllib.request.ProxyHandler):
    def __init__(self):
        super().__init__({"http": "http://" + ADDRESS, "https": "http://" + ADDRESS})

    def proxy_open(self, req, proxy, scheme):
        # Standard ProxyHandler calls proxy_bypass(req.host), even with explicit
        # mappings. That can silently consult NO_PROXY or Windows registry. This
        # fixed handler always sends both HTTP and HTTPS CONNECT to our listener.
        require(scheme in ("http", "https"), "PROBE_FAILED")
        req.set_proxy(ADDRESS, "http")
        return None


class IPv4HTTPSConnection(http.client.HTTPSConnection):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._create_connection = self._connect4

    @staticmethod
    def _connect4(address, timeout, source_address=None):
        # Compare IPv4 with IPv4: an IPv6 baseline cannot demonstrate this
        # client's IPv4 routing policy. TLS still verifies the original host.
        if address[0] in {urllib.parse.urlsplit(url).hostname for url in DOMESTIC}:
            # The physical reference must use the same resolver as split mode.
            # This host returns different observations on different CDN targets.
            address = (domestic_ipv4(address[0], Budget(min(timeout, 8))), address[1])
        connection = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        connection.settimeout(timeout)
        try:
            if source_address is not None:
                connection.bind(source_address)
            connection.connect(address)
            return connection
        except BaseException:
            connection.close()
            raise


class IPv4HTTPSHandler(urllib.request.HTTPSHandler):
    def https_open(self, request):
        return self.do_open(IPv4HTTPSConnection, request, context=self._context)


def domestic_ipv4(host, budget):
    """Read-only TCP DNS reference; no Windows DNS/proxy settings are changed."""
    ident = os.urandom(2)
    name = b"".join(bytes([len(label)]) + label.encode("ascii") for label in host.split(".")) + b"\0"
    question = name + b"\0\1\0\1"
    query = ident + b"\1\0\0\1\0\0\0\0\0\0" + question
    with socket.create_connection(("223.5.5.5", 53), budget.remaining(5)) as connection:
        connection.sendall(struct.pack("!H", len(query)) + query)
        length = struct.unpack("!H", exact(connection, 2, budget))[0]
        require(12 <= length <= 4096, "PROBE_FORMAT_INVALID")
        response = exact(connection, length, budget)
    flags, questions, answers = struct.unpack("!HHH", response[2:8])
    require(response[:2] == ident and flags & 0x8000 and not flags & 0x020f
            and questions == 1 and response[12:12+len(question)] == question, "PROBE_FORMAT_INVALID")
    offset = 12 + len(question)
    for _ in range(answers):
        while offset < len(response):
            size = response[offset]
            offset += 1
            if size & 0xc0 == 0xc0:
                offset += 1
                break
            require(size <= 63 and offset + size <= len(response), "PROBE_FORMAT_INVALID")
            if size == 0:
                break
            offset += size
        require(offset + 10 <= len(response), "PROBE_FORMAT_INVALID")
        kind, klass, _, size = struct.unpack("!HHIH", response[offset:offset+10])
        offset += 10
        require(offset + size <= len(response), "PROBE_FORMAT_INVALID")
        if kind == 1 and klass == 1 and size == 4:
            return public_ipv4(socket.inet_ntop(socket.AF_INET, response[offset:offset+4]))
        offset += size
    raise SmokeFailure("PROBE_FORMAT_INVALID")


def opener(proxied=False, ipv4=False):
    context = ssl.create_default_context()
    context.set_alpn_protocols(["http/1.1"])
    return urllib.request.build_opener(
        ExplicitLocalProxy() if proxied else urllib.request.ProxyHandler({}),
        NoApiRedirect(), (IPv4HTTPSHandler if ipv4 else urllib.request.HTTPSHandler)(context=context))


def read_limited(response, limit, budget):
    body = bytearray()
    while len(body) <= limit:
        budget.remaining()
        # read1 returns after one underlying read: slow streaming cannot avoid
        # checking the overall deadline between chunks.
        chunk = response.read1(min(4096, limit + 1 - len(body)))
        if not chunk:
            return bytes(body)
        body.extend(chunk)
    raise SmokeFailure("PROBE_FORMAT_INVALID")


def public_ipv4(value):
    try:
        require(isinstance(value, str), "PROBE_FORMAT_INVALID")
        ip = ipaddress.ip_address(value)
        require(ip.version == 4 and ip.is_global and not ip.is_multicast
                and not ip.is_reserved and not ip.is_loopback, "PROBE_FORMAT_INVALID")
        # Explicitly align the smoke with the native gate's special-range rules.
        require(not any(ip in ipaddress.ip_network(prefix) for prefix in (
            "0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
            "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3")),
            "PROBE_FORMAT_INVALID")
        return str(ip)
    except (ValueError, TypeError):
        raise SmokeFailure("PROBE_FORMAT_INVALID") from None


def parse_detector(url, body):
    require(len(body) <= 4096, "PROBE_FORMAT_INVALID")
    try:
        text = body.decode("utf-8").strip()
    except UnicodeError:
        raise SmokeFailure("PROBE_FORMAT_INVALID") from None
    if url == FOREIGN:
        addresses = [line[3:].strip() for line in text.split("\n") if line.startswith("ip=")]
        require(len(addresses) == 1, "PROBE_FORMAT_INVALID")
        return public_ipv4(addresses[0])
    if url == DOMESTIC[0]:
        require(text.startswith("当前 IP：") and not any(c in text for c in "\r\n<>"),
                "PROBE_FORMAT_INVALID")
        address, separator, location = text[len("当前 IP："):].partition("来自于：")
        require(separator and location.strip() and "当前 IP：" not in location
                and "来自于：" not in location, "PROBE_FORMAT_INVALID")
        return public_ipv4(address.strip())
    require(url == DOMESTIC[1], "PROBE_FORMAT_INVALID")
    return public_ipv4(text)


def probe(url, budget, proxied=False):
    require(url in (FOREIGN, *DOMESTIC), "PROBE_FAILED")
    request = urllib.request.Request(url, headers={
        "Accept": "text/plain", "Connection": "close", "Cache-Control": "no-cache",
        "User-Agent": "BlueGrass-routing-acceptance/0.1"})
    try:
        with opener(proxied, ipv4=True).open(request, timeout=budget.remaining()) as response:
            require(response.status == 200, "PROBE_FAILED")
            return parse_detector(url, read_limited(response, 4096, budget))
    except SmokeFailure:
        raise
    except urllib.error.HTTPError as error:
        emit("PROBE_HTTP_ERROR", count=error.code, proxied=proxied)
        error.close()
        raise SmokeFailure("PROBE_FAILED") from None
    except Exception as error:
        cause = getattr(error, "reason", error)
        code = getattr(cause, "errno", 0)
        emit("PROBE_TRANSPORT_ERROR", count=code if isinstance(code, int) and code >= 0 else 0,
             proxied=proxied, tls=isinstance(cause, ssl.SSLError))
        raise SmokeFailure("PROBE_FAILED") from None


def pause_blocked(url, budget):
    # A generic timeout or a failing remote detector is NOT proof of pause. Both
    # already-working targets must receive the local gate's exact HTTP response.
    request = urllib.request.Request(url.replace("https://", "http://", 1),
                                     headers={"Connection": "close"})
    try:
        with opener(True).open(request, timeout=budget.remaining(5)):
            return False
    except urllib.error.HTTPError as error:
        try:
            return error.code == 503 and read_limited(error, 128, budget) == b"Proxy paused\n"
        finally:
            error.close()
    except Exception:
        return False


class Api:
    def __init__(self, base, budget):
        parsed = urllib.parse.urlsplit(base)
        require(parsed.scheme == "https" and parsed.hostname and parsed.username is None
                and parsed.password is None and not parsed.query and not parsed.fragment
                and len(base) <= 1024, "API_URL_INVALID")
        self.base = base.rstrip("/")
        self.budget = budget
        self.transport = opener(False)
        self.token = None
        self.account = None

    def request(self, path, body=None, budget=None):
        budget = budget or self.budget
        headers = {"Accept": "application/json", "Content-Type": "application/json",
                   "Connection": "close"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        request = urllib.request.Request(self.base + path, headers=headers,
            data=None if body is None else json.dumps(body).encode("utf-8"))
        try:
            with self.transport.open(request, timeout=budget.remaining()) as response:
                require(response.status == 200, "API_FAILED")
                result = json.loads(read_limited(response, 2 * 1024 * 1024, budget))
            require(isinstance(result, dict) and result.get("success") is True
                    and isinstance(result.get("data"), dict), "API_CONTRACT_INVALID")
            return result["data"]
        except SmokeFailure:
            raise
        except urllib.error.HTTPError as error:
            error.close()  # Do not read, log or interpolate server error bodies.
            raise SmokeFailure("API_FAILED") from None
        except Exception:
            raise SmokeFailure("API_FAILED") from None

    def login(self, account_file):
        try:
            lines = [line for line in account_file.read_text(encoding="utf-8-sig").splitlines()
                     if line.strip()]
            require(len(lines) == 2, "ACCOUNT_INVALID")
        except (OSError, UnicodeError):
            raise SmokeFailure("ACCOUNT_INVALID") from None
        try:
            result = self.request("/auth/login", {"username": lines[0].strip(), "password": lines[1],
                "device_id": str(uuid.uuid4()), "device_name": "BlueGrass routing smoke"})
        finally:
            lines.clear()
        self.token = result.get("access_token")
        require(isinstance(self.token, str) and bool(self.token), "API_CONTRACT_INVALID")
        self.account = identity(result.get("customer", {}).get("id"))
        emit("LOGIN_OK", success=True)

    def nodes(self):
        result, cursors, identities = [], set(), set()
        cursor = None
        for _ in range(100):
            query = {"per_page": 100}
            if cursor:
                query["cursor"] = cursor
            page = self.request("/nodes?" + urllib.parse.urlencode(query))
            items, pagination = page.get("items"), page.get("pagination")
            require(isinstance(items, list) and isinstance(pagination, dict), "API_PAGINATION_INVALID")
            require(len(items) <= 100, "API_PAGINATION_INVALID")
            for item in items:
                require(isinstance(item, dict), "API_CONTRACT_INVALID")
                key = identity(item.get("id"))
                require(key not in identities, "API_PAGINATION_INVALID")
                identities.add(key)
                result.append(item)
            more = pagination.get("has_more")
            require(type(more) is bool, "API_PAGINATION_INVALID")
            if not more:
                emit("INVENTORY_COMPLETE", count=len(result), pagination_complete=True)
                return result
            cursor = pagination.get("next_cursor")
            require(isinstance(cursor, str) and 0 < len(cursor) <= 4096
                    and cursor not in cursors, "API_PAGINATION_INVALID")
            cursors.add(cursor)
        raise SmokeFailure("API_PAGINATION_INVALID")

    def close(self):
        ok = True
        if self.token:
            try:
                self.request("/auth/logout", {}, Budget(8))
            except Exception:
                ok = False
            finally:
                self.token = self.account = None
            emit("LOGOUT_OK" if ok else "LOGOUT_FAILED", success=ok)
        return ok


def identity(value):
    require(type(value) in (str, int) and 0 < len(str(value)) <= 128, "API_CONTRACT_INVALID")
    return str(value)


def eligible_node(node):
    try:
        require(node.get("protocol") == "socks5", "NODE_SKIPPED_PROTOCOL")
        require(isinstance(node.get("host"), str) and 0 < len(node["host"]) <= 253
                and not any(c in node["host"] for c in "/\\:@ \r\n"), "NODE_SKIPPED_PROFILE")
        require(type(node.get("port")) is int and 1 <= node["port"] <= 65535, "NODE_SKIPPED_PROFILE")
        for key in ("username", "password"):
            require(isinstance(node.get(key), str) and 1 <= len(node[key].encode()) <= 255,
                    "NODE_SKIPPED_PROFILE")
        require("expires_at" in node, "NODE_SKIPPED_PROFILE")
        if node["expires_at"] is not None:
            expires = datetime.datetime.fromisoformat(node["expires_at"].replace("Z", "+00:00"))
            require(expires.tzinfo is not None and expires > datetime.datetime.now(datetime.timezone.utc),
                    "NODE_SKIPPED_PROFILE")
    except (ValueError, TypeError, AttributeError, UnicodeError):
        raise SmokeFailure("NODE_SKIPPED_PROFILE") from None


def exact(sock, size, budget):
    result = bytearray()
    while len(result) < size:
        sock.settimeout(budget.remaining(12))
        chunk = sock.recv(size - len(result))
        require(bool(chunk), "SOCKS_FAILED")
        result.extend(chunk)
    return bytes(result)


def socks_socket(node, host, port, budget):
    # A fresh TCP connection per probe; no environment/OS HTTP proxy is involved.
    sock = socket.create_connection((node["host"], node["port"]), budget.remaining(12))
    try:
        username, password = node["username"].encode(), node["password"].encode()
        sock.sendall(b"\x05\x01\x02")  # Require actual username/password authentication.
        require(exact(sock, 2, budget) == b"\x05\x02", "SOCKS_FAILED")
        sock.sendall(b"\x01" + bytes([len(username)]) + username + bytes([len(password)]) + password)
        require(exact(sock, 2, budget) == b"\x01\x00", "SOCKS_FAILED")
        target = host.encode("idna")
        sock.sendall(b"\x05\x01\x00\x03" + bytes([len(target)]) + target + struct.pack("!H", port))
        version, status, reserved, family = exact(sock, 4, budget)
        require(version == 5 and status == 0 and reserved == 0, "SOCKS_FAILED")
        length = 4 if family == 1 else 16 if family == 4 else exact(sock, 1, budget)[0] if family == 3 else 0
        require(length > 0, "SOCKS_FAILED")
        exact(sock, length + 2, budget)
        return sock
    except BaseException:
        sock.close()
        raise


def node_probe(node, url, budget):
    raw = None
    try:
        parsed = urllib.parse.urlsplit(url)
        require(url in (FOREIGN, *DOMESTIC), "PROBE_FAILED")
        raw = socks_socket(node, parsed.hostname, 443, budget)
        raw.settimeout(budget.remaining(12))
        with ssl.create_default_context().wrap_socket(raw, server_hostname=parsed.hostname) as conn:
            conn.sendall(("GET " + parsed.path + " HTTP/1.1\r\nHost: " + parsed.hostname +
                          "\r\nAccept: text/plain\r\nConnection: close\r\nCache-Control: no-cache\r\n\r\n").encode())
            with http.client.HTTPResponse(conn) as response:
                response.begin()
                require(response.status == 200, "PROBE_FAILED")
                return parse_detector(url, read_limited(response, 4096, budget))
    except SmokeFailure:
        raise
    except Exception:
        raise SmokeFailure("PROBE_FAILED") from None
    finally:
        if raw is not None:
            raw.close()


def baseline(budget):
    for ordinal, url in enumerate(DOMESTIC, 1):
        try:
            first = probe(url, budget)
            if probe(url, budget) == first:
                emit("BASELINE_READY", ordinal=ordinal, stable=True, explicit_direct=True)
                return url, first
        except SmokeFailure as error:
            if error.code == "SMOKE_TIMEOUT":
                raise
        emit("DETECTOR_UNAVAILABLE", ordinal=ordinal, success=False)
    raise SmokeFailure("BASELINE_UNAVAILABLE")


def select_node(api, candidates, domestic, direct, budget):
    for ordinal, item in enumerate(candidates, 1):
        budget.remaining()
        if item.get("protocol") not in (None, "socks5"):
            emit("NODE_SKIPPED_PROTOCOL", ordinal=ordinal)
            continue
        node = api.request("/nodes/" + urllib.parse.quote(identity(item["id"]), safe=""))
        require(identity(node.get("id")) == identity(item["id"]), "API_CONTRACT_INVALID")
        try:
            eligible_node(node)
        except SmokeFailure as error:
            emit(error.code, ordinal=ordinal)
            continue
        try:
            foreign = node_probe(node, FOREIGN, budget)
            china = node_probe(node, domestic, budget)
            stable = node_probe(node, FOREIGN, budget) == foreign
        except SmokeFailure as error:
            if error.code == "SMOKE_TIMEOUT":
                raise
            emit("NODE_SKIPPED_UNAVAILABLE", ordinal=ordinal, success=False)
            continue
        # Different/rotating destination exits cannot demonstrate this equality
        # test. Skip only during selection, never retry a native routing failure.
        if not stable or foreign != china or foreign == direct:
            emit("NODE_SKIPPED_NOT_COMPARABLE", ordinal=ordinal, stable=stable,
                 detector_exits_equal=foreign == china, differs_from_direct=foreign != direct)
            continue
        emit("NODE_SELECTED", ordinal=ordinal, authenticated=True, stable=True)
        return ordinal, node, foreign
    raise SmokeFailure("NO_WORKING_NODE")


def proxy_settings():
    try:
        import winreg
        base = r"Software\Microsoft\Windows\CurrentVersion\Internet Settings"
        result = []
        for path, names in ((base, ("ProxyEnable", "ProxyServer", "ProxyOverride", "AutoConfigURL", "AutoDetect")),
                            (base + r"\Connections", ("DefaultConnectionSettings", "SavedLegacySettings"))):
            try:
                with winreg.OpenKey(winreg.HKEY_CURRENT_USER, path, 0, winreg.KEY_READ) as key:
                    for name in names:
                        try:
                            result.append(winreg.QueryValueEx(key, name))
                        except FileNotFoundError:
                            result.append(None)
            except FileNotFoundError:
                result.extend([None] * len(names))
        return tuple(result)  # May contain proxy URLs; memory-only, never emitted.
    except Exception:
        raise SmokeFailure("PROXY_SNAPSHOT_FAILED") from None


def listener_free():
    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_EXCLUSIVEADDRUSE, 1)
            sock.bind(("127.0.0.1", 12808))
        return True
    except OSError:
        return False


class Native:
    def __init__(self, executable, budget, settings):
        self.executable, self.budget, self.settings = executable, budget, settings
        self.process = None
        self.responses = queue.Queue(maxsize=8)
        self.wire_ok = True

    def _offer(self, value):
        try:
            self.responses.put_nowait(value)
        except queue.Full:
            pass

    def _reader(self):
        try:
            while True:
                line = self.process.stdout.readline(65538)
                if not line:
                    break
                if len(line) > 65537 or not line.endswith(b"\n"):
                    break
                self._offer(json.loads(line))
        except Exception:
            pass  # Never let a thread traceback echo raw protocol input.
        finally:
            self._offer(None)

    def __enter__(self):
        self.budget.remaining()
        require(listener_free(), "LISTENER_BUSY")
        # Child env is an in-memory copy; Windows/user environment is unchanged.
        env = {key: value for key, value in os.environ.items()
               if key.lower() not in {"http_proxy", "https_proxy", "all_proxy", "no_proxy"}}
        try:
            self.process = subprocess.Popen([str(self.executable)], stdin=subprocess.PIPE,
                stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env,
                creationflags=subprocess.CREATE_NO_WINDOW)
        except Exception:
            raise SmokeFailure("NATIVE_START_FAILED") from None
        threading.Thread(target=self._reader, daemon=True).start()
        try:
            hello = self.command("Hello")
            require(hello.get("connection") == "disconnected" and hello.get("businessGateOpen") is False,
                    "NATIVE_UNSAFE")
            return self
        except BaseException:
            self.close()
            raise

    def command(self, command, budget=None, **parameters):
        budget = budget or self.budget
        require(self.process is not None and self.process.poll() is None and self.wire_ok,
                "NATIVE_WIRE_FAILED")
        request_id = str(uuid.uuid4())
        payload = (json.dumps({"id": request_id, "command": command, **parameters}) + "\n").encode()
        require(len(payload) <= 4096, "NATIVE_WIRE_FAILED")
        try:
            self.process.stdin.write(payload)
            self.process.stdin.flush()
            response = self.responses.get(timeout=budget.remaining())
        except queue.Empty:
            self.wire_ok = False
            raise SmokeFailure("NATIVE_TIMEOUT") from None
        except SmokeFailure:
            self.wire_ok = False
            raise
        except Exception:
            self.wire_ok = False
            raise SmokeFailure("NATIVE_WIRE_FAILED") from None
        require(isinstance(response, dict) and response.get("id") == request_id, "NATIVE_WIRE_FAILED")
        require(response.get("ok") is True, "NATIVE_REJECTED")
        snapshot = response.get("snapshot")
        require(isinstance(snapshot, dict), "NATIVE_WIRE_FAILED")
        capabilities = snapshot.get("capabilities")
        local_mode = snapshot.get("actualMode") in ("none", "local_proxy")
        # Connect acknowledges before asynchronous Prepare completes; the native
        # controller intentionally reports unknown mode while its gate is closed.
        preparing = (snapshot.get("actualMode") == "unknown"
                     and snapshot.get("connection") in ("starting", "connecting")
                     and snapshot.get("businessGateOpen") is False)
        require(isinstance(capabilities, list) and "local_http_proxy" in capabilities
                and not any(value in capabilities for value in ("system_proxy", "tun", "persistent_protection"))
                and snapshot.get("systemProxyApplied") is False
                and snapshot.get("proxyRecoveryRequired") is False
                and snapshot.get("proxyAddress") == ADDRESS
                and (local_mode or preparing), "NATIVE_UNSAFE")
        require(proxy_settings() == self.settings, "PROXY_SETTINGS_CHANGED")
        return snapshot

    def connected(self):
        for _ in range(200):
            state = self.command("GetStatus")
            if state.get("connection") in ("failed", "paused", "disconnected"):
                raise SmokeFailure("NATIVE_CONNECT_FAILED")
            if state.get("connection") == "connected" and state.get("businessGateOpen") is True:
                require(state.get("gateKnown") is True, "NATIVE_UNSAFE")
                return state
            self.budget.sleep()
        raise SmokeFailure("NATIVE_TIMEOUT")

    def observed(self):
        until = min(self.budget.end, time.monotonic() + 30)
        while time.monotonic() < until:
            state = self.connected()
            if state.get("verifiedExitIpv4"):
                public_ipv4(state["verifiedExitIpv4"])
                return state
            self.budget.sleep()
        raise SmokeFailure("NATIVE_EXIT_MISSING")

    def close(self):
        if self.process is None:
            return True
        clean, freed, unchanged = False, False, False
        try:
            if self.process.poll() is None:
                # EOF invokes Host.Close, even after a failed/timed-out command.
                try:
                    self.process.stdin.close()
                except (OSError, ValueError):
                    pass
                try:
                    self.process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    self.process.kill()
                    self.process.wait(timeout=5)
            clean = self.process.returncode == 0
            until = time.monotonic() + 3
            while time.monotonic() < until:
                freed = listener_free()
                if freed:
                    break
                time.sleep(0.1)
            unchanged = proxy_settings() == self.settings
        except BaseException:
            # Only terminate the exact child we created; never enumerate/kill
            # listeners or touch the user's existing proxy process.
            if self.process.poll() is None:
                try:
                    self.process.kill()
                    self.process.wait(timeout=5)
                except Exception:
                    pass
        finally:
            emit("NATIVE_SHUTDOWN", clean_exit=clean, listener_freed=freed, os_proxy_unchanged=unchanged)
        return clean and freed and unchanged

    def __exit__(self, kind, value, traceback):
        ok = self.close()
        if not ok and kind is None:
            raise SmokeFailure("CLEANUP_FAILED")
        return False


class DeadlineGuard:
    """Last-resort cleanup for OS DNS calls that ignore socket timeouts."""
    def __init__(self, seconds, api, settings):
        self.api, self.settings, self.native = api, settings, None
        self.timer = threading.Timer(seconds + 60, self._expired)
        self.timer.daemon = True

    def stop_child(self):
        native = self.native
        if native is not None and native.process is not None:
            if native.process.poll() is None:
                native.process.kill()
                native.process.wait(timeout=5)

    def _expired(self):
        # Do not wait indefinitely on the worker's DNS call or control pipe.
        # All state remains in memory; this never targets an unrelated process.
        try:
            self.api.budget.end = 0
            self.stop_child()
            result = queue.Queue(maxsize=1)

            def logout():
                try:
                    result.put_nowait(self.api.close())
                except BaseException:
                    pass

            worker = threading.Thread(target=logout, daemon=True)
            worker.start()
            worker.join(timeout=9)
            try:
                logout_ok = result.get_nowait()
            except queue.Empty:
                logout_ok = False
            self.stop_child()
            emit("SMOKE_TIMEOUT", success=False, listener_freed=listener_free(),
                 os_proxy_unchanged=proxy_settings() == self.settings, logout_completed=logout_ok)
        except BaseException:
            emit("SMOKE_TIMEOUT", success=False)
        finally:
            # A stuck daemon/DNS call must not keep the acceptance command alive.
            os._exit(1)


def counters(snapshot):
    metrics = snapshot.get("traffic")
    require(isinstance(metrics, dict) and metrics.get("available") is True, "COUNTERS_INVALID")
    result = []
    for name in ("uploadBytes", "downloadBytes"):
        value = metrics.get(name)
        require(isinstance(value, str) and value.isascii() and value.isdecimal() and len(value) <= 20,
                "COUNTERS_INVALID")
        result.append(int(value))
    return tuple(result)


def profile(api, node, mode):
    return {"key": {"environment": api.base, "dataSource": "api_v2", "accountKey": api.account,
                    "sourceId": identity(node["id"]), "kind": "subscription"},
            "protocol": "socks5", "entry": {"host": node["host"], "port": node["port"]},
            "auth": {"username": node["username"], "password": node["password"]},
            "validUntilUtc": node["expires_at"], "configVersion": 1,
            "configFingerprint": hashlib.sha256(json.dumps(node, sort_keys=True).encode()).hexdigest(),
            "mode": "system_proxy", "routingMode": mode}


def check_mode(native, api, node, ordinal, mode, domestic, direct, selected, budget):
    emit("MODE_STARTED", mode=mode)
    native.command("Connect", profile=profile(api, node, mode))
    before = native.observed()
    emit("MODE_CONNECTED", mode=mode)
    up_before, down_before = counters(before)
    foreign = probe(FOREIGN, budget, proxied=True)
    emit("MODE_FOREIGN_OK", mode=mode)
    china = probe(domestic, budget, proxied=True)
    emit("MODE_DOMESTIC_OK", mode=mode)
    after = native.observed()
    up_after, down_after = counters(after)
    checks = {
        "foreign_matches_selected": foreign == selected,
        "reported_exit_matches_foreign": after["verifiedExitIpv4"] == foreign,
        "domestic_matches_expected": china == (direct if mode == "bypass_cn" else selected),
        "domestic_matches_direct": china == direct,
        "domestic_matches_selected": china == selected,
        "baseline_stable": probe(domestic, budget) == direct,
        "upload_increased": up_after > up_before,
        "download_increased": down_after > down_before,
        "traffic_available": True,
        "os_proxy_unchanged": proxy_settings() == native.settings,
    }
    emit("MODE_PROBES", mode=mode, ordinal=ordinal, **checks)
    # In global mode domestic_matches_direct must be false; do not include it in
    # an indiscriminate all(checks.values()) pass criterion.
    require(all(value for key, value in checks.items() if key not in ("domestic_matches_direct", "domestic_matches_selected")), "MODE_FAILED")
    paused = native.command("Pause")
    foreign_blocked = pause_blocked(FOREIGN, budget)
    domestic_blocked = pause_blocked(domestic, budget)
    gate_closed = (paused.get("connection") == "paused" and paused.get("gateKnown") is True
                   and paused.get("businessGateOpen") is False)
    emit("PAUSE_CHECK", mode=mode, gate_closed=gate_closed,
         foreign_blocked=foreign_blocked, domestic_blocked=domestic_blocked)
    require(gate_closed and foreign_blocked and domestic_blocked, "PAUSE_FAILED")
    disconnected = native.command("Disconnect")
    require(disconnected.get("connection") == "disconnected"
            and disconnected.get("businessGateOpen") is False, "DISCONNECT_FAILED")


class SafeArgumentParser(argparse.ArgumentParser):
    def error(self, message):
        # argparse's default error includes the offending raw command arguments.
        raise SmokeFailure("ARGUMENTS_INVALID")


def main():
    parser = SafeArgumentParser(description="Opt-in sanitized local HTTP routing smoke; no OS proxy changes.")
    parser.add_argument("--executable", required=True, help="Explicit staged BlueGrass.Native.exe path; no native flags.")
    parser.add_argument("--account-file", type=Path, default=ROOT / "testaccount.txt")
    parser.add_argument("--api", default="https://client-api.sunip.vip/api/client/v1")
    parser.add_argument("--timeout-seconds", type=int, default=300)
    args = parser.parse_args()
    require(os.name == "nt", "WINDOWS_REQUIRED")
    require(30 <= args.timeout_seconds <= 900, "ARGUMENTS_INVALID")
    executable = Path(args.executable).resolve()
    require(executable.is_file() and executable.name.lower() == "bluegrass.native.exe", "EXECUTABLE_UNAVAILABLE")
    require(listener_free(), "LISTENER_BUSY")
    settings = proxy_settings()
    budget = Budget(args.timeout_seconds)
    api = Api(args.api, budget)
    guard = DeadlineGuard(args.timeout_seconds, api, settings)
    guard.timer.start()
    completed = False
    try:
        try:
            api.login(args.account_file)
            candidates = api.nodes()  # Finish every page before choosing any resource.
            domestic, direct = baseline(budget)
            ordinal, node, selected = select_node(api, candidates, domestic, direct, budget)
            guard.native = Native(executable, budget, settings)
            with guard.native as native:
                for mode in MODES:
                    check_mode(native, api, node, ordinal, mode, domestic, direct, selected, budget)
            completed = True
        finally:
            logout_ok = api.close()
        require(completed and logout_ok, "LOGOUT_FAILED")
        require(proxy_settings() == settings, "PROXY_SETTINGS_CHANGED")
        require(listener_free(), "CLEANUP_FAILED")
        emit("COMPLETE", count=2, success=True, same_resource=True, listener_freed=True)
        return 0
    finally:
        guard.timer.cancel()


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SmokeFailure as failure:
        emit(failure.code, success=False)
        sys.exit(1)
    except KeyboardInterrupt:
        emit("INTERRUPTED", success=False)
        sys.exit(130)
    except SystemExit:
        raise
    except BaseException:
        emit("INTERNAL_ERROR", success=False)
        sys.exit(1)
