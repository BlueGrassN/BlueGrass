import 'package:flutter/material.dart';

class ExtractedBrand extends StatelessWidget {
  const ExtractedBrand({this.size = 23});
  final double size;
  @override
  Widget build(BuildContext context) => FittedBox(
    fit: BoxFit.scaleDown,
    alignment: Alignment.centerLeft,
    child: Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(
          Icons.grass,
          size: size + 12,
          color: Theme.of(context).colorScheme.primary,
        ),
        const SizedBox(width: 1.75),
        Text(
          'BlueGrass',
          style: TextStyle(
            fontSize: size,
            fontWeight: FontWeight.w800,
            letterSpacing: -.7,
          ),
        ),
      ],
    ),
  );
}
