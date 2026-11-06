import 'dart:ffi';
import 'dart:io';

// Read-only WinINet query, using the same current-user LAN options as native
// ownership/restoration. No registry writes, shell, proxy changes or sockets.
final class _OptionValue extends Union {
  @Uint64()
  external int bits;
  external Pointer<Uint16> text;
}

final class _Option extends Struct {
  @Uint32()
  external int kind;
  external _OptionValue value;
}

final class _Options extends Struct {
  @Uint32()
  external int size;
  external Pointer<Void> connection;
  @Uint32()
  external int count;
  @Uint32()
  external int optionError;
  external Pointer<_Option> options;
}

class WindowsProxySnapshot {
  const WindowsProxySnapshot(
    this.flags,
    this.server,
    this.bypass,
    this.autoConfig,
  );
  final int flags;
  final String server, bypass, autoConfig;

  // Compare without reporting private proxy/PAC configuration in test output.
  bool sameAs(WindowsProxySnapshot other) =>
      flags == other.flags &&
      server == other.server &&
      bypass == other.bypass &&
      autoConfig == other.autoConfig;
}

WindowsProxySnapshot readWindowsProxySnapshot() {
  if (!Platform.isWindows) throw StateError('windows_required');
  final kernel = DynamicLibrary.open('kernel32.dll');
  final getHeap = kernel
      .lookupFunction<Pointer<Void> Function(), Pointer<Void> Function()>(
        'GetProcessHeap',
      );
  final alloc = kernel
      .lookupFunction<
        Pointer<Void> Function(Pointer<Void>, Uint32, IntPtr),
        Pointer<Void> Function(Pointer<Void>, int, int)
      >('HeapAlloc');
  final free = kernel
      .lookupFunction<
        Int32 Function(Pointer<Void>, Uint32, Pointer<Void>),
        int Function(Pointer<Void>, int, Pointer<Void>)
      >('HeapFree');
  final globalFree = kernel
      .lookupFunction<
        Pointer<Void> Function(Pointer<Void>),
        Pointer<Void> Function(Pointer<Void>)
      >('GlobalFree');
  final systemDirectory = kernel
      .lookupFunction<
        Uint32 Function(Pointer<Uint16>, Uint32),
        int Function(Pointer<Uint16>, int)
      >('GetSystemDirectoryW');
  final getError = kernel.lookupFunction<Uint32 Function(), int Function()>(
    'GetLastError',
  );
  final heap = getHeap();
  final allocations = <Pointer<Void>>[];
  Pointer<Void> allocate(int bytes) {
    final result = alloc(heap, 8, bytes);
    if (result == nullptr) throw StateError('native_allocation_failed');
    allocations.add(result);
    return result;
  }

  String readString(Pointer<Uint16> pointer) {
    if (pointer == nullptr) return '';
    final units = <int>[];
    for (var index = 0; index < 32768; index++) {
      final value = pointer[index];
      if (value == 0) return String.fromCharCodes(units);
      units.add(value);
    }
    throw StateError('proxy_string_out_of_bounds');
  }

  try {
    final dir = allocate(32768 * 2).cast<Uint16>();
    final length = systemDirectory(dir, 32768);
    if (length == 0 || length >= 32768) {
      throw StateError('system_directory_unavailable');
    }
    final inet = DynamicLibrary.open('${readString(dir)}\\wininet.dll');
    final query = inet
        .lookupFunction<
          Int32 Function(Pointer<Void>, Uint32, Pointer<Void>, Pointer<Uint32>),
          int Function(Pointer<Void>, int, Pointer<Void>, Pointer<Uint32>)
        >('InternetQueryOptionW');
    final options = allocate(sizeOf<_Option>() * 4).cast<_Option>();
    final list = allocate(sizeOf<_Options>()).cast<_Options>();
    final size = allocate(sizeOf<Uint32>()).cast<Uint32>();
    list.ref
      ..size = sizeOf<_Options>()
      ..count = 4
      ..options = options;
    for (final flagsOption in [10, 1]) {
      options[0].kind = flagsOption;
      for (var i = 1; i < 4; i++) {
        options[i].kind = i + 1;
      }
      size.value = sizeOf<_Options>();
      try {
        if (query(nullptr, 75, list.cast(), size) == 0) {
          final code = getError();
          if (flagsOption == 10 && (code == 87 || code == 12009)) continue;
          throw StateError('proxy_query_failed');
        }
        return WindowsProxySnapshot(
          options[0].value.bits & 0xffffffff,
          readString(options[1].value.text),
          readString(options[2].value.text),
          readString(options[3].value.text),
        );
      } finally {
        for (var i = 1; i < 4; i++) {
          if (options[i].value.text != nullptr) {
            globalFree(options[i].value.text.cast());
          }
          options[i].value.bits = 0;
        }
      }
    }
    throw StateError('proxy_query_failed');
  } finally {
    for (final pointer in allocations.reversed) {
      free(heap, 0, pointer);
    }
  }
}
