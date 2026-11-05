#ifndef BLUEGRASS_CONTROL_HTTP_H_
#define BLUEGRASS_CONTROL_HTTP_H_
#include <atomic>
#include <cstdint>
#include <map>
#include <string>
#include <vector>

namespace bluegrass::control_http {
inline constexpr size_t kMaxBody = 2 * 1024 * 1024;
inline constexpr wchar_t kTlsHost[] = L"client-api.sunip.vip";
struct Request {
  std::string host = "client-api.sunip.vip";
  std::string ip, target, method;
  std::map<std::string, std::string> headers;
  std::vector<uint8_t> body;
  int timeout_ms = 20000;
};
struct Response {
  int status = 0;
  std::map<std::string, std::string> headers;
  std::vector<uint8_t> body;
  std::string error; // Fixed codes only. No raw OS/TLS/HTTP errors.
};
// Synchronous worker-only entry. Cancellation is polled during every socket
// wait (<=50 ms); this never creates a listener or changes system networking.
Response Perform(const Request& request, const std::atomic_bool& cancelled) noexcept;
bool ValidApiHost(const std::string& host);
}
#endif
