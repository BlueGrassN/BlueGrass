import 'package:flutter/material.dart';
import 'package:flutter/foundation.dart';

import 'ui/kit.dart';

// Colours follow FlClash v0.9.2's seeded "vivid" style with the BlueGrass brand
// teal (#087E98) as the seed: primary keeps the seed (dark lifts it 3 tones),
// surfaces stay near grey (chroma <= 3, paper <= 1.5), primaryContainer comes
// from the primary palette at tone 90/30 with chroma <= 48, and dark mode uses
// the higher-contrast tones of v0.9.2 (onSurface 97, card 15, block 21,
// inset 27). Values were computed once with material_color_utilities.
const _light = ColorScheme(
  brightness: Brightness.light,
  primary: Color(0xFF087E98),
  onPrimary: Color(0xFFFFFFFF),
  primaryContainer: Color(0xFFB3EBFF),
  onPrimaryContainer: Color(0xFF004E5F),
  secondary: Color(0xFF44636E),
  onSecondary: Color(0xFFFFFFFF),
  secondaryContainer: Color(0xFFC4E5F2),
  onSecondaryContainer: Color(0xFF486772),
  tertiary: Color(0xFF734B89),
  onTertiary: Color(0xFFFFFFFF),
  tertiaryContainer: Color(0xFF8D63A3),
  onTertiaryContainer: Color(0xFFFFF9FB),
  error: Color(0xFFBA1A1A),
  onError: Color(0xFFFFFFFF),
  errorContainer: Color(0xFFFFDAD6),
  onErrorContainer: Color(0xFF93000A),
  outline: Color(0xFF6E797D),
  outlineVariant: Color(0xFFC5C7C8),
  onSurface: Color(0xFF181C1E),
  onSurfaceVariant: Color(0xFF3E484C),
  inverseSurface: Color(0xFF2D3133),
  onInverseSurface: Color(0xFFEEF1F3),
  inversePrimary: Color(0xFF79D3EF),
  surfaceTint: Color(0xFF087E98),
  surface: Color(0xFFFBF9F9),
  surfaceBright: Color(0xFFFBF9F9),
  surfaceDim: Color(0xFFDADADB),
  surfaceContainerLowest: Color(0xFFFFFFFF),
  surfaceContainerLow: Color(0xFFF3F3F4),
  surfaceContainer: Color(0xFFEEEEEE),
  surfaceContainerHigh: Color(0xFFE8E8E9),
  surfaceContainerHighest: Color(0xFFE2E2E3),
  shadow: Color(0xFF000000),
  scrim: Color(0xFF000000),
);

const _dark = ColorScheme(
  brightness: Brightness.dark,
  primary: Color(0xFF1C86A0),
  onPrimary: Color(0xFFFFFFFF),
  primaryContainer: Color(0xFF004E5F),
  onPrimaryContainer: Color(0xFFB3EBFF),
  secondary: Color(0xFFABCCD8),
  onSecondary: Color(0xFF14343E),
  secondaryContainer: Color(0xFF2E4D58),
  onSecondaryContainer: Color(0xFF9DBECA),
  tertiary: Color(0xFFE5B5FC),
  onTertiary: Color(0xFF451F5B),
  tertiaryContainer: Color(0xFF8D63A3),
  onTertiaryContainer: Color(0xFFFFF9FB),
  error: Color(0xFFFFB4AB),
  onError: Color(0xFF690005),
  errorContainer: Color(0xFF93000A),
  onErrorContainer: Color(0xFFFFDAD6),
  outline: Color(0xFF889297),
  outlineVariant: Color(0xFF444749),
  onSurface: Color(0xFFF4F7F9),
  onSurfaceVariant: Color(0xFFBEC8CD),
  inverseSurface: Color(0xFFDFE3E5),
  onInverseSurface: Color(0xFF2D3133),
  inversePrimary: Color(0xFF00677D),
  surfaceTint: Color(0xFF1C86A0),
  surface: Color(0xFF121415),
  surfaceBright: Color(0xFF38393A),
  surfaceDim: Color(0xFF121415),
  surfaceContainerLowest: Color(0xFF0C0E0F),
  surfaceContainerLow: Color(0xFF1A1C1D),
  surfaceContainer: Color(0xFF242627),
  surfaceContainerHigh: Color(0xFF313334),
  surfaceContainerHighest: Color(0xFF3E4041),
  shadow: Color(0xFF000000),
  scrim: Color(0xFF000000),
);

String? get _font => switch (defaultTargetPlatform) {
  TargetPlatform.windows => 'Microsoft YaHei',
  TargetPlatform.macOS => 'PingFang SC',
  _ => null,
};

ColorScheme blueGrassColorScheme(Brightness brightness) =>
    brightness == Brightness.dark ? _dark : _light;

TextTheme _scale(TextTheme t) => t.copyWith(
  displayLarge: t.displayLarge?.merge(AppTextStyle.largeTitle.style),
  displayMedium: t.displayMedium?.merge(AppTextStyle.largeTitle.style),
  displaySmall: t.displaySmall?.merge(AppTextStyle.largeTitle.style),
  headlineLarge: t.headlineLarge?.merge(AppTextStyle.largeTitle.style),
  headlineMedium: t.headlineMedium?.merge(AppTextStyle.title1.style),
  headlineSmall: t.headlineSmall?.merge(AppTextStyle.title2.style),
  titleLarge: t.titleLarge?.merge(AppTextStyle.title3.style),
  titleMedium: t.titleMedium?.merge(AppTextStyle.headline.style),
  titleSmall: t.titleSmall?.merge(
    AppTextStyle.subheadline.style.copyWith(fontWeight: FontWeight.w600),
  ),
  bodyLarge: t.bodyLarge?.merge(AppTextStyle.subheadline.style),
  bodyMedium: t.bodyMedium?.merge(AppTextStyle.subheadline.style),
  bodySmall: t.bodySmall?.merge(AppTextStyle.footnote.style),
  labelLarge: t.labelLarge?.merge(
    AppTextStyle.subheadline.style.copyWith(fontWeight: FontWeight.w600),
  ),
  labelMedium: t.labelMedium?.merge(
    AppTextStyle.caption1.style.copyWith(fontWeight: FontWeight.w600),
  ),
  labelSmall: t.labelSmall?.merge(
    AppTextStyle.caption2.style.copyWith(fontWeight: FontWeight.w600),
  ),
);

/// FlClash-style theme: grey page, near-white cards, pill buttons, superellipse
/// corners, no structural outlines.
ThemeData blueGrassTheme(Brightness brightness, {bool touch = false}) {
  final colors = blueGrassColorScheme(brightness);
  final dark = brightness == Brightness.dark;
  final page = SurfaceLevel.page.colorOf(colors);
  final card = SurfaceLevel.card.colorOf(colors);
  final floating = SurfaceLevel.floatingOf(colors).colorOf(colors);
  final base = ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme: colors,
    fontFamily: _font,
    scaffoldBackgroundColor: page,
    canvasColor: page,
    visualDensity: VisualDensity.standard,
  );
  const pill = WidgetStatePropertyAll<OutlinedBorder>(StadiumBorder());
  final buttonText = AppTextStyle.subheadline.style.copyWith(
    fontFamily: _font,
    fontWeight: FontWeight.w600,
  );
  final inputBorder = OutlineInputBorder(
    borderRadius: BorderRadius.circular(AppCorner.md),
    borderSide: BorderSide(color: colors.outlineVariant),
  );
  return base.copyWith(
    appBarTheme: AppBarTheme(
      backgroundColor: page,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      scrolledUnderElevation: 0,
      centerTitle: false,
    ),
    materialTapTargetSize: touch
        ? MaterialTapTargetSize.padded
        : base.materialTapTargetSize,
    textTheme: _scale(base.textTheme),
    primaryTextTheme: _scale(base.primaryTextTheme),
    cardTheme: CardThemeData(
      color: card,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      margin: EdgeInsets.zero,
      clipBehavior: Clip.antiAlias,
      shape: AppShape.all(AppCorner.xl),
    ),
    dividerTheme: DividerThemeData(
      color: colors.hairline,
      thickness: 1,
      space: 1,
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: false,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 16),
      border: inputBorder,
      enabledBorder: inputBorder,
      disabledBorder: inputBorder.copyWith(
        borderSide: BorderSide(color: colors.onSurface.withValues(alpha: .08)),
      ),
      focusedBorder: inputBorder.copyWith(
        borderSide: BorderSide(color: colors.primary, width: 2),
      ),
      prefixIconColor: colors.onSurfaceVariant,
      suffixIconColor: colors.onSurfaceVariant,
      labelStyle: TextStyle(color: colors.onSurfaceVariant, fontSize: 15),
      hintStyle: TextStyle(color: colors.onSurfaceVariant, fontSize: 15),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: ButtonStyle(
        shape: pill,
        elevation: const WidgetStatePropertyAll(0),
        minimumSize: const WidgetStatePropertyAll(Size(0, 40)),
        padding: const WidgetStatePropertyAll(
          EdgeInsets.symmetric(horizontal: 20, vertical: 10),
        ),
        textStyle: WidgetStatePropertyAll(buttonText),
      ),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      // FlClash draws no outlines: a secondary button is a tonal pill.
      style: ButtonStyle(
        shape: pill,
        minimumSize: const WidgetStatePropertyAll(Size(0, 40)),
        padding: const WidgetStatePropertyAll(
          EdgeInsets.symmetric(horizontal: 18, vertical: 10),
        ),
        textStyle: WidgetStatePropertyAll(buttonText),
        side: const WidgetStatePropertyAll(BorderSide.none),
        backgroundColor: WidgetStateProperty.resolveWith(
          (states) => states.contains(WidgetState.disabled)
              ? colors.onSurface.withValues(alpha: .04)
              : colors.tonalFill,
        ),
        foregroundColor: WidgetStateProperty.resolveWith(
          (states) => states.contains(WidgetState.disabled)
              ? colors.onSurface.withValues(alpha: .38)
              : colors.onSurface,
        ),
        iconColor: WidgetStateProperty.resolveWith(
          (states) => states.contains(WidgetState.disabled)
              ? colors.onSurface.withValues(alpha: .38)
              : colors.primary,
        ),
      ),
    ),
    textButtonTheme: TextButtonThemeData(
      style: ButtonStyle(
        shape: pill,
        textStyle: WidgetStatePropertyAll(buttonText),
        padding: const WidgetStatePropertyAll(
          EdgeInsets.symmetric(horizontal: 14, vertical: 8),
        ),
      ),
    ),
    iconButtonTheme: const IconButtonThemeData(
      style: ButtonStyle(
        shape: WidgetStatePropertyAll(CircleBorder()),
        iconSize: WidgetStatePropertyAll(20),
      ),
    ),
    segmentedButtonTheme: SegmentedButtonThemeData(
      // The chosen segment is solid primary; the rest are tonal fills.
      style: ButtonStyle(
        shape: pill,
        side: WidgetStatePropertyAll(
          BorderSide(color: colors.surface.withValues(alpha: 0), width: 0),
        ),
        padding: const WidgetStatePropertyAll(
          EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        ),
        textStyle: WidgetStatePropertyAll(buttonText),
        backgroundColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.selected)) {
            return states.contains(WidgetState.disabled)
                ? colors.onSurface.withValues(alpha: .12)
                : colors.primary;
          }
          return colors.tonalFill;
        }),
        foregroundColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.disabled)) {
            return colors.onSurface.withValues(alpha: .38);
          }
          return states.contains(WidgetState.selected)
              ? colors.onPrimary
              : colors.onSurface;
        }),
        iconColor: WidgetStateProperty.resolveWith((states) {
          if (states.contains(WidgetState.disabled)) {
            return colors.onSurface.withValues(alpha: .38);
          }
          return states.contains(WidgetState.selected)
              ? colors.onPrimary
              : colors.onSurfaceVariant;
        }),
      ),
    ),
    switchTheme: SwitchThemeData(
      trackOutlineColor: const WidgetStatePropertyAll(Colors.transparent),
      trackOutlineWidth: const WidgetStatePropertyAll(0),
      thumbColor: WidgetStateProperty.resolveWith((states) {
        if (states.contains(WidgetState.selected)) {
          return states.contains(WidgetState.disabled)
              ? colors.surface
              : colors.onPrimary;
        }
        return states.contains(WidgetState.disabled)
            ? colors.onSurface.withValues(alpha: .3)
            : colors.outline;
      }),
      trackColor: WidgetStateProperty.resolveWith((states) {
        if (states.contains(WidgetState.selected)) {
          return states.contains(WidgetState.disabled)
              ? colors.onSurface.withValues(alpha: .12)
              : colors.primary;
        }
        return colors.onSurface.withValues(alpha: dark ? .16 : .1);
      }),
    ),
    listTileTheme: ListTileThemeData(
      iconColor: colors.onSurfaceVariant,
      selectedColor: colors.primary,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16),
      minVerticalPadding: 10,
      titleTextStyle: AppTextStyle.subheadline.style.copyWith(
        fontFamily: _font,
        color: colors.onSurface,
        fontWeight: FontWeight.w500,
      ),
      subtitleTextStyle: AppTextStyle.footnote.style.copyWith(
        fontFamily: _font,
        color: colors.onSurfaceVariant,
      ),
      shape: AppShape.all(AppCorner.lg),
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: floating,
      surfaceTintColor: Colors.transparent,
      indicatorColor: SurfaceLevel.floatingOf(colors).next.colorOf(colors),
      indicatorShape: const StadiumBorder(),
    ),
    dialogTheme: DialogThemeData(
      backgroundColor: floating,
      surfaceTintColor: Colors.transparent,
      shape: AppShape.all(AppCorner.alert),
      elevation: 8,
      titleTextStyle: AppTextStyle.headline.style.copyWith(
        fontFamily: _font,
        color: colors.onSurface,
      ),
      contentTextStyle: AppTextStyle.footnote.style.copyWith(
        fontFamily: _font,
        color: colors.onSurfaceVariant,
        height: 1.55,
      ),
    ),
    bannerTheme: MaterialBannerThemeData(
      backgroundColor: colors.warningContainer,
      contentTextStyle: AppTextStyle.footnote.style.copyWith(
        fontFamily: _font,
        color: colors.onWarningContainer,
      ),
      dividerColor: Colors.transparent,
    ),
    snackBarTheme: SnackBarThemeData(
      behavior: SnackBarBehavior.floating,
      shape: AppShape.all(AppCorner.md),
      backgroundColor: colors.inverseSurface,
      contentTextStyle: AppTextStyle.footnote.style.copyWith(
        fontFamily: _font,
        color: colors.onInverseSurface,
      ),
    ),
    popupMenuTheme: PopupMenuThemeData(
      shape: AppShape.all(AppCorner.md),
      color: floating,
      surfaceTintColor: Colors.transparent,
    ),
    menuTheme: MenuThemeData(
      style: MenuStyle(
        shape: WidgetStatePropertyAll(AppShape.all(AppCorner.md)),
        backgroundColor: WidgetStatePropertyAll(floating),
        surfaceTintColor: const WidgetStatePropertyAll(Colors.transparent),
      ),
    ),
    dropdownMenuTheme: DropdownMenuThemeData(
      menuStyle: MenuStyle(
        shape: WidgetStatePropertyAll(AppShape.all(AppCorner.md)),
        backgroundColor: WidgetStatePropertyAll(floating),
        surfaceTintColor: const WidgetStatePropertyAll(Colors.transparent),
      ),
    ),
    tooltipTheme: TooltipThemeData(
      decoration: ShapeDecoration(
        shape: AppShape.all(AppCorner.xs),
        color: dark
            ? Colors.white.withAlpha(230)
            : Colors.grey[700]!.withAlpha(230),
      ),
    ),
    chipTheme: ChipThemeData(shape: AppShape.all(AppCorner.sm)),
    progressIndicatorTheme: const ProgressIndicatorThemeData(
      borderRadius: BorderRadius.all(Radius.circular(1000)),
    ),
    scrollbarTheme: ScrollbarThemeData(
      thumbColor: WidgetStatePropertyAll(
        colors.onSurface.withValues(alpha: .24),
      ),
      radius: const Radius.circular(8),
    ),
  );
}
