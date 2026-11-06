import 'package:flutter/material.dart';

/// The supplied outlined wordmark, with the matching light/dark text color.
class BlueGrassBrand extends StatelessWidget {
  const BlueGrassBrand({super.key, this.size = 28});

  final double size;

  @override
  Widget build(BuildContext context) => FittedBox(
    fit: BoxFit.scaleDown,
    alignment: Alignment.centerLeft,
    child: Image.asset(
      Theme.of(context).brightness == Brightness.dark
          ? 'assets/branding/wordmark-dark.png'
          : 'assets/branding/wordmark-light.png',
      width: size * 178.5 / 28,
      height: size * 44 / 28,
      fit: BoxFit.contain,
      filterQuality: FilterQuality.high,
      semanticLabel: 'BlueGrass',
    ),
  );
}
