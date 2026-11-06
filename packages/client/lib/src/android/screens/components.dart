import 'package:flutter/material.dart';

export '../surface_style.dart' show SurfaceCard;

class NoticeCard extends StatelessWidget {
  const NoticeCard(this.message, {super.key, this.onRetry, this.error = false});
  final String message;
  final VoidCallback? onRetry;
  final bool error;
  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return Semantics(
      liveRegion: error,
      child: Container(
        padding: const EdgeInsets.all(16),
        decoration: BoxDecoration(
          color: error ? colors.errorContainer : colors.surfaceContainer,
          borderRadius: BorderRadius.circular(20),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              message,
              style: TextStyle(
                color: error
                    ? colors.onErrorContainer
                    : colors.onSurfaceVariant,
                height: 1.5,
              ),
            ),
            if (onRetry != null)
              TextButton(onPressed: onRetry, child: const Text('重试')),
          ],
        ),
      ),
    );
  }
}

class PageHeader extends StatelessWidget {
  const PageHeader(this.title, this.subtitle, {super.key});
  final String title, subtitle;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(bottom: 20),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: Theme.of(context).textTheme.headlineMedium),
        const SizedBox(height: 6),
        Text(
          subtitle,
          style: TextStyle(
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
      ],
    ),
  );
}

class SectionTitle extends StatelessWidget {
  const SectionTitle(this.title, this.icon, {super.key});
  final String title;
  final IconData icon;
  @override
  Widget build(BuildContext context) => Row(
    children: [
      Icon(
        icon,
        size: 18,
        color: Theme.of(context).colorScheme.onSurfaceVariant,
      ),
      const SizedBox(width: 10),
      Expanded(
        child: Text(
          title,
          style: Theme.of(context).textTheme.titleSmall
              ?.copyWith(fontWeight: FontWeight.w600),
        ),
      ),
    ],
  );
}

class DetailField extends StatelessWidget {
  const DetailField(this.label, this.value, {super.key});
  final String label, value;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(
        label,
        style: TextStyle(color: Theme.of(context).colorScheme.onSurfaceVariant),
      ),
      const SizedBox(height: 6),
      SelectableText(
        value,
        style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
      ),
    ],
  );
}

/// Wraps at narrow widths instead of shrinking labels or mobile hit targets.
class SelectionButton extends StatelessWidget {
  const SelectionButton({
    super.key,
    required this.label,
    required this.selected,
    required this.onPressed,
    this.icon,
  });
  final String label;
  final bool selected;
  final VoidCallback? onPressed;
  final IconData? icon;
  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).colorScheme;
    return Semantics(
      selected: selected,
      child: OutlinedButton(
        style: OutlinedButton.styleFrom(
          backgroundColor: selected ? colors.primary : colors.surface,
          foregroundColor: selected
              ? colors.onPrimary
              : colors.onSurfaceVariant,
          side: BorderSide.none,
        ),
        onPressed: onPressed,
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (icon != null) ...[
              Icon(icon, size: 20),
              const SizedBox(width: 8),
            ],
            Flexible(child: Text(label)),
          ],
        ),
      ),
    );
  }
}

class SectionLabel extends StatelessWidget {
  const SectionLabel(this.text, {super.key});
  final String text;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: 24, bottom: 12),
    child: Text(
      text,
      style: Theme.of(context).textTheme.titleMedium
          ?.copyWith(fontWeight: FontWeight.w600),
    ),
  );
}
