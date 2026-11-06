#ifndef BLUEGRASS_CONTROL_HTTP_BRIDGE_H_
#define BLUEGRASS_CONTROL_HTTP_BRIDGE_H_
#include <flutter/method_call.h>
#include <flutter/method_result.h>
#include <flutter/encodable_value.h>
#include <windows.h>
#include <memory>

namespace bluegrass {
class ControlHttpBridge {
 public:
  ControlHttpBridge(HWND window, UINT completion_message);
  ~ControlHttpBridge();
  bool Handles(const std::string& method) const;
  void HandleCall(const flutter::MethodCall<flutter::EncodableValue>& call,
      std::unique_ptr<flutter::MethodResult<flutter::EncodableValue>> result);
  void Complete();
 private:
  struct Impl;
  std::unique_ptr<Impl> impl_;
};
}
#endif
