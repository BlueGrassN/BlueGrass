import 'package:flutter/services.dart';

/// The native side opens only the fixed official HTTPS site.
Future<void> openOfficialWebsite() =>
    const MethodChannel('bluegrass/android')
        .invokeMethod<void>('openOfficialWebsite');
