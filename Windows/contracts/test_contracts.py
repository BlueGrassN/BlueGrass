"""Validate the deliberately partial K-02 schemas and shared JSON vectors.

Install requirements-test.txt into .schema-tools, then run this from any cwd.
Validation reports case IDs and field paths, never raw profile payloads.
"""
import copy
import ipaddress
import json
from pathlib import Path
import re
import sys
import unittest

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT / '.schema-tools'))
from jsonschema import Draft202012Validator, FormatChecker
from referencing import Registry, Resource


def read(relative):
    return json.loads((ROOT / relative).read_text(encoding='utf-8'))


CHECKER = FormatChecker()


@CHECKER.checks('bluegrass-public-ipv4')
def observed_public_ipv4(value):
    if value is None:
        return True
    if not isinstance(value, str):
        return False
    try:
        a, b, c, d = ipaddress.IPv4Address(value).packed
    except ipaddress.AddressValueError:
        return False
    # Match domain.isPublicIpv4/native PublicIPv4, not Python's evolving is_global.
    if a in (0, 10, 127) or a >= 224:
        return False
    if a == 100 and 64 <= b <= 127:
        return False
    if (a == 169 and b == 254) or (a == 172 and 16 <= b <= 31):
        return False
    if a == 192 and (b == 168 or b == 0 and c == 2 or b == 88 and c == 99):
        return False
    if a == 192 and b == c == 0 and d not in (9, 10):
        return False
    if a == 198 and (b in (18, 19) or b == 51 and c == 100):
        return False
    return not (a == 203 and b == 0 and c == 113)


@CHECKER.checks('bluegrass-int')
def native_int(value):
    # JSON Schema's integer includes 1.0; native encoding/json's int does not.
    return type(value) is int


def secret(value, limit):
    # json.loads joins valid surrogate pairs but preserves lone surrogate escapes.
    # Go replaces each lone surrogate with U+FFFD: both occupy three UTF-8 bytes.
    return (isinstance(value, str) and 0 < len(value.encode('utf-8', 'surrogatepass')) <= limit
            and not any(c in value for c in '\x00\r\n'))


@CHECKER.checks('bluegrass-secret-255')
def socks_secret(value):
    return secret(value, 255)


@CHECKER.checks('bluegrass-secret-1024')
def protocol_secret(value):
    return secret(value, 1024)


@CHECKER.checks('bluegrass-server-name')
def server_name(value):
    if not isinstance(value, str) or not 1 <= len(value) <= 253:
        return False
    if any(not 1 <= len(label) <= 63 or
           re.fullmatch(r'[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?', label) is None
           for label in value.split('.')):
        return False
    try:
        addr = ipaddress.IPv4Address(value)
    except ipaddress.AddressValueError:
        return True  # Like netip.ParseAddr failure followed by DNS-label checks.
    return (not addr.is_loopback and not addr.is_link_local and
            not addr.is_multicast and not addr.is_unspecified and
            value != '255.255.255.255')


@CHECKER.checks('bluegrass-protocol-binding')
def protocol_binding(value):
    if not isinstance(value, dict):
        return False
    auth = value.get('auth', value)
    if not isinstance(auth, dict):
        return False
    user, password = auth.get('username', ''), auth.get('password', '')
    if not isinstance(user, str) or not isinstance(password, str):
        return False
    config = value.get('protocolConfig')
    if 'protocolConfig' not in value:
        return value.get('protocol') == 'socks5' and (
            user == password == '' or secret(user, 255) and secret(password, 255))
    if not isinstance(config, dict) or config.get('type') != value.get('protocol'):
        return False
    if user == password == '':
        return True
    options = config.get('socks5')
    return (config.get('type') == 'socks5' and isinstance(options, dict) and
            options.get('username', '') == user and options.get('password', '') == password)


def protocol_json(text):
    """Retain raw-wire checks that a parsed JSON Schema instance cannot express."""
    def unique(pairs):
        result = {}
        for key, value in pairs:
            if key in result:
                raise ValueError('INVALID_JSON')
            result[key] = value
        return result
    try:
        if len(text.encode('utf-8')) > 16 * 1024:
            raise ValueError('INVALID_JSON')
        return json.loads(text, object_pairs_hook=unique,
                          parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
    except (ValueError, UnicodeError):
        raise ValueError('INVALID_JSON') from None


SCHEMAS = [read('domain/resource.schema.json'), read('ipc/messages.schema.json'),
           read('domain/protocol.schema.json')]
REGISTRY = Registry().with_resources((s['$id'], Resource.from_contents(s)) for s in SCHEMAS)


def validator(index, definition):
    schema = {**SCHEMAS[index], '$ref': f'#/$defs/{definition}'}
    return Draft202012Validator(schema, registry=REGISTRY, format_checker=CHECKER)


class ContractTests(unittest.TestCase):
    def assertValid(self, check, value, case_id):
        paths = ['/'.join(str(p) for p in e.absolute_path) for e in check.iter_errors(value)]
        self.assertEqual(paths, [], case_id)

    def test_schemas_are_valid(self):
        for schema in SCHEMAS:
            Draft202012Validator.check_schema(schema)

    def test_shared_session_requests(self):
        f = read('fixtures/connection/control-requests.json')
        for value in f['requests']:
            self.assertValid(validator(1, 'ControlRequest'), value, value['requestId'])
        self.assertEqual([r['command'] for r in f['requests']], f['expectedCommands'])

    def test_negative_vectors_reject_fields_versions_types_and_enums(self):
        for c in read('fixtures/connection/invalid-messages.json')['cases']:
            with self.subTest(caseId=c['caseId']):
                self.assertEqual(validator(1, c['definition']).is_valid(c['input']), c['expectedValid'])

    def test_profile_expected_exit_is_optional_ignored_metadata(self):
        profile = copy.deepcopy(read('fixtures/connection/control-requests.json')['requests'][0]['params']['profile'])
        check = validator(0, 'ConnectionProfile')
        profile.pop('expectedExitIpv4', None)
        self.assertValid(check, profile, 'missing-expected-exit')
        for metadata in ['8.8.8.8', None, '', 'not-an-ip']:
            self.assertValid(check, dict(profile, expectedExitIpv4=metadata), 'ignored-expected-metadata')
        self.assertFalse(check.is_valid(dict(profile, expectedExitIpv4=123)))
        for field in ['key', 'protocol', 'entry', 'auth', 'validUntilUtc',
                      'configVersion', 'configFingerprint', 'mode']:
            with self.subTest(required=field):
                self.assertFalse(check.is_valid({k: v for k, v in profile.items() if k != field}))

    def test_connected_confirmation_observation_is_optional_but_valid_when_present(self):
        f = read('fixtures/connection/generations.json')
        value = dict(context=f['contextA'], eventSequence=1, phase='connected',
                     protection='forwarding', effectiveNowUtc=f['nowUtc'])
        check = validator(1, 'NativeConfirmation')
        self.assertValid(check, value, 'missing-observation')
        for observed in [None, '8.8.8.8', '1.1.1.1']:
            self.assertValid(check, dict(value, verifiedExitIpv4=observed), 'optional-observation')
        for observed in read('fixtures/eligibility/cases.json')['invalidObservedIps']:
            self.assertFalse(check.is_valid(dict(value, verifiedExitIpv4=observed)))

    def test_node_preserves_null_and_missing_exit(self):
        key = read('fixtures/eligibility/cases.json')['key']
        value = dict(key=key, name='Synthetic', protocol='socks5', host='entry.example.invalid', port=1080, expiresAtUtc=None)
        self.assertValid(validator(0, 'NodeRecord'), value, 'missing-exit-is-valid-node')
        self.assertFalse(validator(0, 'ConnectionProfile').is_valid(value))

    def test_explicit_protocols_bind_to_node_and_profile(self):
        base = read('fixtures/connection/control-requests.json')['requests'][0]['params']['profile']
        for case in read('fixtures/protocol/config-v1.json')['valid']:
            config = json.loads(case['json'])
            with self.subTest(caseId=case['id']):
                profile = copy.deepcopy(base)
                profile.update(protocol=config['type'], protocolConfig=config,
                               auth={'username': '', 'password': ''})
                self.assertValid(validator(0, 'ConnectionProfile'), profile, case['id'])
                node = dict(key=base['key'], name='Fixture', protocol=config['type'],
                            protocolConfig=config, host='entry.example', port=443)
                self.assertValid(validator(0, 'NodeRecord'), node, case['id'])
                if config['type'] != 'socks5':
                    profile.pop('protocolConfig')
                    node.pop('protocolConfig')
                    self.assertFalse(validator(0, 'ConnectionProfile').is_valid(profile))
                    self.assertFalse(validator(0, 'NodeRecord').is_valid(node))

    def test_shared_protocol_raw_vectors(self):
        vectors = read('fixtures/protocol/config-v1.json')
        check = validator(2, 'ProtocolConfig')
        for group, expected in [('valid', True), ('invalid', False)]:
            for case in vectors[group]:
                with self.subTest(caseId=case['id']):
                    try:
                        accepted = check.is_valid(protocol_json(case['json']))
                    except ValueError:
                        accepted = False
                    self.assertEqual(accepted, expected, case['id'])

    def test_protocol_matching_conflicts_null_and_legacy_auth(self):
        base = read('fixtures/connection/control-requests.json')['requests'][0]['params']['profile']
        check = validator(0, 'ConnectionProfile')
        config = dict(version=1, type='http', http={})
        for patch in [dict(protocol='future'), dict(protocolConfig=None),
                      dict(protocolConfig=config),
                      dict(protocol='http', protocolConfig=config),
                      dict(auth={'username': 'one-sided', 'password': ''})]:
            self.assertFalse(check.is_valid(dict(base, **patch)))
        config = dict(version=1, type='socks5', socks5=base['auth'])
        self.assertValid(check, dict(base, protocolConfig=config), 'matching-legacy-auth')
        config = dict(config, socks5=dict(username='different', password='fixture'))
        self.assertFalse(check.is_valid(dict(base, protocolConfig=config)))

    def test_protocol_utf8_limits_and_tls_identity_are_not_exit_policy(self):
        check = validator(2, 'ProtocolConfig')
        def socks(user):
            return dict(version=1, type='socks5', socks5=dict(username=user, password='fixture'))
        self.assertValid(check, socks('é' * 127), 'utf8-254')
        self.assertFalse(check.is_valid(socks('é' * 128)))
        for name in ['entry.example', '10.0.0.1', '240.0.0.1']:
            self.assertValid(check, dict(version=1, type='https', http={}, tls=dict(serverName=name)), 'tls-name')
        for name in ['127.0.0.1', '169.254.1.2', '224.0.0.1', '0.0.0.0', '*.example', 'entry.example.']:
            self.assertFalse(check.is_valid(dict(version=1, type='https', http={}, tls=dict(serverName=name))))

    def test_resource_key_fields_cannot_alias_or_be_numbers(self):
        key = read('fixtures/eligibility/cases.json')['key']
        check = validator(0, 'ResourceKey')
        self.assertValid(check, key, 'namespaced-key')
        for field in key:
            self.assertFalse(check.is_valid({k:v for k,v in key.items() if k != field}))
        self.assertFalse(check.is_valid(dict(key, sourceId=42)))
        self.assertFalse(check.is_valid(dict(key, unknown=True)))

    def test_traffic_vectors_preserve_exact_decimal_strings(self):
        f = read('fixtures/traffic/counters.json')
        check = validator(1, 'TrafficSnapshot')
        for c in f['cases']:
            value = dict(f['base'], **c['patch'], context=f['context'])
            self.assertValid(check, value, c['caseId'])
            for field in ['uploadBytes', 'downloadBytes']:
                self.assertIsInstance(value[field], str)
                self.assertLessEqual(int(value[field]), 2**64-1)
        value = dict(f['base'], context=f['context'])
        value['uploadBytes'] = 9007199254740993
        self.assertFalse(check.is_valid(value))

    def test_generation_vectors_have_complete_contexts_and_known_events(self):
        f = read('fixtures/connection/generations.json')
        check = validator(1, 'ControlContext')
        for name in ['contextA', 'contextB']:
            self.assertValid(check, f[name], name)
        for c in f['cases']:
            self.assertTrue(c['adr'])
            for event in c['events']:
                self.assertIn(event['type'], ['connect','disconnect','confirmed'])
                if 'context' in event:
                    self.assertIn('context'+event['context'], f)


if __name__ == '__main__':
    unittest.main(verbosity=2)
