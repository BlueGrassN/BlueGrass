import 'package:flutter/material.dart';

// One restrained palette across pages, dialogs and native-sized controls.
ThemeData blueGrassTheme(Brightness brightness) {
  final dark = brightness == Brightness.dark;
  final colors =
      ColorScheme.fromSeed(
        seedColor: const Color(0xff087e98),
        brightness: brightness,
      ).copyWith(
        primary: dark ? const Color(0xff67d2e5) : const Color(0xff087e98),
        onPrimary: dark ? const Color(0xff003641) : Colors.white,
        primaryContainer: dark
            ? const Color(0xff15353f)
            : const Color(0xffeaf7fa),
        onPrimaryContainer: dark
            ? const Color(0xff98deeb)
            : const Color(0xff08647b),
        surface: dark ? const Color(0xff141c23) : Colors.white,
        surfaceContainerLowest: dark ? const Color(0xff111820) : Colors.white,
        surfaceContainerLow: dark
            ? const Color(0xff18222b)
            : const Color(0xfff7f9fb),
        surfaceContainer: dark
            ? const Color(0xff202b35)
            : const Color(0xfff1f5f8),
        surfaceContainerHigh: dark
            ? const Color(0xff28343e)
            : const Color(0xffeaf0f4),
        onSurface: dark ? const Color(0xffe8eef3) : const Color(0xff182b3a),
        onSurfaceVariant: dark
            ? const Color(0xffa0b0bd)
            : const Color(0xff647785),
        outline: dark ? const Color(0xff657582) : const Color(0xff94a4af),
        outlineVariant: dark
            ? const Color(0xff2e3c48)
            : const Color(0xffe1e8ee),
      );
  final shape = RoundedRectangleBorder(borderRadius: BorderRadius.circular(4));
  final inputBorder = OutlineInputBorder(
    borderRadius: BorderRadius.circular(4),
    borderSide: BorderSide(color: colors.outlineVariant),
  );
  final base = ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme: colors,
    scaffoldBackgroundColor: colors.surface,
    fontFamily: 'Microsoft YaHei',
  );
  return base.copyWith(
    textTheme: base.textTheme.copyWith(
      headlineMedium: base.textTheme.headlineMedium?.copyWith(
        fontSize: 27,
        fontWeight: FontWeight.w700,
        letterSpacing: -.7,
      ),
      headlineSmall: base.textTheme.headlineSmall?.copyWith(
        fontSize: 24,
        fontWeight: FontWeight.w600,
        letterSpacing: -.4,
      ),
      bodyMedium: base.textTheme.bodyMedium?.copyWith(fontSize: 13),
      bodyLarge: base.textTheme.bodyLarge?.copyWith(fontSize: 14),
      titleMedium: base.textTheme.titleMedium?.copyWith(fontSize: 14),
    ),
    cardTheme: CardThemeData(
      color: colors.surface,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      margin: EdgeInsets.zero,
      shape: shape.copyWith(side: BorderSide(color: colors.outlineVariant)),
    ),
    dividerTheme: DividerThemeData(
      color: colors.outlineVariant,
      thickness: 1,
      space: 1,
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: colors.surface,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 17),
      border: inputBorder,
      enabledBorder: inputBorder,
      disabledBorder: inputBorder,
      focusedBorder: inputBorder.copyWith(
        borderSide: BorderSide(color: colors.primary, width: 1.5),
      ),
      prefixIconColor: colors.onSurfaceVariant,
      suffixIconColor: colors.onSurfaceVariant,
      labelStyle: TextStyle(color: colors.onSurfaceVariant, fontSize: 13),
      hintStyle: TextStyle(color: colors.onSurfaceVariant, fontSize: 13),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        elevation: 0,
        minimumSize: const Size(0, 44),
        padding: const EdgeInsets.symmetric(horizontal: 22, vertical: 14),
        shape: shape,
        textStyle: const TextStyle(
          fontFamily: 'Microsoft YaHei',
          fontSize: 13,
          fontWeight: FontWeight.w600,
        ),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(0, 40),
        padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 12),
        shape: shape,
        side: BorderSide(color: colors.outlineVariant),
        textStyle: const TextStyle(
          fontFamily: 'Microsoft YaHei',
          fontSize: 13,
          fontWeight: FontWeight.w500,
        ),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(
        shape: shape,
        textStyle: const TextStyle(fontFamily: 'Microsoft YaHei', fontSize: 13),
      ),
    ),
    iconButtonTheme: IconButtonThemeData(
      style: IconButton.styleFrom(shape: shape, iconSize: 20),
    ),
    segmentedButtonTheme: SegmentedButtonThemeData(
      style: ButtonStyle(
        shape: WidgetStatePropertyAll(shape),
        side: WidgetStatePropertyAll(BorderSide(color: colors.outlineVariant)),
        padding: const WidgetStatePropertyAll(
          EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        ),
        textStyle: const WidgetStatePropertyAll(
          TextStyle(fontFamily: 'Microsoft YaHei', fontSize: 13),
        ),
        backgroundColor: WidgetStateProperty.resolveWith(
          (states) => states.contains(WidgetState.selected)
              ? colors.primaryContainer
              : colors.surface,
        ),
        foregroundColor: WidgetStateProperty.resolveWith(
          (states) => states.contains(WidgetState.selected)
              ? colors.primary
              : colors.onSurfaceVariant,
        ),
      ),
    ),
    listTileTheme: ListTileThemeData(
      iconColor: colors.onSurfaceVariant,
      selectedColor: colors.primary,
      shape: shape,
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: colors.surface,
      surfaceTintColor: Colors.transparent,
      indicatorColor: colors.primaryContainer,
      indicatorShape: shape,
      height: 68,
      labelTextStyle: const WidgetStatePropertyAll(
        TextStyle(fontFamily: 'Microsoft YaHei', fontSize: 11),
      ),
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: colors.surface,
      surfaceTintColor: Colors.transparent,
      shape: shape,
      elevation: 8,
    ),
    popupMenuTheme: PopupMenuThemeData(
      shape: shape,
      color: colors.surface,
      surfaceTintColor: Colors.transparent,
    ),
    dropdownMenuTheme: DropdownMenuThemeData(
      menuStyle: MenuStyle(
        shape: WidgetStatePropertyAll(shape),
        backgroundColor: WidgetStatePropertyAll(colors.surface),
        surfaceTintColor: const WidgetStatePropertyAll(Colors.transparent),
      ),
    ),
    tooltipTheme: TooltipThemeData(
      decoration: BoxDecoration(
        color: colors.inverseSurface,
        borderRadius: BorderRadius.circular(4),
      ),
    ),
  );
}
