import 'package:flutter/material.dart';

/// Presentation-only binding. Session/storage gates and password lifetime are
/// owned by the host controller, never inferred by the form.
class LoginViewData {
  const LoginViewData({
    required this.fieldsEnabled,
    required this.submitLabel,
    required this.onSubmit,
    required this.onWebsite,
  });
  final bool fieldsEnabled;
  final String submitLabel;
  final VoidCallback? onSubmit;
  final VoidCallback onWebsite;
}

class LoginContent extends StatelessWidget {
  const LoginContent({
    super.key,
    required this.data,
    required this.username,
    required this.password,
    required this.obscure,
    required this.onTogglePassword,
    required this.brand,
    this.validate = false,
    this.messages = const [],
    this.recovery = const [],
    this.footer,
  });
  final LoginViewData data;
  final TextEditingController username, password;
  final bool obscure, validate;
  final VoidCallback onTogglePassword;
  final Widget brand;
  final List<Widget> messages, recovery;
  final Widget? footer;

  @override
  Widget build(BuildContext context) => AutofillGroup(
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Center(child: brand),
        const SizedBox(height: 24),
        Text(
          '欢迎回来',
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.headlineSmall,
        ),
        const SizedBox(height: 6),
        Text(
          '登录以同步你的资源',
          textAlign: TextAlign.center,
          style: TextStyle(
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
        const SizedBox(height: 28),
        TextFormField(
          key: const Key('username'),
          controller: username,
          enabled: data.fieldsEnabled,
          autocorrect: false,
          autofillHints: const [AutofillHints.username],
          textInputAction: TextInputAction.next,
          validator: !validate
              ? null
              : (v) => v == null || v.trim().isEmpty ? '请输入账号' : null,
          decoration: const InputDecoration(
            labelText: '账号或手机号',
            prefixIcon: Icon(Icons.person_outline_rounded),
          ),
        ),
        const SizedBox(height: 14),
        TextFormField(
          key: const Key('password'),
          controller: password,
          enabled: data.fieldsEnabled,
          obscureText: obscure,
          autocorrect: false,
          enableSuggestions: false,
          autofillHints: const [AutofillHints.password],
          textInputAction: TextInputAction.done,
          validator: !validate
              ? null
              : (v) => v == null || v.isEmpty ? '请输入密码' : null,
          onFieldSubmitted: (_) => data.onSubmit?.call(),
          decoration: InputDecoration(
            labelText: '密码',
            prefixIcon: const Icon(Icons.lock_outline_rounded),
            suffixIcon: IconButton(
              tooltip: obscure ? '显示密码' : '隐藏密码',
              onPressed: onTogglePassword,
              icon: Icon(
                obscure
                    ? Icons.visibility_outlined
                    : Icons.visibility_off_outlined,
              ),
            ),
          ),
        ),
        const SizedBox(height: 20),
        for (final message in messages)
          Padding(padding: const EdgeInsets.only(bottom: 16), child: message),
        FilledButton(
          key: const Key('login'),
          style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(48)),
          onPressed: data.onSubmit,
          child: Text(data.submitLabel),
        ),
        for (final action in recovery)
          Padding(padding: const EdgeInsets.only(top: 8), child: action),
        const SizedBox(height: 8),
        Wrap(
          alignment: WrapAlignment.center,
          spacing: 12,
          children: [
            TextButton(onPressed: data.onWebsite, child: const Text('注册账号')),
            TextButton(onPressed: data.onWebsite, child: const Text('找回密码')),
          ],
        ),
        ?footer,
      ],
    ),
  );
}
