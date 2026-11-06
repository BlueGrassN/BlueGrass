part of '../main.dart';

// The home dashboard after FlClash v0.9.2's desktop grid: a status card with
// the start button, upload and download cards with live area charts, a
// control row (routing mode, TUN, LAN) and the expiry and session totals.
// Every value, label, disabled condition and action is BlueGrass's own; a
// figure that is not known yet reads "—" or "暂未获取" and no chart is drawn
// from samples that were never taken.

enum _HomeDensity { standard, compact, mobile }

class _HomeMetrics {
  const _HomeMetrics({
    required this.padding,
    required this.gap,
    required this.ipTop,
    required this.cellGap,
    required this.badge,
    required this.name,
    required this.button,
    required this.slot,
    required this.decoration,
    required this.decorationEnd,
    required this.decorationTop,
    required this.topHeight,
    required this.controlHeight,
    required this.figure,
    required this.chart,
    required this.tilePadding,
    required this.tileIcon,
  });

  factory _HomeMetrics.of(_HomeDensity density) => switch (density) {
    _HomeDensity.standard => const _HomeMetrics(
      padding: EdgeInsets.symmetric(horizontal: 22, vertical: 20),
      gap: 16,
      ipTop: 10,
      cellGap: 28,
      badge: 48,
      name: AppTextStyle.title1,
      button: 104,
      slot: 120,
      decoration: 290,
      decorationEnd: -64,
      decorationTop: -87,
      topHeight: 208,
      controlHeight: 64,
      figure: AppTextStyle.display,
      chart: 88,
      tilePadding: EdgeInsets.fromLTRB(12, 0, 16, 0),
      tileIcon: 40,
    ),
    _HomeDensity.compact => const _HomeMetrics(
      padding: EdgeInsets.symmetric(horizontal: 20, vertical: 16),
      gap: 16,
      ipTop: 8,
      cellGap: 22,
      badge: 40,
      name: AppTextStyle.title2,
      button: 84,
      slot: 100,
      decoration: 250,
      decorationEnd: -64,
      decorationTop: -75,
      topHeight: 176,
      controlHeight: 56,
      figure: AppTextStyle.largeTitle,
      chart: 62,
      tilePadding: EdgeInsets.fromLTRB(10, 0, 12, 0),
      tileIcon: 36,
    ),
    _HomeDensity.mobile => const _HomeMetrics(
      padding: EdgeInsets.fromLTRB(20, 18, 16, 18),
      gap: 10,
      ipTop: 12,
      cellGap: 24,
      badge: 44,
      name: AppTextStyle.title2,
      button: 88,
      slot: 88,
      decoration: 240,
      decorationEnd: -60,
      decorationTop: -66,
      topHeight: 0,
      controlHeight: 64,
      figure: AppTextStyle.display,
      chart: 88,
      tilePadding: EdgeInsets.fromLTRB(12, 0, 16, 0),
      tileIcon: 40,
    ),
  };

  final EdgeInsets padding;
  final double gap;
  final double ipTop;
  final double cellGap;
  final double badge;
  final AppTextStyle name;
  final double button;
  final double slot;
  final double decoration;
  final double decorationEnd;
  final double decorationTop;
  final double topHeight;
  final double controlHeight;
  final AppTextStyle figure;
  final double chart;
  final EdgeInsets tilePadding;
  final double tileIcon;
}

const _homeChartBleed = 3.0;
const _homeMorph = Duration(milliseconds: 450);

double _homeScale(BuildContext context) {
  final scale = MediaQuery.textScalerOf(context).scale(1);
  return scale < 1 ? 1 : scale;
}

double _homeTextWidth(BuildContext context, String text, TextStyle style) {
  final painter = TextPainter(
    text: TextSpan(text: text, style: style),
    maxLines: 1,
    textDirection: Directionality.of(context),
    textScaler: MediaQuery.textScalerOf(context),
  )..layout();
  final width = painter.width;
  painter.dispose();
  return width;
}

/// "12.3 KiB/s" as its figure and unit; "—" has no unit.
(String, String?) _homeSplit(String value) {
  final space = value.indexOf(' ');
  return space < 0
      ? (value, null)
      : (value.substring(0, space), value.substring(space + 1));
}

TextStyle _homeCardTitle(BuildContext context) => AppTextStyle.footnote.of(
  context,
  emphasized: true,
  color: Theme.of(context).colorScheme.onSurfaceVariant,
);

TextStyle _homeLabel(BuildContext context, {Color? color}) =>
    AppTextStyle.caption1.of(
      context,
      color: color ?? Theme.of(context).colorScheme.onSurfaceVariant,
    );

TextStyle _homeFigure(
  BuildContext context,
  AppTextStyle style, {
  Color? color,
}) => style
    .of(context, emphasized: true, color: color)
    .copyWith(letterSpacing: -.02 * style.size);

/// Glyph and footnote-weight title of a card.
class _HomeCardHeader extends StatelessWidget {
  const _HomeCardHeader({required this.icon, required this.title});

  final IconData icon;
  final String title;

  @override
  Widget build(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    return ConstrainedBox(
      constraints: const BoxConstraints(minHeight: 24),
      child: Row(
        children: [
          Icon(icon, size: 18, color: s.onSurfaceVariant),
          const SizedBox(width: 8),
          Flexible(
            flex: 3,
            child: Text(
              title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: _homeCardTitle(context),
            ),
          ),
        ],
      ),
    );
  }
}

class _HomeSpeedHistory {
  const _HomeSpeedHistory(this.upload, this.download);

  final List<double> upload;
  final List<double> download;
}

/// Keeps the last 30 speeds the controller actually measured. A sample is
/// taken once per new traffic reading (each native poll yields a new one);
/// readings without a rate are skipped, and the history is dropped as soon as
/// statistics become unavailable, so a new session never continues an old
/// curve.
class _HomeTrafficSampler extends StatefulWidget {
  const _HomeTrafficSampler({
    super.key,
    required this.controller,
    required this.builder,
  });

  final AppController controller;
  final Widget Function(BuildContext context, _HomeSpeedHistory history)
  builder;

  @override
  State<_HomeTrafficSampler> createState() => _HomeTrafficSamplerState();
}

class _HomeTrafficSamplerState extends State<_HomeTrafficSampler> {
  static const _capacity = 30;
  final _upload = <double>[];
  final _download = <double>[];
  domain.TrafficView? _seen;

  @override
  void initState() {
    super.initState();
    _sample();
    widget.controller.addListener(_changed);
  }

  @override
  void didUpdateWidget(_HomeTrafficSampler oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(oldWidget.controller, widget.controller)) {
      oldWidget.controller.removeListener(_changed);
      widget.controller.addListener(_changed);
      _seen = null;
      _upload.clear();
      _download.clear();
      _sample();
    }
  }

  @override
  void dispose() {
    widget.controller.removeListener(_changed);
    super.dispose();
  }

  void _changed() {
    if (_sample() && mounted) setState(() {});
  }

  bool _sample() {
    final traffic = widget.controller.traffic;
    if (identical(traffic, _seen)) return false;
    _seen = traffic;
    if (!traffic.available) {
      if (_upload.isEmpty && _download.isEmpty) return false;
      _upload.clear();
      _download.clear();
      return true;
    }
    var changed = false;
    void add(List<double> samples, double? value) {
      if (value == null || !value.isFinite) return;
      samples.add(value < 0 ? 0 : value);
      if (samples.length > _capacity) samples.removeAt(0);
      changed = true;
    }

    add(_upload, traffic.uploadBytesPerSecond);
    add(_download, traffic.downloadBytesPerSecond);
    return changed;
  }

  @override
  Widget build(BuildContext context) => widget.builder(
    context,
    _HomeSpeedHistory(List.unmodifiable(_upload), List.unmodifiable(_download)),
  );
}

extension _HomePage on _ShellState {
  Future<void> _chooseHomeResource(BuildContext buttonContext) async {
    if (_resourcePickerOpen || c.busy) return;
    final box = buttonContext.findRenderObject() as RenderBox;
    final account = c.customer?.id;
    _resourcePickerOpen = true;
    try {
      final choice = await showHomeResourcePicker(
        context: context,
        anchor: box.localToGlobal(Offset.zero) & box.size,
        controller: c,
      );
      if (!mounted || choice == null || c.busy || c.customer?.id != account) {
        return;
      }
      final current = c.nodes
          .where((node) => node.key == choice.key)
          .firstOrNull;
      if (current != null &&
          resourceUnavailableReason(current, c.effectiveNowUtc) == null) {
        await _select(current);
      }
    } finally {
      _resourcePickerOpen = false;
    }
  }

  String get _homeLockText =>
      c.active ? '请断开连接后切换路由或 TUN 模式。' : '当前操作尚未完成，请稍候再切换模式。';

  String get _homeModeLabel =>
      switch (c.actualMode != 'none' ? c.actualMode : c.preferences.mode) {
        'tun' => 'TUN 接管',
        'system_proxy' => '系统代理',
        _ => '接管模式待确认',
      };

  Widget _home() => _HomeTrafficSampler(
    key: const ValueKey('home-dashboard'),
    controller: c,
    builder: (context, history) => LayoutBuilder(
      builder: (context, box) => _homeDashboard(context, box.maxWidth, history),
    ),
  );

  Widget _homeDashboard(
    BuildContext context,
    double width,
    _HomeSpeedHistory history,
  ) {
    final s = Theme.of(context).colorScheme;
    final scale = _homeScale(context);
    // Breakpoints are measured in unscaled text, as FlClash does, so larger
    // text moves to fewer columns instead of squeezing four.
    final room = width / scale;
    final columns = room >= 860
        ? 4
        : room >= 640
        ? 2
        : 1;
    final m = _HomeMetrics.of(
      columns == 1
          ? _HomeDensity.mobile
          : columns == 4 && room < 1040
          ? _HomeDensity.compact
          : _HomeDensity.standard,
    );
    const gap = 16.0;
    final cap = (columns == 1 ? 560 : 440) * scale;
    final fit = (width - gap * (columns - 1)) / columns;
    final column = fit < cap ? fit : cap;
    double span(int count) => column * count + gap * (count - 1);
    final full = span(columns);
    final selected = c.selected;
    final node =
        selected != null &&
            domain.isResourceVisible(selected.expiresAtUtc, c.effectiveNowUtc)
        ? selected
        : null;

    final statusWidth = columns == 1 ? full : span(2);
    final status = _homeStatusCard(context, m, node, statusWidth, scale);
    final upload = _homeSpeedCard(context, m, history.upload, upload: true);
    final download = _homeSpeedCard(
      context,
      m,
      history.download,
      upload: false,
    );
    final routing = _homeRoutingBar(context, m, scale);
    // Single column pairs the speeds and the switches while both still fit.
    final pair = columns == 1 && full >= 420 * scale;
    final half = (full - gap) / 2;
    final tileWidth = columns == 1 ? (pair ? half : full) : column;
    final tileIcons = _homeTileIconsFit(context, m, tileWidth);
    final tun = _homeTunTile(context, m, tileIcons);
    final lan = _homeLanTile(context, m, tileIcons);
    final expiry = _homeExpiryCard(context, node);
    final total = _homeTotalCard(context);
    final top = m.topHeight * scale;
    final control = m.controlHeight * scale;

    Widget even(List<(double, Widget)> cards, {double minHeight = 0}) =>
        IntrinsicHeight(
          child: Row(
            mainAxisAlignment: MainAxisAlignment.center,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            spacing: gap,
            children: [
              for (final (cardWidth, card) in cards)
                SizedBox(
                  width: cardWidth,
                  child: ConstrainedBox(
                    constraints: BoxConstraints(minHeight: minHeight),
                    child: card,
                  ),
                ),
            ],
          ),
        );
    Widget fixed(List<(double, Widget)> cards) => SizedBox(
      height: control,
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        spacing: gap,
        children: [
          for (final (cardWidth, card) in cards)
            SizedBox(width: cardWidth, child: card),
        ],
      ),
    );
    Widget aligned(Widget child) => Center(
      child: SizedBox(width: full, child: child),
    );

    final lock = c.active || c.busy
        ? aligned(
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 8),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Padding(
                    padding: const EdgeInsets.only(top: 1),
                    child: Icon(
                      Icons.lock_outline_rounded,
                      size: 15,
                      color: s.onSurfaceVariant,
                    ),
                  ),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(_homeLockText, style: _homeLabel(context)),
                  ),
                ],
              ),
            ),
          )
        : null;

    final rows = <Widget>[
      ...switch (columns) {
        4 => [
          even([
            (span(2), status),
            (column, upload),
            (column, download),
          ], minHeight: top),
          fixed([(span(2), routing), (column, tun), (column, lan)]),
        ],
        2 => [
          even([(full, status)], minHeight: top),
          even([(column, upload), (column, download)], minHeight: top),
          fixed([(full, routing)]),
          fixed([(column, tun), (column, lan)]),
        ],
        _ => [
          even([(full, status)]),
          if (pair)
            even([(half, upload), (half, download)])
          else ...[
            even([(full, upload)]),
            even([(full, download)]),
          ],
          fixed([(full, routing)]),
          if (pair)
            fixed([(half, tun), (half, lan)])
          else ...[
            fixed([(full, tun)]),
            fixed([(full, lan)]),
          ],
        ],
      },
    ];
    final tail = columns == 1
        ? [
            even([(full, expiry)]),
            even([(full, total)]),
          ]
        : [
            even([
              (columns == 4 ? span(2) : column, expiry),
              (columns == 4 ? span(2) : column, total),
            ]),
          ];

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < rows.length; i++) ...[
          if (i > 0) const SizedBox(height: gap),
          rows[i],
        ],
        if (lock != null) ...[const SizedBox(height: 10), lock],
        for (final row in tail) ...[const SizedBox(height: gap), row],
        const SizedBox(height: 16),
        aligned(
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8),
            child: Text(
              c.demo
                  ? '系统代理仅影响遵循系统代理设置的应用。'
                  : DesktopPlatform.current.isMac
                  ? 'TUN 连接需要管理员授权；系统代理仅影响遵循代理设置的应用。连接后检测实际出口。'
                  : '系统代理仅影响遵循代理设置的应用；TUN 接管需后台服务。连接后通过所选节点检测实际出口。',
              style: AppTextStyle.footnote.of(
                context,
                color: s.onSurfaceVariant,
              ),
            ),
          ),
        ),
      ],
    );
  }

  Widget _homeStatusCard(
    BuildContext context,
    _HomeMetrics m,
    domain.NodeRecord? node,
    double width,
    double scale,
  ) {
    final s = Theme.of(context).colorScheme;
    final running = c.active;
    final card = SurfaceLevel.card.colorOf(s);
    final block = SurfaceLevel.block.colorOf(s);
    final inner = width - m.padding.horizontal - m.slot - m.gap;
    final label = c.connecting
        ? '取消连接'
        : c.connected
        ? '断开连接'
        : c.active
        ? '恢复并断开代理'
        : '连接';
    final VoidCallback? onPressed = (node == null && !c.active) || c.busy
        ? null
        : () => c.active ? c.disconnect() : _manualConnect();
    final start = StartToggleButton(
      running: running,
      onPressed: onPressed,
      icon: c.connected
          ? Icons.pause_rounded
          : c.active
          ? Icons.stop_rounded
          : Icons.play_arrow_rounded,
      label: label,
      size: m.button,
    );
    final choose = Builder(
      builder: (buttonContext) => FilledButton.tonalIcon(
        onPressed: c.busy ? null : () => _chooseHomeResource(buttonContext),
        icon: const Icon(Icons.expand_more_rounded, size: 18),
        label: const Text('选择资源'),
      ),
    );
    // A footer this narrow puts the resource button on a line of its own.
    final chooseInline = inner >= 400;
    return DashCard(
      padding: EdgeInsets.zero,
      child: Stack(
        children: [
          PositionedDirectional(
            end: m.decorationEnd,
            top: m.decorationTop,
            child: TweenAnimationBuilder<double>(
              tween: Tween(end: running ? 1 : 0),
              duration: _homeMorph,
              curve: Curves.easeOutBack,
              builder: (context, t, _) => Transform.rotate(
                angle: t * 0.5235987755982988,
                child: Transform.scale(
                  scale: 1 + .12 * t,
                  child: CookieBadge(
                    size: m.decoration,
                    lobes: 12,
                    depth: .1,
                    sharpness: 1.2,
                    color: Color.lerp(
                      block,
                      Color.alphaBlend(s.primary.withValues(alpha: .1), card),
                      t.clamp(0.0, 1.0),
                    )!,
                  ),
                ),
              ),
            ),
          ),
          Padding(
            padding: m.padding,
            child: Row(
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      _homeStatusHeader(context),
                      SizedBox(height: m.ipTop),
                      _homeResourceRow(context, m, node, inner, scale),
                      const SizedBox(height: 14),
                      const Spacer(),
                      Row(
                        crossAxisAlignment: CrossAxisAlignment.end,
                        children: [
                          Expanded(child: _homeStatusCells(context, m)),
                          if (chooseInline) ...[
                            const SizedBox(width: 12),
                            choose,
                          ],
                        ],
                      ),
                      if (!chooseInline) ...[
                        const SizedBox(height: 12),
                        choose,
                      ],
                    ],
                  ),
                ),
                SizedBox(width: m.gap),
                SizedBox(
                  width: m.slot,
                  child: Center(child: start),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _homeStatusHeader(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final (tone, dot) = c.connected
        ? (ConnectionTone.ok, true)
        : c.connecting
        ? (ConnectionTone.warn, true)
        : (ConnectionTone.idle, false);
    return ConstrainedBox(
      constraints: const BoxConstraints(minHeight: 24),
      child: Row(
        children: [
          Icon(
            Icons.monitor_heart_outlined,
            size: 18,
            color: s.onSurfaceVariant,
          ),
          const SizedBox(width: 8),
          Text('状态', style: _homeCardTitle(context)),
          const SizedBox(width: 8),
          Expanded(
            child: Wrap(
              spacing: 6,
              runSpacing: 4,
              children: [
                ConnectionStatusChip(c.connectionLabel, tone: tone, dot: dot),
                ConnectionStatusChip(
                  _homeModeLabel,
                  textKey: const ValueKey('connection-mode'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _homeResourceRow(
    BuildContext context,
    _HomeMetrics m,
    domain.NodeRecord? node,
    double inner,
    double scale,
  ) {
    final s = Theme.of(context).colorScheme;
    final ink = c.connected ? s.onSurface : s.onSurfaceVariant;
    final code = (node?.countryCode ?? '').trim().toUpperCase();
    final badge = CookieBadge(
      size: m.badge,
      lobes: 4,
      depth: .2,
      sharpness: 2,
      rotation: 0.7853981633974483,
      color: SurfaceLevel.inset.colorOf(s),
      child: code.isEmpty
          ? Icon(Icons.public_rounded, size: m.badge * .42, color: ink)
          : Padding(
              padding: EdgeInsets.all(m.badge * .16),
              child: FittedBox(
                fit: BoxFit.scaleDown,
                child: Text(
                  code,
                  maxLines: 1,
                  style: AppTextStyle.footnote
                      .of(context, emphasized: true, color: ink)
                      .copyWith(
                        letterSpacing: .04 * AppTextStyle.footnote.size,
                      ),
                ),
              ),
            ),
    );
    final name = node?.name ?? (c.loading ? '正在同步资源' : '选择连接资源');
    // Past its room the name shrinks to 75% before it is cut short.
    final room = inner - m.badge - 12;
    Widget title = FittedBox(
      fit: BoxFit.scaleDown,
      alignment: AlignmentDirectional.centerStart,
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: room > 0 ? room / .75 : 0),
        child: Text(
          name,
          maxLines: 1,
          softWrap: false,
          overflow: TextOverflow.ellipsis,
          style: _homeFigure(
            context,
            m.name,
            color: node == null ? s.onSurfaceVariant : s.onSurface,
          ),
        ),
      ),
    );
    if (node != null) title = Tooltip(message: node.name, child: title);
    final region = node == null
        ? ''
        : [node.countryName, node.city].where((v) => v.isNotEmpty).join(' / ');
    return Row(
      children: [
        badge,
        const SizedBox(width: 12),
        Expanded(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              SizedBox(
                height: m.name.leading * scale,
                child: Align(
                  alignment: AlignmentDirectional.centerStart,
                  child: title,
                ),
              ),
              const SizedBox(height: 4),
              if (node == null)
                Text('前往“我的 IP”查看可用资源', style: _homeLabel(context))
              else
                Wrap(
                  spacing: 6,
                  runSpacing: 6,
                  children: [
                    if (region.isNotEmpty) ConnectionStatusChip(region),
                    ConnectionStatusChip(node.protocol.toUpperCase()),
                  ],
                ),
            ],
          ),
        ),
      ],
    );
  }

  Widget _homeStatusCells(BuildContext context, _HomeMetrics m) =>
      ConnectionContent(
        spacing: m.cellGap,
        data: ConnectionViewData(
          exitIp: c.connected ? c.actualExitIp : null,
          localProxy: c.localProxyAddress,
          detail: c.connected && c.actualExitIp == null
              ? '节点出口 IP 暂未获取，连接保持生效；可在“诊断”中重试 HTTPS 检测。'
              : null,
        ),
      );

  Widget _homeSpeedCard(
    BuildContext context,
    _HomeMetrics m,
    List<double> samples, {
    required bool upload,
  }) {
    final s = Theme.of(context).colorScheme;
    final card = SurfaceLevel.card.colorOf(s);
    final traffic = c.traffic;
    final (figure, unit) = _homeSplit(
      _speed(
        upload ? traffic.uploadBytesPerSecond : traffic.downloadBytesPerSecond,
      ),
    );
    final color = upload
        ? Color.alphaBlend(s.primary.withValues(alpha: .45), card)
        : s.primary;
    final chart = traffic.available && samples.length >= 2;
    return DashCard(
      padding: EdgeInsets.zero,
      child: Stack(
        children: [
          if (chart)
            Positioned(
              left: -_homeChartBleed,
              right: -_homeChartBleed,
              bottom: -_homeChartBleed,
              height: m.chart,
              child: SpeedChart(values: samples, color: color),
            ),
          Padding(
            padding: m.padding,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                TrafficMetricContent(
                  data: TrafficMetricData(
                    label: upload ? '上传速度' : '下载速度',
                    icon: upload
                        ? Icons.arrow_upward_rounded
                        : Icons.arrow_downward_rounded,
                    value: figure,
                    unit: unit ?? '',
                    detail:
                        '本次 ${_bytes(upload ? traffic.uploadBytes : traffic.downloadBytes)}',
                  ),
                  color: upload ? null : s.primary,
                  figureStyle: _homeFigure(context, m.figure),
                  inlineUnit: true,
                ),
                // The chart's band at the bottom of the card.
                SizedBox(height: m.chart - m.padding.bottom + 4),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _homeRoutingSegments(
    BuildContext context, {
    required double outer,
    required double height,
  }) => RoutingModeControl(
    value: c.preferences.routingMode,
    outer: outer,
    height: height,
    onChanged: c.active || c.busy
        ? null
        : (value) =>
              c.updatePreferences(c.preferences.copyWith(routingMode: value)),
  );

  Widget _homeRoutingBar(BuildContext context, _HomeMetrics m, double scale) {
    const inset = 8.0;
    final s = Theme.of(context).colorScheme;
    return DashCard(
      padding: EdgeInsetsDirectional.fromSTEB(
        m.padding.left,
        inset,
        inset,
        inset,
      ),
      child: LayoutBuilder(
        builder: (context, box) => Row(
          children: [
            Icon(Icons.alt_route_rounded, size: 18, color: s.onSurfaceVariant),
            const SizedBox(width: 16),
            ConstrainedBox(
              constraints: BoxConstraints(maxWidth: box.maxWidth / 4),
              child: Text(
                '路由模式',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: _homeCardTitle(context),
              ),
            ),
            const SizedBox(width: 16),
            Expanded(
              child: _homeRoutingSegments(
                context,
                outer: AppCorner.dashboardCard - inset,
                height: box.maxHeight,
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// Whether every switch tile keeps its icon at [width]: they drop it
  /// together, before any title or state is cut short.
  bool _homeTileIconsFit(BuildContext context, _HomeMetrics m, double width) {
    const gap = 12.0;
    const track = 60.0;
    final titleStyle = AppTextStyle.subheadline.of(context, emphasized: true);
    final subtitleStyle = _homeLabel(context);
    var text = 0.0;
    for (final (value, style) in [
      ('TUN 模式', titleStyle),
      ('允许局域网', titleStyle),
      ('已开启', subtitleStyle),
      ('已关闭', subtitleStyle),
      (_takeoverModeSummary(c), subtitleStyle),
    ]) {
      final measured = _homeTextWidth(context, value, style);
      if (measured > text) text = measured;
    }
    return width - m.tilePadding.horizontal - m.tileIcon - gap * 2 - track >=
        text;
  }

  Widget _homeTunTile(BuildContext context, _HomeMetrics m, bool showIcon) {
    final on = c.preferences.mode == 'tun';
    return _homeSwitchTile(
      context,
      m,
      showIcon: showIcon,
      switchKey: const ValueKey('tun-mode-button'),
      icon: on ? Icons.shield_rounded : Icons.shield_outlined,
      title: 'TUN 模式',
      subtitle: _takeoverModeSummary(c),
      value: on,
      onChanged: !DesktopPlatform.current.supportsTun || c.active || c.busy
          ? null
          : (value) => c.updatePreferences(
              c.preferences.copyWith(mode: value ? 'tun' : 'system_proxy'),
            ),
    );
  }

  Widget _homeLanTile(BuildContext context, _HomeMetrics m, bool showIcon) {
    final on = c.preferences.allowLan;
    return _homeSwitchTile(
      context,
      m,
      showIcon: showIcon,
      icon: Icons.lan_outlined,
      title: '允许局域网',
      subtitle: on ? '已开启' : '已关闭',
      value: on,
      onChanged: c.active
          ? null
          : (value) =>
                c.updatePreferences(c.preferences.copyWith(allowLan: value)),
    );
  }

  /// A control-row tile: the whole card toggles [value], and the [Switch]
  /// alone takes focus and carries the semantics.
  Widget _homeSwitchTile(
    BuildContext context,
    _HomeMetrics m, {
    required bool showIcon,
    Key? switchKey,
    required IconData icon,
    required String title,
    required String subtitle,
    required bool value,
    required ValueChanged<bool>? onChanged,
  }) {
    final s = Theme.of(context).colorScheme;
    const gap = 12.0;
    final badge = value
        ? CookieBadge(
            key: const ValueKey(true),
            size: m.tileIcon,
            lobes: 6,
            depth: .16,
            color: onChanged == null
                ? s.onSurface.withValues(alpha: .38)
                : s.primary,
            child: Icon(icon, size: m.tileIcon * .5, color: s.onPrimary),
          )
        : CookieBadge(
            key: const ValueKey(false),
            size: m.tileIcon,
            depth: 0,
            color: SurfaceLevel.block.colorOf(s),
            child: Icon(icon, size: m.tileIcon * .5, color: s.onSurfaceVariant),
          );
    return DashCard(
      padding: EdgeInsets.zero,
      child: InkWell(
        customBorder: AppShape.all(AppCorner.dashboardCard),
        canRequestFocus: false,
        excludeFromSemantics: true,
        onTap: onChanged == null ? null : () => onChanged(!value),
        child: Padding(
          padding: showIcon
              ? m.tilePadding
              : m.tilePadding.copyWith(left: m.padding.left),
          child: Row(
            children: [
              if (showIcon) ...[
                AnimatedSwitcher(
                  duration: const Duration(milliseconds: 300),
                  child: badge,
                ),
                const SizedBox(width: gap),
              ],
              Expanded(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: AppTextStyle.subheadline.of(
                        context,
                        emphasized: true,
                      ),
                    ),
                    Text(
                      subtitle,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: _homeLabel(context),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: gap),
              Switch(key: switchKey, value: value, onChanged: onChanged),
            ],
          ),
        ),
      ),
    );
  }

  Widget _homeExpiryCard(BuildContext context, domain.NodeRecord? node) =>
      DashCard(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            const _HomeCardHeader(
              icon: Icons.event_outlined,
              title: '到期时间 · 北京时间',
            ),
            const SizedBox(height: 10),
            Text(
              _expiry(node?.expiresAtUtc, noResource: node == null),
              style: AppTextStyle.title3.of(context, emphasized: true),
            ),
          ],
        ),
      );

  Widget _homeTotalCard(BuildContext context) {
    final s = Theme.of(context).colorScheme;
    final traffic = c.traffic;
    final (figure, unit) = _homeSplit(_bytes(traffic.totalBytes));
    return DashCard(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          const _HomeCardHeader(icon: Icons.data_usage_rounded, title: '本次总流量'),
          const SizedBox(height: 10),
          Text.rich(
            TextSpan(
              children: [
                TextSpan(
                  text: figure,
                  style: _homeFigure(context, AppTextStyle.title2),
                ),
                if (unit != null)
                  TextSpan(
                    text: ' $unit',
                    style: AppTextStyle.subheadline.of(
                      context,
                      emphasized: true,
                      color: s.onSurfaceVariant,
                    ),
                  ),
              ],
            ),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
          const SizedBox(height: 4),
          Text(
            !traffic.available
                ? '统计暂不可用'
                : !traffic.complete
                ? '本次统计不完整'
                : '本地代理业务统计 · 非官网计费数据',
            style: _homeLabel(context),
          ),
        ],
      ),
    );
  }
}
