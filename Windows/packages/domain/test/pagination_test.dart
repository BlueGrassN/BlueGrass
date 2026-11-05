import 'package:bluegrass_domain/bluegrass_domain.dart';
import 'package:test/test.dart';
import 'fixtures.dart';

void main() {
  test('recent_order_preserves_cursor', () {
    final f = fixture('connection/generations.json');
    final context =
        ControlContext.fromJson(f['contextB'] as Map<String, dynamic>);
    final items = ['A', 'B', 'C']
        .map((id) => ResourceBrief(key: key(id), name: id))
        .toList();
    final page = Page(items: items, hasMore: true, nextCursor: 'opaque:B/==');
    var history = RecentSuccessIndex();
    final pending =
        ConnectionState(context: context, phase: ConnectionPhase.connecting);
    expect(history.record(pending).sequences, isEmpty);
    final success = ConnectionState(
        context: context,
        phase: ConnectionPhase.connected,
        effects: [ConnectionEffect.recordRecentSuccess]);
    history = history.record(success);
    expect(history.record(success).sequences, history.sequences);
    expect(
        orderRecent(page.items, history).map((e) => e.name), ['B', 'A', 'C']);
    expect(page.items.map((e) => e.name), ['A', 'B', 'C']);
    expect(page.nextCursor, 'opaque:B/==');
    expect(page.hasMore, isTrue);
    final empty = Page<ResourceBrief>(
        items: [], hasMore: true, nextCursor: page.nextCursor);
    expect(orderRecent(empty.items, history), isEmpty);
    expect(empty.nextCursor, page.nextCursor);
  });
}
