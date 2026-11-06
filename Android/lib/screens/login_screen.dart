import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../app_controller.dart';
import '../brand.dart';
import 'components.dart';

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key, required this.controller});
  final AppController controller;
  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _form = GlobalKey<FormState>();
  final _username = TextEditingController();
  final _password = TextEditingController();
  bool _obscure = true;
  @override
  void dispose() {
    _username.dispose();
    _password.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (widget.controller.authBusy ||
        widget.controller.requiresSessionClear ||
        !_form.currentState!.validate()) {
      return;
    }
    FocusScope.of(context).unfocus();
    final password = _password.text;
    _password.clear();
    TextInput.finishAutofillContext(shouldSave: false);
    await widget.controller.login(_username.text, password);
  }

  @override
  Widget build(BuildContext context) {
    final state = widget.controller;
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        actions: [
          PopupMenuButton<ThemeMode>(
            tooltip: '切换主题',
            icon: const Icon(Icons.contrast_rounded),
            onSelected: state.setTheme,
            itemBuilder: (_) => const [
              PopupMenuItem(value: ThemeMode.system, child: Text('跟随系统')),
              PopupMenuItem(value: ThemeMode.light, child: Text('浅色')),
              PopupMenuItem(value: ThemeMode.dark, child: Text('深色')),
            ],
          ),
        ],
      ),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 480),
            child: SingleChildScrollView(
              padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
              child: AutofillGroup(
                child: Form(
                  key: _form,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      const Center(child: BlueGrassBrand(size: 34)),
                      const SizedBox(height: 16),
                      Text(
                        '欢迎回来',
                        textAlign: TextAlign.center,
                        style: theme.textTheme.headlineSmall?.copyWith(
                          fontWeight: FontWeight.w700,
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        '登录以同步你的资源',
                        textAlign: TextAlign.center,
                        style: theme.textTheme.bodyMedium?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                      const SizedBox(height: 24),
                      SurfaceCard(
                        margin: EdgeInsets.zero,
                        child: Padding(
                          padding: const EdgeInsets.all(20),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            children: [
                              TextFormField(
                                key: const Key('username'),
                                controller: _username,
                                enabled:
                                    !state.authBusy &&
                                    !state.requiresSessionClear,
                                autofillHints: const [AutofillHints.username],
                                textInputAction: TextInputAction.next,
                                autocorrect: false,
                                decoration: InputDecoration(
                                  labelText: '账号或手机号',
                                  fillColor: theme.colorScheme.surfaceContainer,
                                  prefixIcon: const Icon(
                                    Icons.person_outline_rounded,
                                  ),
                                ),
                                validator: (value) =>
                                    value == null || value.trim().isEmpty
                                    ? '请输入账号'
                                    : null,
                              ),
                              const SizedBox(height: 16),
                              TextFormField(
                                key: const Key('password'),
                                controller: _password,
                                enabled:
                                    !state.authBusy &&
                                    !state.requiresSessionClear,
                                obscureText: _obscure,
                                autocorrect: false,
                                enableSuggestions: false,
                                autofillHints: const [AutofillHints.password],
                                textInputAction: TextInputAction.done,
                                onFieldSubmitted: (_) => _submit(),
                                decoration: InputDecoration(
                                  labelText: '密码',
                                  fillColor: theme.colorScheme.surfaceContainer,
                                  prefixIcon: const Icon(
                                    Icons.lock_outline_rounded,
                                  ),
                                  suffixIcon: IconButton(
                                    tooltip: _obscure ? '显示密码' : '隐藏密码',
                                    onPressed: () =>
                                        setState(() => _obscure = !_obscure),
                                    icon: Icon(
                                      _obscure
                                          ? Icons.visibility_outlined
                                          : Icons.visibility_off_outlined,
                                    ),
                                  ),
                                ),
                                validator: (value) =>
                                    value == null || value.isEmpty
                                    ? '请输入密码'
                                    : null,
                              ),
                              if (state.authError != null) ...[
                                const SizedBox(height: 16),
                                NoticeCard(state.authError!, error: true),
                              ],
                              if (state.settingsError != null) ...[
                                const SizedBox(height: 16),
                                NoticeCard(state.settingsError!, error: true),
                              ],
                              const SizedBox(height: 20),
                              if (state.requiresSessionClear)
                                FilledButton(
                                  onPressed: state.authBusy
                                      ? null
                                      : state.logout,
                                  child: const Text('清除本机会话'),
                                )
                              else
                                FilledButton(
                                  key: const Key('login'),
                                  onPressed: state.authBusy ? null : _submit,
                                  child: state.authBusy
                                      ? const SizedBox.square(
                                          dimension: 22,
                                          child: CircularProgressIndicator(
                                            strokeWidth: 2,
                                          ),
                                        )
                                      : const Text('登录'),
                                ),
                              if (state.authError != null &&
                                  !state.requiresSessionClear)
                                TextButton(
                                  onPressed: state.authBusy
                                      ? null
                                      : state.initialize,
                                  child: const Text('重试恢复登录'),
                                ),
                            ],
                          ),
                        ),
                      ),
                      const SizedBox(height: 12),
                      Wrap(
                        alignment: WrapAlignment.center,
                        spacing: 12,
                        children: [
                          TextButton(
                            onPressed: state.openOfficialWebsite,
                            child: const Text('注册账号'),
                          ),
                          TextButton(
                            onPressed: state.openOfficialWebsite,
                            child: const Text('找回密码'),
                          ),
                        ],
                      ),
                      if (state.websiteError != null) ...[
                        const SizedBox(height: 12),
                        NoticeCard(state.websiteError!, error: true),
                      ],
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
