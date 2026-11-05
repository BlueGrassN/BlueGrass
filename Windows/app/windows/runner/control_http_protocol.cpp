#include "control_http_internal.h"
#include <algorithm>
#include <cctype>
#include <limits>
#include <regex>

namespace bluegrass::control_http {
namespace {
bool Visible(std::string_view s) {
  return std::all_of(s.begin(), s.end(), [](unsigned char c) { return c >= 32 && c < 127; });
}
bool Token(std::string_view s) {
  constexpr std::string_view punctuation = "!#$%&'*+-.^_`|~";
  return !s.empty() && std::all_of(s.begin(), s.end(), [&](unsigned char c) {
    return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
        (c >= '0' && c <= '9') || punctuation.find(static_cast<char>(c)) != std::string_view::npos;
  });
}
std::string Lower(std::string s) {
  for (auto& c : s) if (c >= 'A' && c <= 'Z') c = static_cast<char>(c + ('a' - 'A'));
  return s;
}
std::string Trim(std::string s) {
  const auto a = s.find_first_not_of(" \t"), b = s.find_last_not_of(" \t");
  return a == std::string::npos ? "" : s.substr(a, b - a + 1);
}
size_t Number(std::string_view text, unsigned base, size_t limit) {
  if (text.empty()) throw Failure("invalid_response");
  size_t value = 0;
  for (unsigned char c : text) {
    unsigned d = 99;
    if (c >= '0' && c <= '9') d = c - '0';
    if (base == 16 && c >= 'a' && c <= 'f') d = c - 'a' + 10;
    if (base == 16 && c >= 'A' && c <= 'F') d = c - 'A' + 10;
    if (d >= base) throw Failure("invalid_response");
    if (d > limit || value > (limit - d) / base) throw Failure("response_too_large");
    value = value * base + d;
  }
  return value;
}
}
bool ValidApiHost(const std::string& host) {
  if (host.size() > 253) return false;
  static const std::regex pattern("^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}$");
  if (!std::regex_match(host, pattern)) return false;
  const auto suffix = host.substr(host.rfind('.') + 1);
  return suffix != "local" && suffix != "localhost" && suffix != "internal" &&
      suffix != "invalid" && suffix != "test";
}
void ValidateRequest(const Request& r) {
  ParsePhysicalAddress(r.ip);
  const auto q = r.target.find('?');
  const auto path = r.target.substr(0, q);
  if (!ValidApiHost(r.host) || r.timeout_ms <= 0 || r.timeout_ms > 20000 || r.target.size() > 4096 ||
      !Visible(r.target) || r.target.find_first_of(" #\\") != std::string::npos ||
      !(path == "/api/client/v1" || path.rfind("/api/client/v1/", 0) == 0) ||
      (r.method != "GET" && r.method != "POST") ||
      (r.method == "GET" && !r.body.empty()) || r.headers.size() > 3) throw Failure("invalid_request");
  if (r.body.size() > 4096) throw Failure("payload_too_large");
  for (const auto& h : r.headers) {
    if ((h.first != "authorization" && h.first != "accept" && h.first != "content-type") ||
        h.second.size() > 4096 || !Visible(h.second)) throw Failure("invalid_request");
  }
}
std::string SerializeRequest(const Request& r) {
  ValidateRequest(r);
  std::string out = r.method + " " + r.target + " HTTP/1.1\r\nHost: " + r.host + "\r\nConnection: close\r\nAccept-Encoding: identity\r\n";
  for (const auto& h : r.headers) out += h.first + ": " + h.second + "\r\n";
  out += "Content-Length: " + std::to_string(r.body.size()) + "\r\n\r\n";
  out.append(r.body.begin(), r.body.end());
  return out;
}
void HttpResponseParser::AddBody(std::string_view s) {
  if (s.size() > kMaxBody - response_.body.size()) throw Failure("response_too_large");
  response_.body.insert(response_.body.end(), s.begin(), s.end());
}
void HttpResponseParser::Feed(std::string_view bytes) {
  if (complete_ && !bytes.empty()) throw Failure("invalid_response");
  // TLS hands us <=16 KiB records. Also bound the public parser entry.
  if (bytes.size() > kMaxBody + 32768 || buffer_.size() + bytes.size() > kMaxBody + 32768) throw Failure("response_too_large");
  buffer_.append(bytes);
  Parse();
}
void HttpResponseParser::Parse() {
  if (!headers_) {
    auto end = buffer_.find("\r\n\r\n");
    if (end == std::string::npos) {
      if (buffer_.size() > 32768) throw Failure("response_too_large");
      return;
    }
    if (end > 32768) throw Failure("response_too_large");
    const auto first = buffer_.find("\r\n");
    const auto status = buffer_.substr(0, first);
    if (status.size() < 12 || (status.rfind("HTTP/1.1 ", 0) != 0 && status.rfind("HTTP/1.0 ", 0) != 0) ||
        !Visible(status) || (status.size() > 12 && status[12] != ' ')) throw Failure("invalid_response");
    response_.status = static_cast<int>(Number(std::string_view(status).substr(9, 3), 10, 599));
    if (response_.status < 200) throw Failure("invalid_response");
    std::map<std::string, std::string> fields;
    for (size_t pos = first + 2; pos < end;) {
      const auto next = buffer_.find("\r\n", pos);
      const auto line = buffer_.substr(pos, next - pos);
      const auto colon = line.find(':');
      if (colon == std::string::npos || !Token(std::string_view(line).substr(0, colon))) throw Failure("invalid_response");
      auto name = Lower(line.substr(0, colon));
      auto value = Trim(line.substr(colon + 1));
      if (!Visible(value)) throw Failure("invalid_response");
      // Duplicate framing fields are ambiguous even when their values match.
      if (fields.count(name) && (name == "content-length" || name == "transfer-encoding" || name == "content-encoding")) throw Failure("invalid_response");
      fields[name] = value;
      pos = next + 2;
    }
    if (fields.count("content-encoding") && Lower(fields["content-encoding"]) != "identity") throw Failure("invalid_response");
    if (fields.count("transfer-encoding")) {
      if (Lower(fields["transfer-encoding"]) != "chunked" || fields.count("content-length")) throw Failure("invalid_response");
      chunked_ = true;
    }
    if (fields.count("content-length")) {
      remaining_ = Number(fields["content-length"], 10, kMaxBody);
      sized_ = true;
    }
    for (const auto* name : {"content-type", "retry-after", "date"}) {
      if (fields.count(name)) response_.headers[name] = fields[name];
    }
    if (response_.status == 204 || response_.status == 304) {
      if (chunked_ || remaining_ != 0) throw Failure("invalid_response");
      sized_ = true;
    }
    buffer_.erase(0, end + 4);
    headers_ = true;
  }
  if (!chunked_) {
    if (sized_ && buffer_.size() > remaining_) throw Failure("invalid_response");
    AddBody(buffer_);
    if (sized_) { remaining_ -= buffer_.size(); complete_ = remaining_ == 0; }
    buffer_.clear();
    return;
  }
  while (!complete_) {
    if (trailers_) {
      auto end = buffer_.find("\r\n");
      if (end == std::string::npos) {
        if (buffer_.size() + framing_bytes_ > 32768) throw Failure("response_too_large");
        return;
      }
      framing_bytes_ += end + 2;
      if (framing_bytes_ > 32768) throw Failure("response_too_large");
      // Trailer fields are unsupported: fail closed instead of merging them.
      if (end != 0) throw Failure("invalid_response");
      buffer_.erase(0, 2); complete_ = true;
      break;
    }
    if (chunk_crlf_) {
      if (buffer_.size() < 2) return;
      if (buffer_.substr(0, 2) != "\r\n") throw Failure("invalid_response");
      buffer_.erase(0, 2); chunk_crlf_ = false;
    }
    if (remaining_ == 0) {
      auto end = buffer_.find("\r\n");
      if (end == std::string::npos) {
        if (buffer_.size() > 1024) throw Failure("invalid_response");
        return;
      }
      framing_bytes_ += end + 2;
      if (framing_bytes_ > 32768 || end > 1024) throw Failure("response_too_large");
      // Extensions are unnecessary for this JSON API; reject rather than
      // interpreting multiple framing dialects.
      remaining_ = Number(std::string_view(buffer_).substr(0, end), 16, kMaxBody - response_.body.size());
      buffer_.erase(0, end + 2);
      if (!remaining_) { trailers_ = true; continue; }
    }
    const auto count = std::min(remaining_, buffer_.size());
    AddBody(std::string_view(buffer_).substr(0, count));
    remaining_ -= count; buffer_.erase(0, count);
    if (remaining_) return;
    chunk_crlf_ = true;
  }
  if (!buffer_.empty()) throw Failure("invalid_response");
}
void HttpResponseParser::End() {
  if (!headers_ || (!complete_ && (sized_ || chunked_))) throw Failure("invalid_response");
  complete_ = true;
}
}
