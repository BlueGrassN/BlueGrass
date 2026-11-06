# Local TLS test fixture

These certificates and the server key are disposable test material generated for
loopback-only tests. The private CA key was not saved. The server certificate is
for `api.example.com`, and only an explicitly constructed test SecurityContext
trusts its CA. Production uses the platform trust store. Never add this CA to an
OS trust store or package these files as application assets.

The leaf has a one-year validity period, following Apple's TLS certificate
[lifetime requirements](https://support.apple.com/en-us/103769). CI regenerates
these disposable files before testing. To refresh the local fixtures, run from
the repository root:

```text
go run Windows/app/test/fixtures/control-tls/generate.go -out Windows/app/test/fixtures/control-tls
```
