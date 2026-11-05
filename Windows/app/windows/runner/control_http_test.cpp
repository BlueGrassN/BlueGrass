// Standalone native tests: no Flutter, network, route or registry mutations.
#include "control_http_internal.h"
#include <iostream>
#include <stdexcept>
#include <thread>
#include <ncrypt.h>

using namespace bluegrass::control_http;
void Assert(bool ok, int line) {
  if (!ok) { std::cerr << "assertion_line=" << line << "\n"; throw std::runtime_error("test_failed"); }
}
#define Check(ok) Assert((ok), __LINE__)
template<class F> void Reject(F f) {
  bool rejected = false;
  try { f(); } catch (const Failure&) { rejected = true; }
  Check(rejected);
}
// Real Windows chain/name policy against an isolated in-memory trust engine.
// A null CNG key name creates an ephemeral key: no OS trust store/key writes.
void CertificateIdentityTest(const wchar_t* name, bool expected, const wchar_t* host = kTlsHost) {
  NCRYPT_PROV_HANDLE provider = 0;
  NCRYPT_KEY_HANDLE key = 0;
  Check(NCryptOpenStorageProvider(&provider, MS_KEY_STORAGE_PROVIDER, 0) == ERROR_SUCCESS);
  Check(NCryptCreatePersistedKey(provider, &key, NCRYPT_RSA_ALGORITHM, nullptr, 0, 0) == ERROR_SUCCESS);
  Check(NCryptFinalizeKey(key, 0) == ERROR_SUCCESS);
  DWORD size = 0;
  const std::wstring subject = std::wstring(L"CN=") + name;
  Check(CertStrToNameW(X509_ASN_ENCODING, subject.c_str(), CERT_X500_NAME_STR, nullptr, nullptr, &size, nullptr) != FALSE);
  std::vector<BYTE> encoded(size);
  Check(CertStrToNameW(X509_ASN_ENCODING, subject.c_str(), CERT_X500_NAME_STR, nullptr, encoded.data(), &size, nullptr) != FALSE);
  CERT_NAME_BLOB blob{size, encoded.data()};
  CRYPT_ALGORITHM_IDENTIFIER signature{};
  signature.pszObjId = const_cast<LPSTR>(szOID_RSA_SHA256RSA);
  const auto cert = CertCreateSelfSignCertificate(key, &blob, CERT_CREATE_SELFSIGN_NO_KEY_INFO,
      nullptr, &signature, nullptr, nullptr, nullptr);
  Check(cert != nullptr);
  const auto store = CertOpenStore(CERT_STORE_PROV_MEMORY, 0, 0, CERT_STORE_CREATE_NEW_FLAG, nullptr);
  Check(store != nullptr);
  Check(CertAddCertificateContextToStore(store, cert, CERT_STORE_ADD_ALWAYS, nullptr) != FALSE);
  CERT_CHAIN_ENGINE_CONFIG config{};
  config.cbSize = sizeof(config); config.hExclusiveRoot = store;
  HCERTCHAINENGINE engine = nullptr;
  Check(CertCreateCertificateChainEngine(&config, &engine) != FALSE);
  CERT_CHAIN_PARA para{}; para.cbSize = sizeof(para);
  PCCERT_CHAIN_CONTEXT chain = nullptr;
  const auto flags = CERT_CHAIN_CACHE_ONLY_URL_RETRIEVAL | CERT_CHAIN_DISABLE_AUTH_ROOT_AUTO_UPDATE;
  Check(CertGetCertificateChain(engine, cert, nullptr, store, &para, flags, nullptr, &chain) != FALSE);
  Check(ValidServerChain(chain, host) == expected);
  CertFreeCertificateChain(chain);
  chain = nullptr;
  // The same synthetic cert must not become trusted by the real system engine.
  Check(CertGetCertificateChain(nullptr, cert, nullptr, store, &para, flags, nullptr, &chain) != FALSE);
  Check(!ValidServerChain(chain));
  CertFreeCertificateChain(chain);
  CertFreeCertificateChainEngine(engine);
  CertCloseStore(store, 0); CertFreeCertificateContext(cert);
  NCryptFreeObject(key); NCryptFreeObject(provider);
}
int main(int argc, char** argv) {
  try {
    if (argc == 3 && std::string(argv[1]) == "--smoke") {
      Request request;
      request.ip = argv[2]; request.method = "GET"; request.target = "/api/client/v1/health";
      request.headers["accept"] = "application/json";
      const std::atomic_bool cancelled{false};
      const auto r = Perform(request, cancelled);
      // Never print addresses, headers or bodies (even in this standalone
      // health-only harness). A successful result uses the production stack.
      std::cout << "health_status=" << r.status << " body_bytes=" << r.body.size() << " error=" << r.error << "\n";
      return r.error.empty() && r.status == 200 ? 0 : 1;
    }
    {
      Request request;
      request.ip = "1.1.1.1"; request.method = "GET"; request.target = "/api/client/v1/health";
      const auto wire = SerializeRequest(request);
      Check(wire == "GET /api/client/v1/health HTTP/1.1\r\nHost: client-api.sunip.vip\r\nConnection: close\r\nAccept-Encoding: identity\r\nContent-Length: 0\r\n\r\n");
    }
    CertificateIdentityTest(L"client-api.sunip.vip", true);
    CertificateIdentityTest(L"api-next.sunip.vip", true, L"api-next.sunip.vip");
    CertificateIdentityTest(L"client-api.sunip.vip", false, L"api-next.sunip.vip");
    CertificateIdentityTest(L"client-api.sunip.cc", false);
    {
      Request request;
      request.ip = "1.1.1.1"; request.method = "GET"; request.target = "/api/client/v1/health";
      request.host = "api-next.sunip.vip";
      Check(SerializeRequest(request).find("Host: api-next.sunip.vip\r\n") != std::string::npos);
      for (const auto* host : {"127.0.0.1", "localhost", "api.local", "https://api.sunip.vip", "api.sunip.vip:443", "api.sunip.vip\r\nX: y", "api..sunip.vip"}) {
        request.host = host;
        Reject([&] { ValidateRequest(request); });
      }
    }
    // A still-trusted certificate for the former environment is not the
    // production TLS identity; keep this explicit migration regression.
    CertificateIdentityTest(L"test-api.dos.im", false);
    CertificateIdentityTest(L"wrong.invalid", false);
    {
      Request request;
      request.ip = "1.1.1.1"; request.method = "GET"; request.target = "/api/client/v1/health";
      const std::atomic_bool cancelled{true};
      Check(Perform(request, cancelled).error == "cancelled");
      const std::atomic_bool running{false};
      const Deadline expired{std::chrono::steady_clock::now() - std::chrono::seconds(1), running};
      Reject([&] { (expired.Check)(); });
      Check(!ValidServerChain(nullptr));
    }
    {
      // Interrupt a real pending readiness wait without remote traffic.
      WSADATA startup{}; Check(WSAStartup(MAKEWORD(2, 2), &startup) == 0);
      const auto socket = ::socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
      Check(socket != INVALID_SOCKET);
      sockaddr_in address{}; address.sin_family = AF_INET; address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
      Check(bind(socket, reinterpret_cast<sockaddr*>(&address), sizeof(address)) == 0);
      std::atomic_bool cancelled{false};
      const auto start = std::chrono::steady_clock::now();
      const Deadline deadline{start + std::chrono::seconds(2), cancelled};
      std::thread stop([&] { std::this_thread::sleep_for(std::chrono::milliseconds(10)); cancelled.store(true); });
      std::string code;
      try { WaitSocket(socket, false, deadline); } catch (const Failure& e) { code = e.what(); }
      stop.join(); closesocket(socket); WSACleanup();
      Check(code == "cancelled");
      Check(std::chrono::steady_clock::now() - start < std::chrono::seconds(1));
    }
    {
      HttpResponseParser p;
      const std::string wire = "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Type: application/json\r\n\r\n{}";
      for (char c : wire) p.Feed(std::string(1, c));
      Check(p.complete());
      Check(p.response().status == 200 && p.response().body == std::vector<uint8_t>({'{', '}'}));
    }
    {
      HttpResponseParser p;
      p.Feed("HTTP/1.1 429 Busy\r\nTransfer-Encoding: chunked\r\nRetry-After: 12\r\n\r\n1\r\n{\r\n1\r\n}\r\n0\r\n\r\n");
      Check(p.complete() && p.response().headers.at("retry-after") == "12");
      Check(p.response().body.size() == 2);
    }
    {
      HttpResponseParser p;
      p.Feed("HTTP/1.1 302 Found\r\nContent-Length: 0\r\nLocation: https://untrusted.invalid/\r\n\r\n");
      Check(p.complete() && p.response().status == 302);
      Check(p.response().headers.count("location") == 0);
    }
    for (const auto* s : {
        "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nContent-Length: 3\r\n\r\n{}",
        "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nTransfer-Encoding: chunked\r\n\r\n",
        "HTTP/1.1 200 OK\r\nContent-Length: 2097153\r\n\r\n",
        "HTTP/1.1 200 OK\r\nTransfer-Encoding: gzip\r\n\r\n",
        "HTTP/1.1 200 OK\r\nContent-Encoding: gzip\r\n\r\n",
        "HTTP/1.1 200 OK\r\n bad-fold: x\r\n\r\n",
        "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n200001\r\n",
        "HTTP/1.1 200 OK\r\nContent-Length: -1\r\n\r\n",
        "HTTP/1.1 200 OK\r\nContent-Length: 1\r\n\r\nxx"}) {
      Reject([&] { HttpResponseParser p; p.Feed(s); });
    }
    Reject([] { HttpResponseParser p; p.Feed("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nx"); p.End(); });
    Reject([] { HttpResponseParser p; p.Feed("HTTP/1.1 200 OK\r\nX: " + std::string(32768, 'a')); });
    {
      HttpResponseParser p;
      p.Feed("HTTP/1.1 200 OK\r\n\r\n{}");
      Check(!p.complete());
      p.End();
      Check(p.complete());
    }
    {
      Route a, b;
      a.index = 1; a.luid = 10; a.prefix_bits = 0; a.metric = 25;
      b.index = 2; b.luid = 20; b.prefix_bits = 1; b.metric = 50;
      Check(SelectRoute({a, b}, 99).index == 2); // TUN cannot choose a physical adapter.
      Reject([&] { SelectRoute({a, b}, 1); }); // Contradictory physical OS best.
      b.prefix_bits = 0; b.metric = 25;
      Reject([&] { SelectRoute({a, b}, 99); }); // No arbitrary tie fallback.
      Check(SelectRoute({a, b}, 1).index == 1);
    }
    for (const auto* ip : {"127.0.0.1", "198.18.0.5", "::1", "fc00::1", "::ffff:1.1.1.1", "0.0.0.0", "172.19.0.1", "client-api.sunip.vip", "fe80::1%2"}) {
      Reject([&] { ParsePhysicalAddress(ip); });
    }
    ParsePhysicalAddress("1.1.1.1");
    ParsePhysicalAddress("2606:4700:4700::1111");
    Request r; r.ip = "1.1.1.1"; r.target = "/api/client/v1/health"; r.method = "GET"; r.timeout_ms = 20000;
    ValidateRequest(r);
    r.headers["authorization"] = "x\r\nInjected: 1";
    Reject([&] { ValidateRequest(r); });
    r.headers.clear(); r.target = "https://other.invalid/";
    Reject([&] { ValidateRequest(r); });
    std::cout << "control_http native tests passed\n";
    return 0;
  } catch (...) { std::cerr << "control_http native tests failed\n"; return 1; }
}
