# Local TLS test fixture

These certificates and the server key are disposable test material generated for
loopback-only tests. The private CA key was not saved. The server certificate is
for `api.example.com`, and only an explicitly constructed test SecurityContext
trusts its CA. Production uses the platform trust store. Never add this CA to an
OS trust store or package these files as application assets.
