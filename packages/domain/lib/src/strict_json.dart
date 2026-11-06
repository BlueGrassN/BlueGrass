import 'dart:convert';

/// JSON at an untrusted boundary must not silently discard duplicate keys.
/// Errors deliberately omit both the raw input and parser diagnostic excerpts.
Object? decodeStrictJson(String text) {
  try {
    final containers = <Set<String>?>[];
    final tokens = RegExp(r'"(?:[^"\\]|\\[\s\S])*"|[{}\[\]]');
    for (final token in tokens.allMatches(text)) {
      final value = token.group(0)!;
      if (value == '{') {
        containers.add(<String>{});
      } else if (value == '[') {
        containers.add(null);
      } else if (value == '}' || value == ']') {
        if (containers.isEmpty) throw const FormatException();
        containers.removeLast();
      } else {
        var next = token.end;
        while (next < text.length &&
            const [32, 9, 10, 13].contains(text.codeUnitAt(next))) {
          next++;
        }
        if (next < text.length && text[next] == ':') {
          final key = jsonDecode(value) as String;
          if (containers.isEmpty ||
              containers.last == null ||
              !containers.last!.add(key)) {
            throw const FormatException();
          }
        }
      }
      if (containers.length > 64) throw const FormatException();
    }
    return jsonDecode(text);
  } catch (_) {
    throw const FormatException('INVALID_JSON');
  }
}
