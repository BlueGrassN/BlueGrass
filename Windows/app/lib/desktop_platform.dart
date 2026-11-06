import 'dart:io';

import 'package:flutter/foundation.dart';

/// Desktop presentation/capability policy. Native methods remain on the shared
/// bluegrass/platform channel; the host owns Keychain/DPAPI and OS integration.
enum DesktopPlatform {
  windows,
  macos;

  static DesktopPlatform get current =>
      defaultTargetPlatform == TargetPlatform.macOS ? macos : windows;

  bool get isMac => this == macos;
  bool get supportsTun => true;
  String get tunnelBlockingState => isMac ? 'pf_blocking' : 'wfp_blocking';
  String get tunnelForwardingState => isMac ? 'pf_forwarding' : 'wfp_forwarding';
  bool get supportsWindowsUpdater => !isMac;
  String get name => isMac ? 'macOS' : 'Windows';
  String get diagnosticId => isMac ? 'macos' : 'windows';
  String get fontFamily => isMac ? 'PingFang SC' : 'Microsoft YaHei';
  String get storage => isMac ? 'macOS 钥匙串' : 'Windows 当前用户加密存储';
  String get loginStorage => isMac ? '凭据由 macOS 钥匙串加密保存' : '凭据由 Windows 加密保存';
  String get minimumSystem =>
      isMac ? 'macOS 13+ · Apple Silicon / Intel' : 'Windows 10 / 11 · x64';
  String get startupTitle => '登录 $name 时启动';
  String get startupFooter => isMac
      ? '登录 macOS 时启动：仅当前用户；也可在系统设置“通用 → 登录项”中禁用。'
      : '登录 Windows 时启动：仅当前 Windows 用户；也可在系统“启动应用”中禁用。';
  String get closeWindow => isMac ? '隐藏窗口；从菜单栏菜单退出应用' : '隐藏到系统托盘；从托盘菜单退出应用';
  String get manualUpdate =>
      isMac ? '开发版本需手动下载并替换应用。此版本不会自动检查、下载或安装更新。' : '当前会话未接入更新组件，不会下载或安装更新。';
  static const tunUnavailable = '当前平台暂不支持 TUN，请使用系统代理模式。';

  /// Both helpers live beside the GUI (Contents/MacOS in a macOS bundle).
  /// Only macOS drops .exe, preserving the Windows resolver exactly.
  String helperPath(String stem, {Uri? executable}) =>
      (executable ?? File(Platform.resolvedExecutable).uri)
          .resolve(isMac ? stem : '$stem.exe')
          .toFilePath();
}
