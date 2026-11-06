part of '../main.dart';

extension _LoginPage on _ShellState {
  Widget _loginPage(double width) {
    final colors = Theme.of(context).colorScheme;
    final wide = width >= 900;
    final mobile = width < 600;
    final pill = FilledButton.styleFrom(minimumSize: const Size.fromHeight(48));
    final form = AutofillGroup(
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Center(child: _Brand(size: 26)),
          const SizedBox(height: 24),
          Text(
            '欢迎回来',
            textAlign: TextAlign.center,
            style: AppTextStyle.title2.of(context, emphasized: true),
          ),
          const SizedBox(height: 6),
          Text(
            '登录以同步你的资源',
            textAlign: TextAlign.center,
            style: AppTextStyle.subheadline.of(
              context,
              color: colors.onSurfaceVariant,
            ),
          ),
          const SizedBox(height: 28),
          TextField(
            controller: username,
            enabled: !c.busy && c.canLogin,
            autofillHints: const [AutofillHints.username],
            textInputAction: TextInputAction.next,
            decoration: const InputDecoration(
              labelText: '账号或手机号',
              prefixIcon: Icon(Icons.person_outline_rounded),
            ),
          ),
          const SizedBox(height: 14),
          TextField(
            controller: password,
            enabled: !c.busy && c.canLogin,
            obscureText: obscure,
            autofillHints: const [AutofillHints.password],
            onSubmitted: (_) => _login(),
            decoration: InputDecoration(
              labelText: '密码',
              prefixIcon: const Icon(Icons.lock_outline_rounded),
              suffixIcon: IconButton(
                tooltip: obscure ? '显示密码' : '隐藏密码',
                onPressed: () => _update(() => obscure = !obscure),
                icon: Icon(
                  obscure
                      ? Icons.visibility_outlined
                      : Icons.visibility_off_outlined,
                ),
              ),
            ),
          ),
          const SizedBox(height: 20),
          if (c.message != null)
            Padding(
              padding: const EdgeInsets.only(bottom: 16),
              child: _Notice(c.message!),
            ),
          FilledButton(
            style: pill,
            onPressed:
                c.initializing || c.busy || !c.canLogin || c.logoutNeedsRetry
                ? null
                : _login,
            child: Text(
              c.initializing
                  ? '正在准备'
                  : c.busy
                  ? '正在登录'
                  : '登录',
            ),
          ),
          if (c.logoutNeedsRetry)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: TextButton(
                onPressed: c.busy ? null : c.logout,
                child: const Text('重试清除本地会话'),
              ),
            ),
          if (c.restoreNeedsRetry)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: TextButton(
                onPressed: c.initializing ? null : c.initialize,
                child: const Text('重试恢复会话'),
              ),
            ),
          if (!c.storageReady && !c.initializing)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: TextButton(
                onPressed: c.busy
                    ? null
                    : () async {
                        if (await _confirm(
                          '修复本地存储',
                          '将清除本机的登录会话、设备标识和设置，需要重新登录。此操作不会撤销服务端会话。',
                        )) {
                          await c.resetLocalStorage();
                        }
                      },
                child: const Text('修复本地存储'),
              ),
            ),
          const SizedBox(height: 8),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              TextButton(onPressed: _openWebsite, child: const Text('注册账号')),
              Text('·', style: TextStyle(color: colors.onSurfaceVariant)),
              TextButton(onPressed: _openWebsite, child: const Text('找回密码')),
            ],
          ),
          const SizedBox(height: 12),
          Divider(color: colors.hairline),
          const SizedBox(height: 12),
          OutlinedButton(
            style: OutlinedButton.styleFrom(
              minimumSize: const Size.fromHeight(48),
            ),
            onPressed: null,
            child: const Text('短信登录（预留）'),
          ),
        ],
      ),
    );
    final card = ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 400),
      child: BgCard(
        radius: AppCorner.xxl,
        shadow: true,
        padding: EdgeInsets.fromLTRB(
          mobile ? 20 : 28,
          32,
          mobile ? 20 : 28,
          24,
        ),
        child: form,
      ),
    );
    return Center(
      child: SingleChildScrollView(
        padding: EdgeInsets.all(wide ? 48 : (mobile ? 16 : 28)),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 1040),
          child: !wide
              ? Center(child: card)
              : Row(
                  crossAxisAlignment: CrossAxisAlignment.center,
                  children: [
                    Expanded(child: _loginShowcase()),
                    SizedBox(width: width < 1100 ? 40 : 72),
                    card,
                  ],
                ),
        ),
      ),
    );
  }

  /// The wide window's brand column, set straight on the page.
  Widget _loginShowcase() {
    final colors = Theme.of(context).colorScheme;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          '连接，从容有序。',
          style: AppTextStyle.title1.of(context, emphasized: true),
        ),
        const SizedBox(height: 10),
        Text(
          '从这里，连接更广阔的网络。',
          style: AppTextStyle.subheadline.of(
            context,
            color: colors.onSurfaceVariant,
          ),
        ),
        const SizedBox(height: 20),
        const ConnectionArtwork(),
        const SizedBox(height: 20),
        Row(
          spacing: 8,
          children: [
            Icon(Icons.shield_outlined, size: 16, color: colors.primary),
            Flexible(
              child: Text(
                DesktopPlatform.current.loginStorage,
                style: AppTextStyle.footnote.of(
                  context,
                  color: colors.onSurfaceVariant,
                ),
              ),
            ),
          ],
        ),
      ],
    );
  }
}
