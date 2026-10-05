package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecretWatchCommands(t *testing.T) {
	t.Setenv(secretWatchNamespacesEnv, "keycloak,pomerium")
	t.Setenv(secretWatchAllowlistEnv, "pomerium/bootstrap")
	standalone := &controllerCmd{}
	require.NoError(t, standalone.setupFlags())
	require.Equal(t, []string{"keycloak", "pomerium"}, standalone.SecretWatchNamespaces)
	require.Equal(t, []string{"pomerium/bootstrap"}, standalone.SecretWatchAllowlist)
	require.NoError(t, standalone.ParseFlags([]string{"--secret-watch-namespaces=default", "--namespaces=pomerium"}))
	require.Equal(t, []string{"default"}, standalone.SecretWatchNamespaces)
	require.Equal(t, []string{"pomerium"}, standalone.Namespaces)
	all := &allCmd{}
	require.NoError(t, all.setupFlags())
	require.Equal(t, []string{"keycloak", "pomerium"}, all.SecretWatchNamespaces)
	require.Equal(t, []string{"pomerium/bootstrap"}, all.SecretWatchAllowlist)
	require.NoError(t, all.ParseFlags([]string{"--secret-watch-namespaces=", "--namespaces=pomerium"}))
	_, err := all.getIngressControllerOptions()
	require.Error(t, err)
}
