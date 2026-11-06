import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import 'dev/fixture_data_source.dart';
import 'main.dart';

void main() {
  if (kReleaseMode) {
    throw StateError('Fixture is not allowed in release builds');
  }
  runApp(MyApp(controller: fixtureController()));
}
