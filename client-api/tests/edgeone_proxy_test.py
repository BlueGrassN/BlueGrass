"""Exercise the deployed Nginx maps/proxy against a loopback-only dummy API.

Usage: python tests/edgeone_proxy_test.py --nginx /path/to/nginx
No production endpoint, account or database is used.
The fixture replaces only deployment paths, upstream port and the TLS listener;
the IP selection and rate limits run in real Nginx.
"""

import argparse
import concurrent.futures
import http.client
import http.server
import json
from pathlib import Path
import re
import socket
import subprocess
import tempfile
import threading
import time
import unittest


ROOT = Path(__file__).resolve().parents[1]
EO_HEADER = "EO-Connecting-IP"
ARGS = None


class DummyAPI(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    held = 0
    lock = threading.Lock()
    release = threading.Event()

    def do_GET(self):
        if self.path == "/api/client/v1/hold":
            with self.lock:
                type(self).held += 1
            self.release.wait(8)
        body = json.dumps(dict(self.headers)).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


class EdgeOneProxyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.backend = http.server.ThreadingHTTPServer(("127.0.0.1", 0), DummyAPI)
        cls.thread = threading.Thread(target=cls.backend.serve_forever, daemon=True)
        cls.thread.start()
        cls.addClassCleanup(cls.backend.server_close)
        cls.addClassCleanup(cls.backend.shutdown)
        runtime = ROOT / ".artifacts" / "edgeone-tests"
        runtime.mkdir(parents=True, exist_ok=True)
        cls.temp = tempfile.TemporaryDirectory(prefix="run-", dir=runtime)
        cls.addClassCleanup(cls.temp.cleanup)
        cls.work = Path(cls.temp.name)
        (cls.work / "logs").mkdir()
        (cls.work / "temp").mkdir()
        cls.webroot = cls.work / "www"
        (cls.webroot / "client-config").mkdir(parents=True)
        (cls.webroot / "client-config/config.json").write_text('{"fixture":"public-app-config"}', encoding="utf-8")
        for private in ["config.json", "client-config/private.json", "client-config/config.json.bak"]:
            (cls.webroot / private).write_text("private-local-fixture", encoding="utf-8")
        site = (ROOT / "deploy/client-api.sunip.vip.nginx-site.conf").read_text(encoding="utf-8")
        prefix = re.split(r"(?m)^server\s*$", site, maxsplit=1)[0]
        proxy = (ROOT / "deploy/client-api.sunip.vip.nginx-proxy.conf").read_text(encoding="utf-8")
        proxy = proxy.replace("http://127.0.0.1:8087", f"http://127.0.0.1:{cls.backend.server_port}")
        with socket.socket() as reserved:
            reserved.bind(("127.0.0.1", 0))
            cls.port = reserved.getsockname()[1]
        conf = '''daemon off;
master_process off;
worker_processes 1;
error_log logs/error.log warn;
pid nginx.pid;
events { worker_connections 256; }
http {
''' + prefix + f'''
server {{
    listen 127.0.0.1:{cls.port};
    server_name client-api.sunip.vip;
    root "{cls.webroot.as_posix()}";
    limit_req_status 429;
    limit_conn_status 429;
    access_log logs/access.log client_api_sunip_vip_safe;
    location = /.well-known/acme-challenge/test {{ return 200 "challenge"; }}
''' + proxy + "\n}\n}\n"
        (cls.work / "nginx.conf").write_text(conf, encoding="utf-8")
        cls.command = [str(Path(ARGS.nginx).resolve()), "-p", cls.work.as_posix() + "/", "-c", "nginx.conf"]
        checked = subprocess.run(cls.command + ["-t"], capture_output=True, timeout=10)
        if checked.returncode:
            raise RuntimeError(checked.stderr.decode(errors="replace"))
        cls.output = (cls.work / "process.log").open("wb")
        cls.addClassCleanup(cls.output.close)
        cls.process = subprocess.Popen(cls.command, stdout=cls.output, stderr=cls.output, cwd=cls.work)
        cls.addClassCleanup(cls.stop_nginx)
        for _ in range(100):
            try:
                with socket.create_connection(("127.0.0.1", cls.port), timeout=0.1):
                    break
            except OSError:
                time.sleep(0.05)
        else:
            raise RuntimeError("Local Nginx did not start")

    @classmethod
    def stop_nginx(cls):
        DummyAPI.release.set()
        subprocess.run(cls.command + ["-s", "quit"], capture_output=True, timeout=10)
        try:
            cls.process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            cls.process.terminate()
            cls.process.wait(timeout=5)

    def request(self, headers=(), path="/api/client/v1/health"):
        connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=10)
        try:
            connection.putrequest("GET", path, skip_host=True)
            connection.putheader("Host", "client-api.sunip.vip")
            for name, value in headers:
                connection.putheader(name, value)
            connection.endheaders()
            response = connection.getresponse()
            return response.status, dict(response.getheaders()), response.read()
        finally:
            connection.close()

    def eo_headers(self, ip):
        return [(EO_HEADER, ip)]

    def test_00_site_can_be_saved_before_legacy_proxy_is_replaced(self):
        current = (self.work / "nginx.conf").read_text(encoding="utf-8")
        for legacy_request, legacy_connection in [(True, False), (False, True), (True, True)]:
            with self.subTest(request=legacy_request, connection=legacy_connection):
                mixed = current
                if legacy_request:
                    mixed = mixed.replace("limit_req zone=client_api_sunip_vip_eo_ip ", "limit_req zone=client_api_sunip_vip_ip ")
                if legacy_connection:
                    mixed = mixed.replace("limit_conn client_api_sunip_vip_eo_conn_ip ", "limit_conn client_api_sunip_vip_conn_ip ")
                (self.work / "mixed.conf").write_text(mixed, encoding="utf-8")
                check = subprocess.run(self.command[:-1] + ["mixed.conf", "-t"], capture_output=True, timeout=10)
                self.assertEqual(check.returncode, 0, check.stderr.decode(errors="replace"))

    def test_01_missing_or_empty_eo_header_uses_connection_ip(self):
        for headers in [[], [(EO_HEADER, "")], [("X-Real-IP", "198.51.100.99"), ("X-Forwarded-For", "198.51.100.99")]]:
            with self.subTest(header_names=[h[0] for h in headers]):
                status, _, body = self.request(headers)
                self.assertEqual(status, 200)
                upstream = json.loads(body)
                self.assertEqual(upstream["X-Real-IP"], "127.0.0.1")
                self.assertEqual(upstream["X-Forwarded-For"], "127.0.0.1")

    def test_02_eo_ipv4_ipv6_override_other_ip_headers(self):
        for ip in ["203.0.113.20", "2001:db8::20", "::ffff:192.0.2.20"]:
            with self.subTest(ip=ip):
                status, _, body = self.request(self.eo_headers(ip) + [("X-Real-IP", "198.51.100.99"), ("X-Forwarded-For", "198.51.100.99, 192.0.2.99"), ("Authorization", "Bearer local-fixture")])
                self.assertEqual(status, 200)
                upstream = json.loads(body)
                self.assertEqual(upstream["X-Real-IP"], ip)
                self.assertEqual(upstream["X-Forwarded-For"], ip)
                self.assertEqual(upstream["Authorization"], "Bearer local-fixture")

    def test_04_static_files_blocked_acme_preserved(self):
        for path in ["/config.json", "/client-api", "/.env", "/.git/config", "/"]:
            self.assertEqual(self.request(path=path)[0], 404)
        self.assertEqual(self.request(path="/.well-known/acme-challenge/test")[0], 200)

    def test_03_only_public_app_config_is_served_and_updates_are_visible(self):
        public_file = self.webroot / "client-config/config.json"
        for fixture in ["public-app-config", "updated-app-config"]:
            public_file.write_text(json.dumps({"fixture": fixture}), encoding="utf-8")
            status, headers, body = self.request(path="/client-config/config.json")
            self.assertEqual(status, 200)
            self.assertEqual(headers.get("Content-Type"), "application/json")
            self.assertIn("no-store", headers.get("Cache-Control", ""))
            self.assertEqual(json.loads(body), {"fixture": fixture})
        for path in ["/client-config/", "/client-config/private.json", "/client-config/config.json.bak", "/client-config/config.json/", "/config.json"]:
            status, _, body = self.request(path=path)
            self.assertEqual(status, 404, path)
            self.assertNotIn(b"private-local-fixture", body)
        public_file.unlink()
        self.assertEqual(self.request(path="/client-config/config.json")[0], 404)

    def test_05_connection_limits_separate_clients_at_one_origin(self):
        DummyAPI.release.clear()
        with concurrent.futures.ThreadPoolExecutor(max_workers=16) as executor:
            jobs = [executor.submit(self.request, self.eo_headers("203.0.113.30"), "/api/client/v1/hold") for _ in range(16)]
            try:
                deadline = time.monotonic() + 5
                while DummyAPI.held < 16 and time.monotonic() < deadline:
                    time.sleep(0.02)
                self.assertEqual(DummyAPI.held, 16)
                self.assertEqual(self.request(self.eo_headers("203.0.113.30"))[0], 429)
                self.assertEqual(self.request(self.eo_headers("203.0.113.31"))[0], 200)
            finally:
                DummyAPI.release.set()
            for job in jobs:
                self.assertEqual(job.result()[0], 200)

    def test_06_request_limits_separate_clients_at_one_origin(self):
        statuses = [self.request(self.eo_headers("203.0.113.40"))[0] for _ in range(32)]
        self.assertIn(200, statuses)
        self.assertIn(429, statuses)
        self.assertEqual(self.request(self.eo_headers("203.0.113.41"))[0], 200)

    def test_07_logs_contain_client_and_peer_without_credentials(self):
        self.assertEqual(self.request(self.eo_headers("203.0.113.50"), "/api/client/v1/health?private=local-sensitive-query")[0], 200)
        log = (self.work / "logs/access.log").read_text()
        self.assertIn("203.0.113.50", log)
        self.assertIn("127.0.0.1", log)
        self.assertNotIn("local-sensitive-query", log)
        self.assertNotIn("local-fixture", log)


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--nginx", required=True)
    ARGS, extra = parser.parse_known_args()
    unittest.main(argv=[__file__] + extra, verbosity=2)
