import '../app_controller.dart';
import '../platform/vpn_client.dart';

String vpnStateLabel(VpnState state) => switch (state) {
  VpnState.idle => '未连接',
  VpnState.connecting => '连接中',
  VpnState.connected => '已连接',
  VpnState.paused => '已暂停',
  VpnState.error => '连接异常',
};

String vpnStateDetail(AppController controller) =>
    switch (controller.vpnState) {
      VpnState.idle => '选择资源后连接 VPN',
      VpnState.connecting => '正在建立 VPN 连接',
      VpnState.connected => 'VPN 通道已建立',
      VpnState.paused =>
        controller.vpnSnapshot?.tunActive == true
            ? '网络已暂停，VPN 保护通道仍保留'
            : '连接已暂停，请重试',
      VpnState.error =>
        controller.vpnSnapshot?.tunActive == true
            ? '连接异常，VPN 保护通道仍保留'
            : 'VPN 连接失败，请重试',
    };

String exitObservationDetail(VpnObservationState state) => switch (state) {
  VpnObservationState.unknown => '连接后可检测实际 IPv4 出口。',
  VpnObservationState.checking => '正在检测实际出口…',
  VpnObservationState.available => '此地址来自实际出口检测。',
  VpnObservationState.failed => '出口检测失败，VPN 连接状态不受影响。',
};

bool canObserveExit(AppController controller) =>
    controller.vpnState == VpnState.connected &&
    controller.observationState != VpnObservationState.checking &&
    !controller.authBusy &&
    !controller.vpnBusy;
