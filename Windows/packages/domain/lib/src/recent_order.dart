import 'connection.dart';
import 'resources.dart';

final class RecentSuccessIndex {
  RecentSuccessIndex(
      {Map<ResourceKey, int> sequences = const {},
      Set<ControlContext> acknowledged = const {}})
      : sequences = Map.unmodifiable(sequences),
        acknowledged = Set.unmodifiable(acknowledged);
  final Map<ResourceKey, int> sequences;
  final Set<ControlContext> acknowledged;
  RecentSuccessIndex record(ConnectionState state) {
    final context = state.context;
    if (context == null ||
        state.phase != ConnectionPhase.connected ||
        !state.effects.contains(ConnectionEffect.recordRecentSuccess) ||
        acknowledged.contains(context)) return this;
    final sequence = sequences.values.fold<int>(0, (a, b) => a > b ? a : b) + 1;
    return RecentSuccessIndex(
        sequences: {...sequences, context.resourceKey: sequence},
        acknowledged: {...acknowledged, context});
  }
}

List<ResourceBrief> orderRecent(
    List<ResourceBrief> items, RecentSuccessIndex history) {
  // Original index explicitly resolves ties: List.sort is not guaranteed stable.
  final indexed = items.indexed.toList();
  indexed.sort((a, b) {
    final bySuccess = (history.sequences[b.$2.key] ?? 0)
        .compareTo(history.sequences[a.$2.key] ?? 0);
    return bySuccess != 0 ? bySuccess : a.$1.compareTo(b.$1);
  });
  return indexed.map((e) => e.$2).toList(growable: false);
}
