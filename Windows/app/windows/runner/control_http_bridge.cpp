#include "control_http_bridge.h"
#include "control_http.h"
#include <map>
#include <stdexcept>
#include <thread>
#include <variant>

namespace bluegrass {
namespace {
using flutter::EncodableMap;
using flutter::EncodableValue;
using Result = flutter::MethodResult<EncodableValue>;
template<class T> const T& Field(const EncodableMap& m, const char* key) {
  const auto it = m.find(EncodableValue(key));
  if (it == m.end() || !std::holds_alternative<T>(it->second)) throw std::runtime_error("invalid_request");
  return std::get<T>(it->second);
}
struct State {
  control_http::Request request;
  control_http::Response response;
  std::atomic_bool cancelled{false}, done{false};
  ~State() {
    for (auto& h : request.headers) if (!h.second.empty()) SecureZeroMemory(h.second.data(), h.second.size());
    if (!request.body.empty()) SecureZeroMemory(request.body.data(), request.body.size());
    if (!response.body.empty()) SecureZeroMemory(response.body.data(), response.body.size());
  }
};
struct Job {
  std::shared_ptr<State> state;
  std::unique_ptr<Result> result;
  std::thread worker;
};
}
struct ControlHttpBridge::Impl {
  HWND window;
  UINT message;
  std::map<std::string, Job> jobs; // Touched on the Flutter platform thread only.
  std::string host = "client-api.sunip.vip";
  bool configured = false;
  Impl(HWND w, UINT m) : window(w), message(m) {}
  ~Impl() {
    for (auto& item : jobs) item.second.state->cancelled.store(true);
    for (auto& item : jobs) {
      if (item.second.worker.joinable()) item.second.worker.join();
      if (item.second.result) item.second.result->Error("cancelled", "Request cancelled");
    }
  }
};
ControlHttpBridge::ControlHttpBridge(HWND window, UINT message) : impl_(std::make_unique<Impl>(window, message)) {}
ControlHttpBridge::~ControlHttpBridge() = default;
bool ControlHttpBridge::Handles(const std::string& method) const {
  return method == "controlHttpSend" || method == "controlHttpCancel" || method == "controlHttpConfigure";
}
void ControlHttpBridge::HandleCall(const flutter::MethodCall<EncodableValue>& call, std::unique_ptr<Result> result) {
  try {
    Complete();
    const auto* args = call.arguments() ? std::get_if<EncodableMap>(call.arguments()) : nullptr;
    if (!args) throw std::runtime_error("invalid_request");
    if (call.method_name() == "controlHttpConfigure") {
      const auto host = Field<std::string>(*args, "host");
      if (args->size() != 1 || !control_http::ValidApiHost(host) || !impl_->jobs.empty() ||
          (impl_->configured && impl_->host != host)) throw std::runtime_error("invalid_request");
      // Only the application startup configuration sets this pin. Requests,
      // redirects and node profiles never provide or replace a TLS identity.
      impl_->host = host; impl_->configured = true;
      result->Success(); return;
    }
    const auto id = Field<std::string>(*args, "id");
    if (id.empty() || id.size() > 32 || id.find_first_not_of("0123456789") != std::string::npos) throw std::runtime_error("invalid_request");
    if (call.method_name() == "controlHttpCancel") {
      const auto found = impl_->jobs.find(id);
      if (found != impl_->jobs.end()) {
        found->second.state->cancelled.store(true);
        if (found->second.result) {
          found->second.result->Error("cancelled", "Request cancelled");
          found->second.result.reset();
        }
      }
      result->Success(); return;
    }
    if (args->size() != 7 || impl_->jobs.count(id)) throw std::runtime_error("invalid_request");
    if (impl_->jobs.size() >= 8) { result->Error("busy", "Request limit reached"); return; }
    auto state = std::make_shared<State>();
    impl_->configured = true;
    state->request.host = impl_->host;
    state->request.ip = Field<std::string>(*args, "ip");
    state->request.target = Field<std::string>(*args, "target");
    state->request.method = Field<std::string>(*args, "method");
    state->request.body = Field<std::vector<uint8_t>>(*args, "body");
    state->request.timeout_ms = Field<int32_t>(*args, "timeoutMs");
    const auto& headers = Field<EncodableMap>(*args, "headers");
    if (headers.size() > 3 || state->request.body.size() > 4096) throw std::runtime_error("invalid_request");
    for (const auto& h : headers) {
      if (!std::holds_alternative<std::string>(h.first) || !std::holds_alternative<std::string>(h.second)) throw std::runtime_error("invalid_request");
      state->request.headers.emplace(std::get<std::string>(h.first), std::get<std::string>(h.second));
    }
    auto& job = impl_->jobs[id];
    job.state = state;
    const auto window = impl_->window; const auto message = impl_->message;
    try {
      job.worker = std::thread([state, window, message] {
        state->response = control_http::Perform(state->request, state->cancelled);
        state->done.store(true, std::memory_order_release);
        // No posted pointers: shutdown can safely leave a harmless queued message.
        PostMessageW(window, message, 0, 0);
      });
    } catch (...) { impl_->jobs.erase(id); throw; }
    job.result = std::move(result);
  } catch (...) {
    if (result) result->Error("invalid_request", "Request rejected");
  }
}
void ControlHttpBridge::Complete() {
  for (auto it = impl_->jobs.begin(); it != impl_->jobs.end();) {
    auto& job = it->second;
    if (!job.state->done.load(std::memory_order_acquire)) { ++it; continue; }
    job.worker.join();
    const auto& r = job.state->response;
    if (job.result) {
      if (job.state->cancelled.load()) job.result->Error("cancelled", "Request cancelled");
      else if (!r.error.empty()) job.result->Error(r.error, "Control request failed");
      else {
        EncodableMap headers;
        for (const auto& h : r.headers) headers.emplace(EncodableValue(h.first), EncodableValue(h.second));
        job.result->Success(EncodableValue(EncodableMap{
          {EncodableValue("status"), EncodableValue(r.status)},
          {EncodableValue("headers"), EncodableValue(headers)},
          {EncodableValue("body"), EncodableValue(r.body)},
        }));
      }
    }
    it = impl_->jobs.erase(it);
  }
}
}
