#define SECURITY_WIN32
#include "control_http_internal.h"
#include <security.h>
#include <schannel.h>
#include <wincrypt.h>
#include <algorithm>
#include <cstring>

namespace bluegrass::control_http {
bool ValidServerChain(PCCERT_CHAIN_CONTEXT chain, const std::wstring& host) {
  if (!chain) return false;
  SSL_EXTRA_CERT_CHAIN_POLICY_PARA ssl{};
  ssl.cbSize = sizeof(ssl); ssl.dwAuthType = AUTHTYPE_SERVER;
  ssl.pwszServerName = const_cast<wchar_t*>(host.c_str());
  CERT_CHAIN_POLICY_PARA policy{};
  policy.cbSize = sizeof(policy); policy.pvExtraPolicyPara = &ssl;
  CERT_CHAIN_POLICY_STATUS status{}; status.cbSize = sizeof(status);
  return CertVerifyCertificateChainPolicy(CERT_CHAIN_POLICY_SSL, chain, &policy, &status) != FALSE && status.dwError == 0;
}
namespace {
struct Winsock {
  Winsock() { WSADATA data{}; if (WSAStartup(MAKEWORD(2,2), &data)) throw Failure("network_error"); }
  ~Winsock() { WSACleanup(); }
};
struct SocketOwner {
  SOCKET value = INVALID_SOCKET;
  ~SocketOwner() { if (value != INVALID_SOCKET) closesocket(value); }
};
struct Credentials {
  CredHandle handle;
  Credentials() {
    SecInvalidateHandle(&handle);
    SCHANNEL_CRED options{};
    options.dwVersion = SCHANNEL_CRED_VERSION;
    // TLS 1.2 is available across supported Windows 10 builds. The OS cipher
    // policy still applies. No downgrade to older TLS, automatic client cert,
    // online certificate retrieval or implicit credential use is permitted.
    options.grbitEnabledProtocols = SP_PROT_TLS1_2_CLIENT;
    options.dwFlags = SCH_CRED_MANUAL_CRED_VALIDATION | SCH_CRED_NO_DEFAULT_CREDS |
        SCH_CRED_NO_SYSTEM_MAPPER | SCH_USE_STRONG_CRYPTO;
    TimeStamp expiry{};
    if (AcquireCredentialsHandleW(nullptr, const_cast<wchar_t*>(UNISP_NAME_W), SECPKG_CRED_OUTBOUND, nullptr,
        &options, nullptr, nullptr, &handle, &expiry) != SEC_E_OK) throw Failure("tls_failed");
  }
  ~Credentials() { if (SecIsValidHandle(&handle)) FreeCredentialsHandle(&handle); }
};
void ValidateCertificate(CtxtHandle* context, const Deadline& deadline, const std::wstring& host) {
  deadline.Check();
  PCCERT_CONTEXT cert = nullptr;
  if (QueryContextAttributesW(context, SECPKG_ATTR_REMOTE_CERT_CONTEXT, &cert) != SEC_E_OK || !cert) throw Failure("tls_failed");
  PCCERT_CHAIN_CONTEXT chain = nullptr;
  CERT_CHAIN_PARA options{};
  options.cbSize = sizeof(options);
  LPSTR usage = const_cast<LPSTR>(szOID_PKIX_KP_SERVER_AUTH);
  options.RequestedUsage.dwType = USAGE_MATCH_TYPE_AND;
  options.RequestedUsage.Usage.cUsageIdentifier = 1;
  options.RequestedUsage.Usage.rgpszUsageIdentifier = &usage;
  // No AIA/CTL/root auto-update fetch can escape through system DNS or an
  // unbound certificate socket. Missing local trust/intermediates fail closed.
  // This verifies chain/time/EKU/name; it does not claim online revocation.
  const DWORD flags = CERT_CHAIN_CACHE_ONLY_URL_RETRIEVAL | CERT_CHAIN_DISABLE_AUTH_ROOT_AUTO_UPDATE |
      CERT_CHAIN_REVOCATION_CHECK_CACHE_ONLY;
  bool ok = CertGetCertificateChain(nullptr, cert, nullptr, cert->hCertStore, &options, flags, nullptr, &chain) != FALSE;
  if (ok) ok = ValidServerChain(chain, host);
  if (chain) CertFreeCertificateChain(chain);
  CertFreeCertificateContext(cert);
  deadline.Check();
  if (!ok) throw Failure("tls_failed");
}
class TlsStream {
 public:
  TlsStream(SOCKET socket, const Deadline& deadline, const std::string& host)
      : socket_(socket), deadline_(deadline), host_(host.begin(), host.end()) { SecInvalidateHandle(&context_); }
  ~TlsStream() {
    if (SecIsValidHandle(&context_)) DeleteSecurityContext(&context_);
    if (!encrypted_.empty()) SecureZeroMemory(encrypted_.data(), encrypted_.size());
  }
  void Handshake() {
    constexpr ULONG requested = ISC_REQ_SEQUENCE_DETECT | ISC_REQ_REPLAY_DETECT | ISC_REQ_CONFIDENTIALITY |
        ISC_REQ_ALLOCATE_MEMORY | ISC_REQ_STREAM;
    bool first = true;
    for (size_t round = 0; round < 256; ++round) {
      deadline_.Check();
      if (!first && encrypted_.empty()) Receive();
      SecBuffer input[2]{{static_cast<ULONG>(encrypted_.size()), SECBUFFER_TOKEN, encrypted_.data()}, {0, SECBUFFER_EMPTY, nullptr}};
      SecBufferDesc in{SECBUFFER_VERSION, 2, input};
      SecBuffer output{0, SECBUFFER_TOKEN, nullptr};
      SecBufferDesc out{SECBUFFER_VERSION, 1, &output};
      ULONG attributes = 0; TimeStamp expiry{};
      auto status = InitializeSecurityContextW(&credentials_.handle, first ? nullptr : &context_, host_.data(),
          requested, 0, SECURITY_NATIVE_DREP, first ? nullptr : &in, 0, &context_, &out, &attributes, &expiry);
      first = false;
      try {
        if (status >= 0 && output.cbBuffer) SendRaw(static_cast<const char*>(output.pvBuffer), output.cbBuffer);
      } catch (...) { if (output.pvBuffer) FreeContextBuffer(output.pvBuffer); throw; }
      if (output.pvBuffer) FreeContextBuffer(output.pvBuffer);
      if (status == SEC_E_INCOMPLETE_MESSAGE) { Receive(); continue; }
      if (status != SEC_E_OK && status != SEC_I_CONTINUE_NEEDED) throw Failure("tls_failed");
      RetainExtra(input, 2);
      if (status == SEC_E_OK) {
        if (!(attributes & ISC_RET_CONFIDENTIALITY) || !(attributes & ISC_RET_STREAM)) throw Failure("tls_failed");
        ValidateCertificate(&context_, deadline_, host_);
        if (QueryContextAttributesW(&context_, SECPKG_ATTR_STREAM_SIZES, &sizes_) != SEC_E_OK ||
            sizes_.cbMaximumMessage == 0 || sizes_.cbMaximumMessage > 65536 || sizes_.cbHeader > 65536 || sizes_.cbTrailer > 65536) throw Failure("tls_failed");
        return;
      }
    }
    throw Failure("tls_failed");
  }
  void Write(const std::string& text) {
    for (size_t offset = 0; offset < text.size();) {
      deadline_.Check();
      const size_t count = std::min(text.size() - offset, static_cast<size_t>(sizes_.cbMaximumMessage));
      std::vector<char> record(sizes_.cbHeader + count + sizes_.cbTrailer);
      std::memcpy(record.data() + sizes_.cbHeader, text.data() + offset, count);
      SecBuffer buffers[4]{{sizes_.cbHeader, SECBUFFER_STREAM_HEADER, record.data()},
          {static_cast<ULONG>(count), SECBUFFER_DATA, record.data() + sizes_.cbHeader},
          {sizes_.cbTrailer, SECBUFFER_STREAM_TRAILER, record.data() + sizes_.cbHeader + count}, {0, SECBUFFER_EMPTY, nullptr}};
      SecBufferDesc desc{SECBUFFER_VERSION, 4, buffers};
      if (EncryptMessage(&context_, 0, &desc, 0) != SEC_E_OK) {
        SecureZeroMemory(record.data(), record.size()); throw Failure("tls_failed");
      }
      SendRaw(record.data(), static_cast<size_t>(buffers[0].cbBuffer) + buffers[1].cbBuffer + buffers[2].cbBuffer);
      offset += count;
    }
  }
  Response ReadResponse() {
    HttpResponseParser parser;
    while (!parser.complete()) {
      deadline_.Check();
      if (encrypted_.empty()) Receive();
      SecBuffer buffers[4]{{static_cast<ULONG>(encrypted_.size()), SECBUFFER_DATA, encrypted_.data()},
          {0, SECBUFFER_EMPTY, nullptr}, {0, SECBUFFER_EMPTY, nullptr}, {0, SECBUFFER_EMPTY, nullptr}};
      SecBufferDesc desc{SECBUFFER_VERSION, 4, buffers};
      const auto status = DecryptMessage(&context_, &desc, 0, nullptr);
      if (status == SEC_E_INCOMPLETE_MESSAGE) { Receive(); continue; }
      if (status == SEC_I_CONTEXT_EXPIRED) { parser.End(); break; }
      // Refuse TLS renegotiation/client-certificate requests and upgrades.
      if (status != SEC_E_OK) throw Failure("tls_failed");
      for (const auto& b : buffers) if (b.BufferType == SECBUFFER_DATA && b.cbBuffer) parser.Feed(std::string_view(static_cast<const char*>(b.pvBuffer), b.cbBuffer));
      RetainExtra(buffers, 4);
    }
    deadline_.Check();
    return parser.response();
  }
 private:
  void SendRaw(const char* data, size_t length) {
    for (size_t offset = 0; offset < length;) {
      WaitSocket(socket_, true, deadline_);
      const int n = send(socket_, data + offset, static_cast<int>(length - offset), 0);
      if (n < 0 && WSAGetLastError() == WSAEWOULDBLOCK) continue;
      if (n <= 0) throw Failure("network_error");
      offset += static_cast<size_t>(n);
    }
  }
  void Receive() {
    for (;;) {
      WaitSocket(socket_, false, deadline_);
      char data[16384];
      const auto n = recv(socket_, data, sizeof(data), 0);
      if (n < 0 && WSAGetLastError() == WSAEWOULDBLOCK) continue;
      // Raw EOF is not a TLS close_notify: never treat truncated streams as
      // valid close-delimited HTTP responses.
      if (n <= 0) throw Failure("network_error");
      received_ += static_cast<size_t>(n);
      if (encrypted_.size() + static_cast<size_t>(n) > 256 * 1024 || received_ > 4 * 1024 * 1024) throw Failure("response_too_large");
      encrypted_.insert(encrypted_.end(), data, data + n);
      return;
    }
  }
  void RetainExtra(SecBuffer* buffers, size_t count) {
    size_t extra = 0;
    for (size_t i = 0; i < count; ++i) if (buffers[i].BufferType == SECBUFFER_EXTRA) extra = buffers[i].cbBuffer;
    if (extra > encrypted_.size()) throw Failure("tls_failed");
    std::vector<char> rest(encrypted_.end() - extra, encrypted_.end());
    if (!encrypted_.empty()) SecureZeroMemory(encrypted_.data(), encrypted_.size());
    encrypted_.swap(rest);
  }
  SOCKET socket_;
  const Deadline& deadline_;
  std::wstring host_;
  Credentials credentials_;
  CtxtHandle context_;
  SecPkgContext_StreamSizes sizes_{};
  std::vector<char> encrypted_;
  size_t received_ = 0;
};
struct SensitiveRequest {
  std::string text;
  ~SensitiveRequest() { if (!text.empty()) SecureZeroMemory(text.data(), text.size()); }
};
}
Response Perform(const Request& r, const std::atomic_bool& cancelled) noexcept {
  try {
    const Deadline deadline{std::chrono::steady_clock::now() + std::chrono::milliseconds(r.timeout_ms), cancelled};
    ValidateRequest(r);
    deadline.Check();
    Winsock startup;
    const auto route = PhysicalRoute(ParsePhysicalAddress(r.ip), deadline);
    SocketOwner socket;
    socket.value = ConnectPhysical(route, deadline);
    TlsStream stream(socket.value, deadline, r.host);
    stream.Handshake();
    // Never put credentials on a socket whose physical route changed while
    // certificates were checked. The socket remains pinned throughout.
    if (!(PhysicalRoute(route.destination, deadline) == route)) throw Failure("physical_route_unavailable");
    SensitiveRequest plaintext{SerializeRequest(r)};
    stream.Write(plaintext.text);
    auto response = stream.ReadResponse();
    if (!(PhysicalRoute(route.destination, deadline) == route)) throw Failure("physical_route_unavailable");
    return response;
  } catch (const Failure& failure) {
    Response response; response.error = failure.what(); return response;
  } catch (...) {
    Response response; response.error = "network_error"; return response;
  }
}
}
