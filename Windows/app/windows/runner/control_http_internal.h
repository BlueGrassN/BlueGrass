#ifndef BLUEGRASS_CONTROL_HTTP_INTERNAL_H_
#define BLUEGRASS_CONTROL_HTTP_INTERNAL_H_
#include "control_http.h"
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <wincrypt.h>
#include <iphlpapi.h>
#include <chrono>
#include <stdexcept>
#include <string_view>

namespace bluegrass::control_http {
class Failure : public std::runtime_error {
 public:
  explicit Failure(const char* code) : std::runtime_error(code) {}
};
struct Route {
  SOCKADDR_INET destination{}, source{}, prefix{}, next_hop{};
  uint32_t index = 0;
  uint64_t luid = 0, metric = 0;
  GUID guid{};
  uint8_t prefix_bits = 0;
  bool operator==(const Route& other) const;
};
struct Deadline {
  std::chrono::steady_clock::time_point end;
  const std::atomic_bool& cancelled;
  void Check() const;
};
SOCKADDR_INET ParsePhysicalAddress(const std::string& text);
Route SelectRoute(const std::vector<Route>& routes, uint32_t os_best);
Route PhysicalRoute(const SOCKADDR_INET& destination, const Deadline& deadline);
SOCKET ConnectPhysical(const Route& route, const Deadline& deadline);
void WaitSocket(SOCKET socket, bool write, const Deadline& deadline);
void ValidateRequest(const Request& request);
std::string SerializeRequest(const Request& request);
bool ValidServerChain(PCCERT_CHAIN_CONTEXT chain, const std::wstring& host = kTlsHost);

// Incremental bounded HTTP/1.1 framing. No redirects, compression, cookies,
// authentication retry, HTTP upgrades or extra network operations.
class HttpResponseParser {
 public:
  void Feed(std::string_view bytes);
  void End();
  bool complete() const { return complete_; }
  const Response& response() const { return response_; }
 private:
  void Parse();
  void AddBody(std::string_view bytes);
  Response response_;
  std::string buffer_;
  size_t remaining_ = 0, framing_bytes_ = 0;
  bool headers_ = false, complete_ = false, chunked_ = false,
       sized_ = false, chunk_crlf_ = false, trailers_ = false;
};
}
#endif
