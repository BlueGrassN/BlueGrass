#include "control_http_internal.h"
#include <iphlpapi.h>
#include <algorithm>
#include <cstring>

namespace bluegrass::control_http {
namespace {
const uint8_t* Bytes(const SOCKADDR_INET& a) {
  return reinterpret_cast<const uint8_t*>(a.si_family == AF_INET ?
      static_cast<const void*>(&a.Ipv4.sin_addr) : static_cast<const void*>(&a.Ipv6.sin6_addr));
}
bool SameAddress(const SOCKADDR_INET& a, const SOCKADDR_INET& b) {
  return a.si_family == b.si_family && std::memcmp(Bytes(a), Bytes(b), a.si_family == AF_INET ? 4 : 16) == 0;
}
bool Contains(const SOCKADDR_INET& prefix, uint8_t bits, const SOCKADDR_INET& a) {
  if (prefix.si_family != a.si_family || bits > (a.si_family == AF_INET ? 32 : 128)) return false;
  const auto *p = Bytes(prefix), *v = Bytes(a);
  for (unsigned i = 0; i < bits; ++i) if ((p[i / 8] ^ v[i / 8]) & (0x80 >> (i % 8))) return false;
  return true;
}
SOCKADDR_INET Numeric(const std::string& text) {
  SOCKADDR_INET a{};
  if (InetPtonA(AF_INET, text.c_str(), &a.Ipv4.sin_addr) == 1) a.si_family = AF_INET;
  else if (InetPtonA(AF_INET6, text.c_str(), &a.Ipv6.sin6_addr) == 1) a.si_family = AF_INET6;
  else throw Failure("physical_route_unavailable");
  return a;
}
bool AllowedAddress(const SOCKADDR_INET& a) {
  if (a.si_family != AF_INET && a.si_family != AF_INET6) return false;
  const auto* b = Bytes(a);
  if (a.si_family == AF_INET) {
    if (b[0] == 0 || b[0] == 127 || b[0] >= 224 || (b[0] == 169 && b[1] == 254)) return false;
  } else {
    static const uint8_t zero[16]{};
    if (std::memcmp(b, zero, 15) == 0 || b[0] == 0xff || (b[0] == 0xfe && (b[1] & 0xc0) == 0x80) ||
        IN6_IS_ADDR_V4MAPPED(&a.Ipv6.sin6_addr) || a.Ipv6.sin6_scope_id != 0) return false;
  }
  struct Denied { SOCKADDR_INET first; uint8_t second; };
  static const Denied denied[] = {
    {Numeric("198.18.0.0"),15}, {Numeric("fc00::"),18}, {Numeric("172.19.0.0"),30},
    {Numeric("172.31.255.0"),30}, {Numeric("172.30.255.0"),30}, {Numeric("10.255.255.0"),30},
    {Numeric("fdfe:dcba:9876::"),126}, {Numeric("fdde:adbe:ef01::"),126},
  };
  for (const auto& p : denied) if (Contains(p.first, p.second, a)) return false;
  return true;
}
bool HardwareType(ULONG t) { return t == 6 || t == 71 || t == 243 || t == 244; }
Route Snapshot(const SOCKADDR_INET& destination, const Deadline& deadline) {
  deadline.Check();
  ULONG os_best = 0;
  auto d = destination;
  if (GetBestInterfaceEx(reinterpret_cast<sockaddr*>(&d), &os_best) != NO_ERROR) throw Failure("physical_route_unavailable");
  ULONG size = 16384;
  for (int attempt = 0; attempt < 4; ++attempt) {
    deadline.Check();
    if (size > 16 * 1024 * 1024) throw Failure("physical_route_unavailable");
    std::vector<uint8_t> buffer(size);
    auto* first = reinterpret_cast<IP_ADAPTER_ADDRESSES*>(buffer.data());
    const auto result = GetAdaptersAddresses(AF_UNSPEC, GAA_FLAG_SKIP_ANYCAST | GAA_FLAG_SKIP_MULTICAST | GAA_FLAG_SKIP_DNS_SERVER,
        nullptr, first, &size);
    if (result == ERROR_BUFFER_OVERFLOW) continue;
    if (result != NO_ERROR) throw Failure("physical_route_unavailable");
    std::vector<Route> routes;
    size_t count = 0;
    for (auto* a = first; a; a = a->Next) {
      deadline.Check();
      if (++count > 256) throw Failure("physical_route_unavailable");
      if (!a->Luid.Value || a->OperStatus != IfOperStatusUp || !HardwareType(a->IfType)) continue;
      MIB_IF_ROW2 row{}; row.InterfaceLuid = a->Luid;
      if (GetIfEntry2Ex(MibIfEntryNormalWithoutStatistics, &row) != NO_ERROR) continue;
      const auto& flags = row.InterfaceAndOperStatusFlags;
      const GUID empty{};
      if (row.InterfaceLuid.Value != a->Luid.Value || row.OperStatus != IfOperStatusUp || row.AdminStatus != NET_IF_ADMIN_STATUS_UP ||
          row.MediaConnectState != MediaConnectStateConnected || !flags.HardwareInterface || flags.FilterInterface || flags.EndPointInterface ||
          flags.NotMediaConnected || flags.Paused || flags.LowPower || flags.NotAuthenticated || !HardwareType(row.Type) ||
          std::memcmp(&row.InterfaceGuid, &empty, sizeof(GUID)) == 0 ||
          (a->FriendlyName && _wcsnicmp(a->FriendlyName, L"bluegrass", 9) == 0)) continue;
      MIB_IPFORWARD_ROW2 best{};
      SOCKADDR_INET source{};
      if (GetBestRoute2(&a->Luid, 0, nullptr, &destination, 0, &best, &source) != NO_ERROR) continue;
      const ULONG index = destination.si_family == AF_INET ? a->IfIndex : a->Ipv6IfIndex;
      const ULONG metric = destination.si_family == AF_INET ? a->Ipv4Metric : a->Ipv6Metric;
      bool assigned = false;
      size_t address_count = 0;
      for (auto* u = a->FirstUnicastAddress; u; u = u->Next) {
        if (++address_count > 1024) throw Failure("physical_route_unavailable");
        if (!u->Address.lpSockaddr || u->DadState != IpDadStatePreferred || !u->ValidLifetime) continue;
        SOCKADDR_INET local{};
        const int length = source.si_family == AF_INET ? sizeof(sockaddr_in) : sizeof(sockaddr_in6);
        if (u->Address.iSockaddrLength < length || u->Address.lpSockaddr->sa_family != source.si_family) continue;
        std::memcpy(&local, u->Address.lpSockaddr, static_cast<size_t>(length));
        if (SameAddress(local, source)) assigned = true;
      }
      if (!assigned || !index || !AllowedAddress(source) || source.si_family != destination.si_family ||
          best.InterfaceLuid.Value != a->Luid.Value || best.InterfaceIndex != index || best.Loopback || !best.ValidLifetime ||
          !Contains(best.DestinationPrefix.Prefix, best.DestinationPrefix.PrefixLength, destination) ||
          best.NextHop.si_family != destination.si_family) continue;
      // On-link zero next-hop is valid. Multicast/loopback next hops are not.
      const auto* hop = Bytes(best.NextHop);
      if (destination.si_family == AF_INET ? (hop[0] == 127 || hop[0] >= 224) :
          (IN6_IS_ADDR_MULTICAST(&best.NextHop.Ipv6.sin6_addr) || IN6_IS_ADDR_LOOPBACK(&best.NextHop.Ipv6.sin6_addr) || IN6_IS_ADDR_V4MAPPED(&best.NextHop.Ipv6.sin6_addr))) continue;
      Route r;
      r.destination = destination; r.source = source; r.prefix = best.DestinationPrefix.Prefix;
      r.prefix_bits = best.DestinationPrefix.PrefixLength; r.next_hop = best.NextHop;
      r.index = index; r.luid = a->Luid.Value; r.guid = row.InterfaceGuid;
      r.metric = static_cast<uint64_t>(best.Metric) + metric;
      routes.push_back(r);
    }
    return SelectRoute(routes, os_best);
  }
  throw Failure("physical_route_unavailable");
}
}
bool Route::operator==(const Route& r) const {
  return index == r.index && luid == r.luid && metric == r.metric && prefix_bits == r.prefix_bits &&
      std::memcmp(&guid, &r.guid, sizeof(GUID)) == 0 && SameAddress(destination, r.destination) &&
      SameAddress(source, r.source) && SameAddress(prefix, r.prefix) && SameAddress(next_hop, r.next_hop);
}
void Deadline::Check() const {
  if (cancelled.load()) throw Failure("cancelled");
  if (std::chrono::steady_clock::now() >= end) throw Failure("timeout");
}
SOCKADDR_INET ParsePhysicalAddress(const std::string& text) {
  if (text.empty() || text.size() > 45 || text.find('\0') != std::string::npos) throw Failure("physical_route_unavailable");
  auto a = Numeric(text);
  if (!AllowedAddress(a)) throw Failure("physical_route_unavailable");
  return a;
}
Route SelectRoute(const std::vector<Route>& routes, uint32_t os_best) {
  if (routes.empty()) throw Failure("physical_route_unavailable");
  auto best = routes.front();
  for (const auto& r : routes) if (r.prefix_bits > best.prefix_bits || (r.prefix_bits == best.prefix_bits && r.metric < best.metric)) best = r;
  bool physical_os_best = false;
  for (const auto& r : routes) {
    if (r.index == os_best) physical_os_best = true;
    if (r.prefix_bits == best.prefix_bits && r.metric == best.metric && r.index == os_best) best = r;
  }
  for (const auto& r : routes) if (r.prefix_bits == best.prefix_bits && r.metric == best.metric && !(r == best) && best.index != os_best) throw Failure("physical_route_unavailable");
  if (physical_os_best && best.index != os_best) throw Failure("physical_route_unavailable");
  return best;
}
Route PhysicalRoute(const SOCKADDR_INET& destination, const Deadline& deadline) {
  Route previous;
  for (int i = 0; i < 3; ++i) {
    auto current = Snapshot(destination, deadline);
    if (i && current == previous) return current;
    previous = current;
  }
  throw Failure("physical_route_unavailable");
}
void WaitSocket(SOCKET socket, bool write, const Deadline& deadline) {
  for (;;) {
    deadline.Check();
    fd_set ready, errors;
    FD_ZERO(&ready); FD_SET(socket, &ready);
    FD_ZERO(&errors); FD_SET(socket, &errors);
    timeval timeout{0, 50000};
    const auto n = select(0, write ? nullptr : &ready, write ? &ready : nullptr, &errors, &timeout);
    if (n == SOCKET_ERROR || FD_ISSET(socket, &errors)) throw Failure("network_error");
    if (n > 0) { deadline.Check(); return; }
  }
}
SOCKET ConnectPhysical(const Route& route, const Deadline& deadline) {
  deadline.Check();
  SOCKET s = socket(route.destination.si_family, SOCK_STREAM, IPPROTO_TCP);
  if (s == INVALID_SOCKET) throw Failure("network_error");
  try {
    if (!(PhysicalRoute(route.destination, deadline) == route)) throw Failure("physical_route_unavailable");
    const bool v4 = route.destination.si_family == AF_INET;
    const int level = v4 ? IPPROTO_IP : IPPROTO_IPV6;
    const int option = v4 ? IP_UNICAST_IF : IPV6_UNICAST_IF;
    const DWORD index = v4 ? htonl(route.index) : route.index;
    DWORD actual = 0; int size = sizeof(actual);
    if (setsockopt(s, level, option, reinterpret_cast<const char*>(&index), sizeof(index)) != 0 ||
        getsockopt(s, level, option, reinterpret_cast<char*>(&actual), &size) != 0 || size != sizeof(actual) || actual != route.index) throw Failure("physical_route_unavailable");
    auto source = route.source;
    source.Ipv4.sin_port = 0;
    const int length = v4 ? sizeof(sockaddr_in) : sizeof(sockaddr_in6);
    if (bind(s, reinterpret_cast<const sockaddr*>(&source), length) != 0) throw Failure("physical_route_unavailable");
    u_long nonblocking = 1;
    if (ioctlsocket(s, FIONBIO, &nonblocking) != 0) throw Failure("network_error");
    auto dest = route.destination;
    dest.Ipv4.sin_port = htons(443);
    // Last route check occurs after binding and immediately before connect.
    if (!(PhysicalRoute(route.destination, deadline) == route)) throw Failure("physical_route_unavailable");
    deadline.Check();
    if (connect(s, reinterpret_cast<const sockaddr*>(&dest), length) != 0) {
      if (WSAGetLastError() != WSAEWOULDBLOCK) throw Failure("network_error");
      WaitSocket(s, true, deadline);
      int error = 0; int n = sizeof(error);
      if (getsockopt(s, SOL_SOCKET, SO_ERROR, reinterpret_cast<char*>(&error), &n) != 0 || error != 0) throw Failure("network_error");
    }
    SOCKADDR_INET local{}, peer{}; int n = sizeof(local);
    if (getsockname(s, reinterpret_cast<sockaddr*>(&local), &n) != 0 || !SameAddress(local, route.source)) throw Failure("physical_route_unavailable");
    n = sizeof(peer);
    if (getpeername(s, reinterpret_cast<sockaddr*>(&peer), &n) != 0 || !SameAddress(peer, route.destination) || peer.Ipv4.sin_port != htons(443) ||
        !(PhysicalRoute(route.destination, deadline) == route)) throw Failure("physical_route_unavailable");
    return s;
  } catch (...) { closesocket(s); throw; }
}
}
