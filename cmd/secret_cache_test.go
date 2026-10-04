package cmd

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestSecretCacheOptions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		namespaces []string
		invalid    bool
	}{
		{"omitted", nil, false},
		{"single", []string{"pomerium"}, false},
		{"multiple", []string{"default", "keycloak", "pomerium"}, false},
		{"duplicates", []string{"pomerium", "pomerium"}, false},
		{"empty", []string{}, true},
		{"empty element", []string{"pomerium", ""}, true},
		{"wildcard", []string{"*"}, true},
		{"whitespace", []string{" pomerium"}, true},
		{"uppercase", []string{"Pomerium"}, true},
		{"invalid name", []string{"not/a/namespace"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := secretCacheOptions(tc.namespaces)
			if tc.invalid {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Nil(t, opts.DefaultNamespaces)
			if tc.namespaces == nil {
				require.Nil(t, opts.ByObject)
				return
			}
			require.Len(t, opts.ByObject, 1)
			for object, config := range opts.ByObject {
				require.IsType(t, &corev1.Secret{}, object)
				for _, namespace := range tc.namespaces {
					require.Contains(t, config.Namespaces, namespace)
				}
				require.NotContains(t, config.Namespaces, "")
				require.NotContains(t, config.Namespaces, "opentelemetry")
			}
		})
	}
}

func TestSecretWatchNamespaceFlags(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		cli       []string
		invalid   bool
		want      []string
	}{
		{"environment", "default,pomerium", nil, false, []string{"default", "pomerium"}},
		{"CLI override", "default", []string{"--secret-watch-namespaces=pomerium"}, false, []string{"pomerium"}},
		{"empty environment", "", nil, true, nil},
		{"empty CLI", "default", []string{"--secret-watch-namespaces="}, true, nil},
		{"empty element", "default,", nil, true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(secretWatchNamespacesEnv, tc.env)
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			var scope []string
			flags.StringSliceVar(&scope, secretWatchNamespaces, nil, "Secret namespace scope")
			require.NoError(t, flags.Parse(tc.cli))
			require.NoError(t, bindSecretWatchNamespaces(flags))
			_, err := secretCacheOptions(scope)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, scope)
			}
		})
	}
}

func TestSecretCacheOptionsIndependent(t *testing.T) {
	a, err := secretCacheOptions([]string{"pomerium"})
	require.NoError(t, err)
	b, err := secretCacheOptions([]string{"pomerium"})
	require.NoError(t, err)
	for _, cfg := range a.ByObject {
		delete(cfg.Namespaces, "pomerium")
	}
	for _, cfg := range b.ByObject {
		require.Contains(t, cfg.Namespaces, "pomerium")
	}
}
