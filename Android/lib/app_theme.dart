import 'package:flutter/material.dart';

/// Tonal mobile surfaces, generous corners and a compact floating dock.
ThemeData blueGrassTheme(Brightness brightness) {
  final dark = brightness == Brightness.dark;
  final colors =
      ColorScheme.fromSeed(
        seedColor: const Color(0xff007aff),
        brightness: brightness,
      ).copyWith(
        primary: dark ? const Color(0xff8cbeff) : const Color(0xff007aff),
        onPrimary: dark ? const Color(0xff002c60) : Colors.white,
        primaryContainer: dark
            ? const Color(0xff22364f)
            : const Color(0xffe2edff),
        onPrimaryContainer: dark
            ? const Color(0xffc5ddff)
            : const Color(0xff0057b9),
        surface: dark ? const Color(0xff25262b) : const Color(0xfffdfbfe),
        surfaceContainerLowest: dark ? const Color(0xff111216) : Colors.white,
        surfaceContainerLow: dark
            ? const Color(0xff141519)
            : const Color(0xffeeedf2),
        surfaceContainer: dark
            ? const Color(0xff32343b)
            : const Color(0xffedecf1),
        surfaceContainerHigh: dark
            ? const Color(0xff35373e)
            : const Color(0xffe3e4ec),
        onSurface: dark ? const Color(0xfff0f1f7) : const Color(0xff20232c),
        onSurfaceVariant: dark
            ? const Color(0xffb7bac6)
            : const Color(0xff626775),
        outlineVariant: dark
            ? const Color(0xff424650)
            : const Color(0xffdfe1e9),
      );
  final pill = RoundedRectangleBorder(borderRadius: BorderRadius.circular(24));
  final border = OutlineInputBorder(
    borderRadius: BorderRadius.circular(22),
    borderSide: BorderSide.none,
  );
  final base = ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme: colors,
    scaffoldBackgroundColor: colors.surfaceContainerLow,
    visualDensity: VisualDensity.standard,
    materialTapTargetSize: MaterialTapTargetSize.padded,
  );
  return base.copyWith(
    textTheme: base.textTheme.copyWith(
      headlineMedium: base.textTheme.headlineMedium?.copyWith(
        fontSize: 28,
        fontWeight: FontWeight.w700,
        letterSpacing: -.6,
      ),
      headlineSmall: base.textTheme.headlineSmall?.copyWith(
        fontSize: 24,
        fontWeight: FontWeight.w700,
        letterSpacing: -.5,
      ),
      titleLarge: base.textTheme.titleLarge?.copyWith(
        fontWeight: FontWeight.w700,
      ),
      titleMedium: base.textTheme.titleMedium?.copyWith(
        fontWeight: FontWeight.w600,
      ),
    ),
    appBarTheme: AppBarTheme(
      centerTitle: false,
      titleSpacing: 24,
      toolbarHeight: 76,
      titleTextStyle: base.textTheme.headlineSmall?.copyWith(
        color: colors.onSurface,
        fontWeight: FontWeight.w700,
      ),
      elevation: 0,
      scrolledUnderElevation: 0,
      backgroundColor: colors.surfaceContainerLow,
      surfaceTintColor: Colors.transparent,
    ),
    cardTheme: CardThemeData(
      color: colors.surface,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      margin: EdgeInsets.zero,
      shape: RoundedSuperellipseBorder(borderRadius: BorderRadius.circular(28)),
      clipBehavior: Clip.antiAlias,
    ),
    dividerTheme: DividerThemeData(
      color: colors.outlineVariant,
      thickness: .6,
      space: 1,
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: colors.surfaceContainer,
      contentPadding: const EdgeInsets.symmetric(horizontal: 20, vertical: 17),
      border: border,
      enabledBorder: border,
      disabledBorder: border,
      focusedBorder: border.copyWith(
        borderSide: BorderSide(color: colors.primary, width: 1.5),
      ),
      prefixIconColor: colors.onSurfaceVariant,
      suffixIconColor: colors.onSurfaceVariant,
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        elevation: 0,
        minimumSize: const Size(48, 48),
        padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 14),
        shape: pill,
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(
        minimumSize: const Size(48, 48),
        padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 14),
        shape: pill,
        side: BorderSide(color: colors.outlineVariant),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(minimumSize: const Size(48, 48), shape: pill),
    ),
    iconButtonTheme: IconButtonThemeData(
      style: IconButton.styleFrom(
        minimumSize: const Size(48, 48),
        shape: const CircleBorder(),
        iconSize: 22,
      ),
    ),
    segmentedButtonTheme: SegmentedButtonThemeData(
      style: ButtonStyle(
        side: const WidgetStatePropertyAll(BorderSide.none),
        shape: WidgetStatePropertyAll(pill),
        minimumSize: const WidgetStatePropertyAll(Size(48, 56)),
        backgroundColor: WidgetStateProperty.resolveWith(
          (states) => states.contains(WidgetState.selected)
              ? (states.contains(WidgetState.disabled)
                    ? colors.primaryContainer
                    : colors.primary)
              : colors.surfaceContainer,
        ),
        foregroundColor: WidgetStateProperty.resolveWith(
          (states) => states.contains(WidgetState.selected)
              ? (states.contains(WidgetState.disabled)
                    ? colors.onPrimaryContainer
                    : colors.onPrimary)
              : colors.onSurfaceVariant,
        ),
      ),
    ),
    listTileTheme: ListTileThemeData(
      iconColor: colors.onSurfaceVariant,
      selectedColor: colors.primary,
      minTileHeight: 60,
      shape: pill,
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: colors.surface,
      surfaceTintColor: Colors.transparent,
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(28)),
    ),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: colors.surface,
      showDragHandle: true,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(28)),
      ),
    ),
    popupMenuTheme: PopupMenuThemeData(
      color: colors.surface,
      surfaceTintColor: Colors.transparent,
      shape: pill,
    ),
  );
}
