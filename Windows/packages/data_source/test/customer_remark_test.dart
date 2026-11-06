import 'package:bluegrass_data_source/bluegrass_data_source.dart';
import 'package:test/test.dart';

import 'api_v2_data_source_test.dart'
    show FakeTransport, login, node, ok, source;
import 'resource_queries_test.dart' show page, resource;
import 'session_coordinator_test.dart' show tokens;

void main() {
  for (final remark in <String?>[null, '', ' \t ', ' 我的办公节点 ']) {
    test(
      'name and remark remain independent across all resource routes: $remark',
      () async {
        const assetName = '美国-198.87.96.137';
        Map<String, Object?> fields(Map<String, Object?> input) => {
          ...input,
          'name': assetName,
          if (remark != null) 'customer_remark': remark,
        };
        final transport = FakeTransport((r) async {
          final path = r.uri.path;
          if (path.endsWith('/login')) return ok(tokens('old'));
          if (path.endsWith('/nodes/00456')) return ok(fields(node()));
          if (path.endsWith('/nodes'))
            return ok({
              'items': [fields(node())],
              'pagination': {'per_page': 50, 'has_more': false},
            });
          if (path.endsWith('/resources/subscription/11'))
            return ok(fields(resource()));
          return ok({
            ...page(),
            'items': [fields(resource())],
          });
        });
        final s = source(transport);
        await login(s);
        final nodes = [
          (await s.listNodes()).items.single,
          await s.getNode('00456'),
        ];
        final resources = [
          (await s.listResources()).items.single,
          await s.getResource('11'),
          (await s.listExpirySummaries()).items.single,
        ];
        for (final n in nodes) {
          expect(n.name, assetName);
          expect(n.customerRemark, remark?.trim() ?? '');
        }
        for (final r in resources) {
          expect(r.summary.name, assetName);
          expect(r.summary.customerRemark, remark?.trim() ?? '');
        }
      },
    );
  }

  for (final invalid in <Object?>[null, 12, true, <String>[]]) {
    test(
      'present non-string customer remark rejects corrupt payload: $invalid',
      () async {
        final t = FakeTransport(
          (r) async => ok(
            r.uri.path.endsWith('/login')
                ? tokens('old')
                : {...node(), 'customer_remark': invalid},
          ),
        );
        final s = source(t);
        await login(s);
        await expectLater(s.getNode('00456'), throwsA(isA<ApiFailure>()));
      },
    );
  }
}
