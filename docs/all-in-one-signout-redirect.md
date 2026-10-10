# All-in-one sign-out redirect

The all-in-one command accepts `--signout-redirect-url` or the equivalent
`SIGNOUT_REDIRECT_URL` environment variable:

```yaml
args:
  - all-in-one
  - --signout-redirect-url=https://site.example/
```

The command-line flag overrides the environment variable. The default is empty,
which retains Pomerium's built-in signed-out landing page. The configured URL
must be an absolute HTTP or HTTPS URL without userinfo; invalid settings fail
startup. Prefer HTTPS for public sites.

The setting is passed to Pomerium Core's `signout_redirect_url` bootstrap option
and retained through settings reconciliation. It does not change session
revocation or the identity provider's logout flow. With OIDC, register the exact
destination as a valid post-logout redirect URI on the identity-provider client.

Use a public destination. Redirecting to a protected route will start another
authentication flow immediately after logout. This option does not enable
arbitrary request-supplied sign-out redirects or add a field to the Pomerium CRD.
