import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../presentation/login_content.dart';
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
              child: Form(
                key: _form,
                child: LoginContent(
                  data: LoginViewData(
                    fieldsEnabled:
                        !state.authBusy && !state.requiresSessionClear,
                    submitLabel: state.requiresSessionClear
                        ? '清除本机会话'
                        : state.authBusy
                        ? '正在登录'
                        : '登录',
                    onSubmit: state.authBusy
                        ? null
                        : state.requiresSessionClear
                        ? state.logout
                        : _submit,
                    onWebsite: state.openOfficialWebsite,
                  ),
                  username: _username,
                  password: _password,
                  obscure: _obscure,
                  onTogglePassword: () => setState(() => _obscure = !_obscure),
                  validate: true,
                  brand: const BlueGrassBrand(size: 34),
                  messages: [
                    if (state.authError != null)
                      NoticeCard(state.authError!, error: true),
                    if (state.settingsError != null)
                      NoticeCard(state.settingsError!, error: true),
                  ],
                  recovery: [
                    if (state.authError != null && !state.requiresSessionClear)
                      TextButton(
                        onPressed: state.authBusy ? null : state.initialize,
                        child: const Text('重试恢复登录'),
                      ),
                  ],
                  footer: state.websiteError == null
                      ? null
                      : NoticeCard(state.websiteError!, error: true),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}
