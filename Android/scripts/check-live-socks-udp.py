"""Opt-in, independent SOCKS5 UDP diagnostic. Importing does not start any work.

Parent invocation from the repository root (this file has NOT been run live):
  python Android/scripts/check-live-socks-udp.py --ordinal 1
  python Android/scripts/check-live-socks-udp.py --ordinal 1 --wire-destination stun
  python Android/scripts/check-live-socks-udp.py --ordinal 1 --allow-authenticated-public-relay

Reads only root testaccount.txt at runtime (two nonempty lines: username/password).
Uses the fixed HTTPS client API, refuses redirects and environment proxies, logs
in with a fresh device ID, finishes node pagination, fetches the selected node's
detail, and attempts logout in finally. Ordinal is 1-based API inventory order,
NOT the order after filtering protocols. Credentials/tokens/addresses/IDs and
server bodies/errors are never printed or written to disk. Do not enable HTTP
debugging. No native client, emulator, adb, proxy configuration or route changes.

One compact JSON report; checks[0]/checks[1] are Cloudflare/Google respectively.
Each check has method_ok, auth_ok, associate_ok, relay_same_entry,
relay_unspecified, relay_port_nonzero, relay_atyp, relay_public_ipv4,
relay_private, relay_loopback, relay_matches_entry_dns, stun_ok, attempts,
received, provider_ordinal and a fixed code. associate_ok means a complete successful
SOCKS reply, NOT that its relay passed the separate address restrictions.
By default only BND IPv4 equal to the pinned public entry IPv4 or 0.0.0.0 is
usable, pinned to that entry IPv4. With --allow-authenticated-public-relay, only
an actual ATYP1 public BND IPv4 and nonzero port from the successful authenticated
association are usable; that exact response tuple is used without substitution.
This opt-in rejects 0.0.0.0. Domain/IPv6/private relays are never used in either mode.
Classification is informational only: relay_public_ipv4 uses the existing
public4 filter; relay_private/relay_loopback use ipaddress properties (which can
overlap). relay_matches_entry_dns compares against every public IPv4 candidate
from the same entry-host resolution used to select the TCP peer. It does not
authorize another candidate as a relay. Non-IPv4 BND values have all four false.

Default UDP_ASSOCIATE wire destination is 0.0.0.0:0. Mode 'stun' instead puts
each resolved STUN IPv4:port into that request, to reproduce the reported Xray
behavior. RFC 1928 defines this field as the client's expected UDP source, so
'stun' is a diagnostic comparison, not the normal protocol interpretation.
The actual UDP payload always carries the fixed STUN server's IPv4:port.
RFC1929 username/password auth is required. A fresh association per provider
retains its TCP control socket throughout same-transaction UDP retransmission.

The entire invocation has a 15-second budget: work <=13 seconds, remaining time
for logout. Both providers share the work remainder equally; at most three
identical STUN requests each, at roughly 0, 0.5, 1.5 seconds. Deadlines may allow
fewer. OS DNS/HTTPS calls run in bounded daemon workers; no work starts on import.
Cleanup is best-effort: a login response lost/timed out before delivering a token
cannot be logged out; logout_attempted/logout_ok make cleanup evidence explicit.
Exit 0 requires both STUN successes and logout; 1 failure; 2 invalid arguments.

Contracts: Windows/scripts/test-routing-live.py; RFC 1928 sections 6/7,
RFC 1929 section 2; RFC 8489 sections 5/14.2.
"""

import argparse
import binascii
import datetime
import ipaddress
import json
from pathlib import Path
import queue
import select
import socket
import ssl
import struct
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import os


ROOT = Path(__file__).resolve().parents[2]
API = "https://client-api.sunip.vip/api/client/v1"
PROVIDERS = (("stun.cloudflare.com", 3478), ("stun.l.google.com", 19302))
CODES = frozenset("""
OK NOT_RUN ARGUMENTS_INVALID ACCOUNT_INVALID API_FAILED API_CONTRACT_INVALID
API_PAGINATION_INVALID ORDINAL_UNAVAILABLE NODE_PROTOCOL NODE_PROFILE
ENTRY_RESOLUTION_FAILED STUN_RESOLUTION_FAILED BUDGET_EXPIRED INTERNAL_ERROR
METHOD_REJECTED AUTH_REJECTED ASSOCIATE_REJECTED SOCKS_FORMAT SOCKS_IO
SOCKS_TIMEOUT RELAY_REJECTED CONTROL_CLOSED CONTROL_UNEXPECTED UDP_IO
UDP_ENVELOPE_REJECTED STUN_TIMEOUT STUN_MALFORMED STUN_COOKIE STUN_TRANSACTION
STUN_TYPE STUN_RESPONSE_ERROR STUN_NO_IPV4 STUN_FINGERPRINT STUN_NONPUBLIC
PACKET_LIMIT PROBE_FAILED LOGOUT_FAILED INTERRUPTED
""".split())


class Failure(Exception):
    def __init__(self, code):
        self.code = code if code in CODES else "INTERNAL_ERROR"
        super().__init__(self.code)


def require(ok, code):
    if not ok:
        raise Failure(code)


class Budget:
    def __init__(self, end):
        self.end = end

    def left(self, maximum=3.0):
        left = self.end - time.monotonic()
        require(left > 0, "BUDGET_EXPIRED")
        return min(maximum, left)


def bounded(function, budget, maximum, failure_code):
    """Bound even OS DNS/HTTPS stalls; worker exceptions never reach stderr."""
    timeout = budget.left(maximum)
    ready = queue.Queue(maxsize=1)

    def worker():
        try:
            ready.put((True, function()))
        except Failure as error:
            ready.put((False, error.code))
        except BaseException:
            ready.put((False, failure_code))

    threading.Thread(target=worker, daemon=True).start()
    try:
        ok, value = ready.get(timeout=timeout)
    except queue.Empty:
        raise Failure("BUDGET_EXPIRED") from None
    if not ok:
        raise Failure(value)
    return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def identity(value):
    require(type(value) in (str, int) and 0 < len(str(value)) <= 128,
            "API_CONTRACT_INVALID")
    return str(value)


class Api:
    def __init__(self, budget):
        self.budget = budget
        self.token = None
        # Constructed only at invocation, with ordinary certificate/hostname checks.
        self.transport = urllib.request.build_opener(
            urllib.request.ProxyHandler({}), NoRedirect(),
            urllib.request.HTTPSHandler(context=ssl.create_default_context()))

    def request(self, path, body=None, budget=None):
        budget = budget or self.budget
        headers = {"Accept": "application/json", "Content-Type": "application/json",
                   "Connection": "close"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        request = urllib.request.Request(API + path, headers=headers,
            data=None if body is None else json.dumps(body).encode("utf-8"))

        def fetch():
            try:
                with self.transport.open(request, timeout=budget.left(3)) as response:
                    require(response.status == 200, "API_FAILED")
                    data = bytearray()
                    limit = 2 * 1024 * 1024
                    while True:
                        budget.left()
                        chunk = response.read1(min(4096, limit + 1 - len(data)))
                        if not chunk:
                            break
                        data.extend(chunk)
                        require(len(data) <= limit, "API_CONTRACT_INVALID")
                    result = json.loads(data)
                require(isinstance(result, dict) and result.get("success") is True
                        and isinstance(result.get("data"), dict), "API_CONTRACT_INVALID")
                return result["data"]
            except urllib.error.HTTPError as error:
                error.close()  # Never read, log, or interpolate server errors.
                raise Failure("API_FAILED") from None

        return bounded(fetch, budget, 5, "API_FAILED")

    def login(self):
        try:
            with (ROOT / "testaccount.txt").open("r", encoding="utf-8-sig") as source:
                text = source.read(16385)
            require(len(text) <= 16384, "ACCOUNT_INVALID")
            lines = [line for line in text.splitlines() if line.strip()]
            text = None
            require(len(lines) == 2, "ACCOUNT_INVALID")
        except (OSError, UnicodeError):
            raise Failure("ACCOUNT_INVALID") from None
        try:
            data = self.request("/auth/login", {"username": lines[0].strip(),
                "password": lines[1], "device_id": str(uuid.uuid4()),
                "device_name": "BlueGrass UDP diagnostic"})
        finally:
            lines.clear()
        token = data.get("access_token")
        require(isinstance(token, str) and 0 < len(token) <= 16384
                and "\r" not in token and "\n" not in token, "API_CONTRACT_INVALID")
        self.token = token  # Set before further validation so finally can log out.
        customer = data.get("customer")
        require(isinstance(customer, dict), "API_CONTRACT_INVALID")
        identity(customer.get("id"))

    def nodes(self):
        result, seen, cursors = [], set(), set()
        cursor = None
        for _ in range(100):
            query = {"per_page": 100}
            if cursor is not None:
                query["cursor"] = cursor
            page = self.request("/nodes?" + urllib.parse.urlencode(query))
            items, pagination = page.get("items"), page.get("pagination")
            require(isinstance(items, list) and len(items) <= 100
                    and isinstance(pagination, dict), "API_PAGINATION_INVALID")
            for item in items:
                require(isinstance(item, dict), "API_CONTRACT_INVALID")
                key = identity(item.get("id"))
                require(key not in seen, "API_PAGINATION_INVALID")
                seen.add(key)
                result.append(item)
            more = pagination.get("has_more")
            require(type(more) is bool, "API_PAGINATION_INVALID")
            if not more:
                return result
            cursor = pagination.get("next_cursor")
            require(isinstance(cursor, str) and 0 < len(cursor) <= 4096
                    and cursor not in cursors, "API_PAGINATION_INVALID")
            cursors.add(cursor)
        raise Failure("API_PAGINATION_INVALID")

    def close(self, budget):
        if self.token is None:
            return False, False
        try:
            self.request("/auth/logout", {}, budget)
            return True, True
        except BaseException:
            return True, False
        finally:
            self.token = None


def profile(node):
    require(isinstance(node, dict), "NODE_PROFILE")
    require(node.get("protocol") == "socks5", "NODE_PROTOCOL")
    host, port = node.get("host"), node.get("port")
    require(isinstance(host, str) and 0 < len(host) <= 253
            and not any(c in host for c in "/\\:@ \r\n\t\x00"), "NODE_PROFILE")
    require(type(port) is int and 1 <= port <= 65535, "NODE_PROFILE")
    try:
        username, password = node["username"].encode("utf-8"), node["password"].encode("utf-8")
        require(1 <= len(username) <= 255 and 1 <= len(password) <= 255, "NODE_PROFILE")
        require("expires_at" in node, "NODE_PROFILE")
        if node["expires_at"] is not None:
            expires = datetime.datetime.fromisoformat(node["expires_at"].replace("Z", "+00:00"))
            require(expires.tzinfo is not None
                    and expires > datetime.datetime.now(datetime.timezone.utc), "NODE_PROFILE")
    except (KeyError, AttributeError, TypeError, ValueError, UnicodeError):
        raise Failure("NODE_PROFILE") from None
    return host, port, username, password


def public4(value):
    try:
        ip = ipaddress.ip_address(value)
        return (ip.version == 4 and ip.is_global and not ip.is_multicast
                and not ip.is_reserved and not ip.is_loopback
                and not any(ip in ipaddress.ip_network(prefix) for prefix in (
                    "0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
                    "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/3")))
    except (ValueError, TypeError):
        return False


def resolve_public4(host, budget, code):
    addresses = bounded(lambda: socket.getaddrinfo(host, None, socket.AF_INET,
                                                  socket.SOCK_DGRAM), budget, 2, code)
    candidates = []
    for item in addresses:
        address = item[4][0]
        if public4(address) and address not in candidates:
            candidates.append(address)
    require(bool(candidates), code)
    return tuple(candidates)


def resolve4(host, budget, code):
    return resolve_public4(host, budget, code)[0]


def exact(connection, size, budget):
    data = bytearray()
    while len(data) < size:
        connection.settimeout(budget.left())
        chunk = connection.recv(size - len(data))
        require(bool(chunk), "CONTROL_CLOSED")
        data.extend(chunk)
    return bytes(data)


def send(connection, packet, budget):
    connection.settimeout(budget.left())
    connection.sendall(packet)


def associate(connection, entry, username, password, destination, budget, result,
              entry_candidates, allow_public_relay):
    send(connection, b"\x05\x01\x02", budget)
    require(exact(connection, 2, budget) == b"\x05\x02", "METHOD_REJECTED")
    result["method_ok"] = True
    send(connection, b"\x01" + bytes([len(username)]) + username
         + bytes([len(password)]) + password, budget)
    require(exact(connection, 2, budget) == b"\x01\x00", "AUTH_REJECTED")
    result["auth_ok"] = True
    send(connection, b"\x05\x03\x00\x01" + socket.inet_aton(destination[0])
         + struct.pack("!H", destination[1]), budget)
    version, reply, reserved, atyp = exact(connection, 4, budget)
    result["associate_reply_code"] = reply
    result["relay_atyp"] = atyp
    require(version == 5 and reserved == 0, "SOCKS_FORMAT")
    require(reply == 0, "ASSOCIATE_REJECTED")
    size = 4 if atyp == 1 else 16 if atyp == 4 else exact(connection, 1, budget)[0] if atyp == 3 else 0
    require(size > 0, "SOCKS_FORMAT")
    address = exact(connection, size, budget)
    port = struct.unpack("!H", exact(connection, 2, budget))[0]
    result["associate_ok"] = True
    result["relay_port_nonzero"] = port != 0
    result["relay_same_entry"] = atyp == 1 and address == socket.inet_aton(entry)
    result["relay_unspecified"] = atyp == 1 and address == b"\0\0\0\0"
    if atyp == 1:
        relay_ip = ipaddress.IPv4Address(address)
        result["relay_public_ipv4"] = public4(str(relay_ip))
        result["relay_private"] = relay_ip.is_private
        result["relay_loopback"] = relay_ip.is_loopback
        result["relay_matches_entry_dns"] = str(relay_ip) in entry_candidates
    if allow_public_relay:
        require(result["method_ok"] and result["auth_ok"] and result["associate_ok"]
                and atyp == 1 and result["relay_public_ipv4"] and port != 0,
                "RELAY_REJECTED")
        # Authorize only this fresh authenticated response's literal tuple.
        # Do not substitute the entry, re-resolve a hostname, or reuse authority.
        return str(ipaddress.IPv4Address(address)), port
    require(atyp == 1 and port != 0
            and (result["relay_same_entry"] or result["relay_unspecified"]), "RELAY_REJECTED")
    # Never resolve or connect to arbitrary BND.ADDR, even if it is public.
    return entry, port


def stun_response(packet, transaction):
    if len(packet) < 20:
        return "STUN_MALFORMED"
    kind, length, cookie = struct.unpack("!HHI", packet[:8])
    if length % 4 or len(packet) != 20 + length:
        return "STUN_MALFORMED"
    if cookie != 0x2112A442:
        return "STUN_COOKIE"
    if packet[8:20] != transaction:
        return "STUN_TRANSACTION"
    if kind == 0x0111:
        return "STUN_RESPONSE_ERROR"
    if kind != 0x0101:
        return "STUN_TYPE"
    offset, found = 20, False
    while offset < len(packet):
        start = offset
        if offset + 4 > len(packet):
            return "STUN_MALFORMED"
        kind, length = struct.unpack("!HH", packet[offset:offset + 4])
        offset += 4
        padded = (length + 3) & ~3
        if offset + padded > len(packet):
            return "STUN_MALFORMED"
        value = packet[offset:offset + length]
        if kind == 0x0020:
            if length < 4 or (value[1] == 1 and length != 8) or (value[1] == 2 and length != 20) or value[1] not in (1, 2):
                return "STUN_MALFORMED"
            if value[1] == 1 and not found:
                port = struct.unpack("!H", value[2:4])[0] ^ 0x2112
                ip = struct.unpack("!I", value[4:8])[0] ^ 0x2112A442
                if not port or not public4(str(ipaddress.IPv4Address(ip))):
                    return "STUN_NONPUBLIC"
                found = True
        if kind == 0x8028:
            if length != 4 or offset + padded != len(packet):
                return "STUN_FINGERPRINT"
            expected = (binascii.crc32(packet[:start]) & 0xffffffff) ^ 0x5354554e
            if struct.unpack("!I", value)[0] != expected:
                return "STUN_FINGERPRINT"
        offset += padded
    return "OK" if found else "STUN_NO_IPV4"


def probe_udp(control, relay, target, budget, result):
    transaction = os.urandom(12)
    stun = struct.pack("!HHI", 1, 0, 0x2112A442) + transaction
    envelope = b"\0\0\0\x01" + socket.inet_aton(target[0]) + struct.pack("!H", target[1])
    packet = envelope + stun
    last_code = "STUN_TIMEOUT"
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as udp:
        udp.settimeout(budget.left())
        udp.connect(relay)  # Already validated under the explicitly selected policy.
        for attempt in range(3):
            udp.settimeout(budget.left())
            require(udp.send(packet) == len(packet), "UDP_IO")
            result["attempts"] += 1
            until = budget.end if attempt == 2 else min(budget.end, time.monotonic() + 0.5 * (2 ** attempt))
            while time.monotonic() < until:
                ready, _, _ = select.select([udp, control], [], [], max(0, until - time.monotonic()))
                if control in ready:
                    require(bool(control.recv(1, socket.MSG_PEEK)), "CONTROL_CLOSED")
                    raise Failure("CONTROL_UNEXPECTED")
                if udp not in ready:
                    break
                response = udp.recv(65535)
                result["received"] += 1
                if len(response) < 10 or response[:10] != envelope:
                    last_code = "UDP_ENVELOPE_REJECTED"
                else:
                    last_code = stun_response(response[10:], transaction)
                    if last_code == "OK":
                        result["stun_ok"] = True
                        return "OK"
                    if last_code == "STUN_RESPONSE_ERROR":
                        return last_code
                if result["received"] >= 64:
                    return "PACKET_LIMIT"
            if time.monotonic() >= budget.end:
                break
    return last_code


def new_check(ordinal):
    return {"provider_ordinal": ordinal, "code": "NOT_RUN", "method_ok": False,
            "auth_ok": False, "associate_ok": False, "relay_same_entry": False,
            "relay_unspecified": False, "relay_port_nonzero": False, "relay_atyp": 0,
            "relay_public_ipv4": False, "relay_private": False,
            "relay_loopback": False, "relay_matches_entry_dns": False,
            "stun_ok": False, "attempts": 0, "received": 0}


def check(entry, port, username, password, provider, actual, budget, result,
          entry_candidates, allow_public_relay):
    try:
        target = (resolve4(provider[0], budget, "STUN_RESOLUTION_FAILED"), provider[1])
        destination = target if actual else ("0.0.0.0", 0)
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as control:
            control.settimeout(budget.left())
            control.connect((entry, port))
            relay = associate(control, entry, username, password, destination, budget,
                              result, entry_candidates, allow_public_relay)
            result["code"] = probe_udp(control, relay, target, budget, result)
    except Failure as error:
        result["code"] = error.code
    except (socket.timeout, TimeoutError):
        result["code"] = "STUN_TIMEOUT" if result["attempts"] else "SOCKS_TIMEOUT"
    except OSError:
        result["code"] = "UDP_IO" if result["attempts"] else "SOCKS_IO"


class SafeParser(argparse.ArgumentParser):
    def error(self, message):
        raise Failure("ARGUMENTS_INVALID")


def main(argv=None):
    start = time.monotonic()
    deadline = start + 15.0
    work = Budget(deadline - 2.0)
    result = {"code": "NOT_RUN", "ordinal": 1, "wire_actual_destination": False,
              "allow_authenticated_public_relay": False,
              "login_ok": False, "inventory_count": 0, "logout_attempted": False,
              "logout_ok": False, "stun_success_count": 0,
              "checks": [new_check(1), new_check(2)]}
    api, exit_code = None, 1
    try:
        parser = SafeParser(add_help=False, allow_abbrev=False)
        parser.add_argument("--ordinal", type=int, default=1)
        parser.add_argument("--wire-destination", choices=("unspecified", "stun"), default="unspecified")
        parser.add_argument("--allow-authenticated-public-relay", action="store_true")
        args = parser.parse_args(argv)
        require(1 <= args.ordinal <= 10000, "ARGUMENTS_INVALID")
        result["ordinal"] = args.ordinal
        result["wire_actual_destination"] = args.wire_destination == "stun"
        result["allow_authenticated_public_relay"] = args.allow_authenticated_public_relay
        api = Api(work)
        api.login()
        result["login_ok"] = True
        nodes = api.nodes()
        result["inventory_count"] = len(nodes)
        require(args.ordinal <= len(nodes), "ORDINAL_UNAVAILABLE")
        selected = identity(nodes[args.ordinal - 1].get("id"))
        node = api.request("/nodes/" + urllib.parse.quote(selected, safe=""))
        require(identity(node.get("id")) == selected, "API_CONTRACT_INVALID")
        host, port, username, password = profile(node)
        nodes.clear()
        node.clear()
        entry_candidates = resolve_public4(host, work, "ENTRY_RESOLUTION_FAILED")
        entry = entry_candidates[0]
        try:
            for index, provider in enumerate(PROVIDERS):
                remaining = work.left(15)
                budget = Budget(min(work.end, time.monotonic() + remaining / (2 - index)))
                check(entry, port, username, password, provider,
                      result["wire_actual_destination"], budget, result["checks"][index],
                      entry_candidates, args.allow_authenticated_public_relay)
        finally:
            username = password = None
        result["stun_success_count"] = sum(item["stun_ok"] for item in result["checks"])
        result["code"] = "OK" if result["stun_success_count"] == 2 else "PROBE_FAILED"
        exit_code = 0 if result["code"] == "OK" else 1
    except Failure as error:
        result["code"] = error.code
        exit_code = 2 if error.code == "ARGUMENTS_INVALID" else 1
    except KeyboardInterrupt:
        result["code"] = "INTERRUPTED"
    except BaseException:
        result["code"] = "INTERNAL_ERROR"
    finally:
        if api is not None:
            result["logout_attempted"], result["logout_ok"] = api.close(Budget(deadline))
        result["stun_success_count"] = sum(item["stun_ok"] for item in result["checks"])
        if exit_code == 0 and not result["logout_ok"]:
            result["code"], exit_code = "LOGOUT_FAILED", 1
    # This is the sole output boundary: only fixed code strings and int/bool data.
    require(result["code"] in CODES and all(item["code"] in CODES for item in result["checks"]), "INTERNAL_ERROR")
    print(json.dumps(result, separators=(",", ":")), flush=True)
    return exit_code


if __name__ == "__main__":
    try:
        sys.exit(main())
    except SystemExit:
        raise
    except BaseException:
        # Never expose a traceback, arguments, remote fields, or exceptions.
        print('{"code":"INTERNAL_ERROR"}', flush=True)
        sys.exit(1)
