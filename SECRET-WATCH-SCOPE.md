# Optional Secret namespace scope

This branch is based on upstream v0.33.4. It adds:

```
--secret-watch-namespaces=default,keycloak,pomerium
```

The corresponding environment variable is `SECRET_WATCH_NAMESPACES`. CLI input
overrides environment input. Omission preserves the existing cluster-wide default;
explicit empty values and invalid namespace names fail validation. This scope is
independent of `--namespaces`, which continues to filter ingress processing.

The new option changes only Secret caches in the bootstrap manager, configuration
manager and standalone controller. Every Secret in an allowed namespace can still
be cached. It does not provide named-Secret isolation. Bootstrap, OIDC, route and
certificate Secret namespaces must all be explicitly included.

RBAC must separately enforce the same scope through namespace RoleBindings.
Existing upstream deployment manifests retain their broad default RBAC. Restricting
the cache alone does not prevent a compromised process from using broader API
permissions. Never silently widen scope to satisfy a configuration reference.

Build an ARM64 candidate locally from this checkout:

```
docker buildx build --platform linux/arm64 --load -f Dockerfile.secret-scope -t iad.ocir.io/idktjivvhhey/pomerium-ingress-oci:v0.33.4-secret-scope.1 .
```

The build runs targeted tests before compiling. Image publication and deployment
are separate steps; do not deploy until restricted-identity integration tests and
a fresh-controller rehearsal pass. No API keys or registry passwords belong in
the build context. The upstream `.dockerignore` excludes `.git` and other local
files; use only this clean source checkout as context.

## Maintenance

Keep this patch on its own branch and rebase onto reviewed upstream releases.
Submit upstream for consideration under issue #521; acceptance is not guaranteed.
Return to official images only after a released implementation passes the same
authorization tests. Record the published image digest and deploy by digest.
