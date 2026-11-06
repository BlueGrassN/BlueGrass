"""Offline regression for API redirects; no real account or socket is used."""
import email.message
import importlib.util
import io
from pathlib import Path
import unittest
import urllib.error
import urllib.request
import urllib.response

spec = importlib.util.spec_from_file_location("live_check", Path(__file__).with_name("live-socks-check.py"))
live = importlib.util.module_from_spec(spec)
spec.loader.exec_module(live)


class RedirectTransport(urllib.request.HTTPSHandler, urllib.request.HTTPHandler):
    def __init__(self, target, status):
        super().__init__()
        self.target, self.status, self.requests = target, status, []

    def https_open(self, request):
        self.requests.append(request.full_url)
        if len(self.requests) != 1:
            raise AssertionError("redirect destination was contacted")
        headers = email.message.Message()
        headers["Location"] = self.target
        response = urllib.response.addinfourl(io.BytesIO(b""), headers, request.full_url, self.status)
        response.msg = "Redirect"
        return response

    def http_open(self, request):
        raise AssertionError("TLS downgrade was attempted")


class ApiRedirectTests(unittest.TestCase):
    def test_authenticated_requests_never_follow_redirects(self):
        origin = "https://client-api.sunip.vip/api/client/v1/nodes"
        for target in ("https://other.invalid/collect", "http://client-api.sunip.vip/collect"):
            for status in (301, 302, 303, 307, 308):
                for method in ("GET", "POST"):
                    with self.subTest(target=target, status=status, method=method):
                        transport = RedirectTransport(target, status)
                        opener = urllib.request.build_opener(
                            urllib.request.ProxyHandler({}), transport, live.NoApiRedirect()
                        )
                        request = urllib.request.Request(
                            origin, method=method,
                            headers={"Authorization": "Bearer SYNTHETIC_TEST_TOKEN"},
                            data=b'{"password":"SYNTHETIC_TEST_PASSWORD"}' if method == "POST" else None,
                        )
                        with self.assertRaises(urllib.error.HTTPError) as caught:
                            opener.open(request)
                        self.assertEqual(caught.exception.code, status)
                        self.assertEqual(transport.requests, [origin])
                        caught.exception.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
