import 'package:flutter/material.dart';

import '../../app_theme.dart' as shared;

/// Touch host uses the same BlueGrass theme and components as desktop.
ThemeData blueGrassTheme(Brightness brightness) =>
    shared.blueGrassTheme(brightness, touch: true);
