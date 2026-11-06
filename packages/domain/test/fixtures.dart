import 'dart:convert';
import 'dart:io';
import 'package:bluegrass_domain/bluegrass_domain.dart';

Map<String, dynamic> fixture(String path) =>
    jsonDecode(File('../../contracts/fixtures/$path').readAsStringSync())
        as Map<String, dynamic>;
ResourceKey key([String id = '00042']) => ResourceKey(
    environment: 'fixture',
    dataSource: 'api-v1',
    accountKey: 'fake-account',
    sourceId: id);
NodeRecord node(
        {String? expected = '8.8.8.8',
        DateTime? expiry,
        ResourceKey? resourceKey}) =>
    NodeRecord(
        key: resourceKey ?? key(),
        name: 'Synthetic node',
        protocol: 'socks5',
        host: 'entry.example.invalid',
        port: 1080,
        expectedExitIpv4: expected,
        expiresAtUtc: expiry);
