package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/pomerium/ingress-controller/util/secretcache"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const secretWatchNamespaces = "secret-watch-namespaces"
const secretWatchNamespacesEnv = "SECRET_WATCH_NAMESPACES"

// bindSecretWatchNamespaces preserves the distinction between an omitted scope
// and an explicitly empty environment value, which Viper normally ignores.
func bindSecretWatchNamespaces(flags *pflag.FlagSet) error {
	if value, present := os.LookupEnv(secretWatchNamespacesEnv); present && !flags.Changed(secretWatchNamespaces) {
		// Replace the initial value without marking pflag's slice as changed:
		// the first CLI occurrence must replace, rather than append to, the env scope.
		namespaces := []string{}
		if value != "" {
			namespaces = strings.Split(value, ",")
		}
		return flags.Lookup(secretWatchNamespaces).Value.(pflag.SliceValue).Replace(namespaces)
	}
	return nil
}

// secretCacheOptions scopes only Secrets; other resource caches are unchanged.
// RBAC must independently enforce the same boundary.
func secretCacheOptions(namespaces []string) (cache.Options, error) {
	if namespaces == nil {
		return cache.Options{}, nil // Backward-compatible cluster-wide default.
	}
	if len(namespaces) == 0 {
		return cache.Options{}, fmt.Errorf("--%s must contain at least one namespace", secretWatchNamespaces)
	}
	scope := make(map[string]cache.Config, len(namespaces))
	for _, namespace := range namespaces {
		if namespace == "" || strings.TrimSpace(namespace) != namespace || len(validation.IsDNS1123Label(namespace)) != 0 {
			return cache.Options{}, fmt.Errorf("--%s contains an invalid namespace", secretWatchNamespaces)
		}
		scope[namespace] = cache.Config{}
	}
	return cache.Options{ByObject: map[client.Object]cache.ByObject{
		&corev1.Secret{}: {Namespaces: scope},
	}}, nil
}

const secretWatchAllowlist = "secret-watch-allowlist"
const secretWatchAllowlistEnv = "SECRET_WATCH_ALLOWLIST"

func bindSecretWatchAllowlist(flags *pflag.FlagSet) error {
	if value, present := os.LookupEnv(secretWatchAllowlistEnv); present && !flags.Changed(secretWatchAllowlist) {
		entries := []string{}
		if value != "" {
			entries = strings.Split(value, ",")
		}
		return flags.Lookup(secretWatchAllowlist).Value.(pflag.SliceValue).Replace(entries)
	}
	return nil
}
func secretCacheFactory(namespaces, allowlist []string) (cache.NewCacheFunc, error) {
	names, err := secretcache.Parse(allowlist, namespaces)
	if err != nil {
		return nil, err
	}
	if names == nil {
		return nil, nil
	}
	return secretcache.Factory(names), nil
}
