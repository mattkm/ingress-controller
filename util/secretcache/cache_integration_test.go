package secretcache

import (
	"context"
	"k8s.io/client-go/rest"
	toolscache "k8s.io/client-go/tools/cache"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Uses only synthetic credentials and objects in a disposable local API server.
func TestSecretAllowlistRestrictedIdentity(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("requires local envtest API-server assets")
	}
	environment := &envtest.Environment{}
	adminConfig, err := environment.Start()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, environment.Stop()) })
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, rbacv1.AddToScheme(scheme))
	admin, err := client.New(adminConfig, client.Options{Scheme: scheme})
	require.NoError(t, err)
	ctx := context.Background()
	for _, ns := range []string{"pomerium", "excluded"} {
		require.NoError(t, admin.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))
		require.NoError(t, admin.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "synthetic", Namespace: ns}, Data: map[string][]byte{"test": []byte("synthetic-v1")}}))
	}
	require.NoError(t, admin.Create(ctx, &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "scoped-reader", Namespace: "pomerium"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list", "watch"}, ResourceNames: []string{"synthetic", "new-synthetic"}}}}))
	require.NoError(t, admin.Create(ctx, &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "scoped-reader", Namespace: "pomerium"}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: "scoped-reader"}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "scoped-reader"}}}))
	user, err := environment.AddUser(envtest.User{Name: "scoped-reader"}, adminConfig)
	require.NoError(t, err)
	direct, err := client.New(user.Config(), client.Options{Scheme: scheme})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return direct.Get(ctx, client.ObjectKey{Namespace: "pomerium", Name: "synthetic"}, &corev1.Secret{}) == nil
	}, 15*time.Second, 100*time.Millisecond)
	require.True(t, apierrors.IsForbidden(direct.List(ctx, &corev1.SecretList{})))
	require.True(t, apierrors.IsForbidden(direct.Get(ctx, client.ObjectKey{Namespace: "excluded", Name: "synthetic"}, &corev1.Secret{})))
	require.NoError(t, admin.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "oci-eso-api-credentials", Namespace: "pomerium"}, Data: map[string][]byte{"privateKey": []byte("synthetic-excluded")}}))
	require.True(t, apierrors.IsForbidden(direct.Get(ctx, client.ObjectKey{Namespace: "pomerium", Name: "oci-eso-api-credentials"}, &corev1.Secret{})))
	require.True(t, apierrors.IsForbidden(direct.List(ctx, &corev1.SecretList{}, client.InNamespace("pomerium"))))
	var mu sync.Mutex
	var queries []string
	var watches int
	var reconnect sync.Once
	wrapped := rest.CopyConfig(user.Config())
	wrapped.WrapTransport = func(next http.RoundTripper) http.RoundTripper {
		return requestRecorder{next: next, reconnect: &reconnect, record: func(req *http.Request) {
			if strings.HasSuffix(req.URL.Path, "/secrets") {
				mu.Lock()
				queries = append(queries, req.URL.Query().Get("fieldSelector"))
				if req.URL.Query().Get("watch") == "true" {
					watches++
				}
				mu.Unlock()
			}
		}}
	}
	// Fresh caches model bootstrap/configuration creation and later recreation.
	for _, label := range []string{"bootstrap", "configuration", "recreated"} {
		t.Run(label, func(t *testing.T) {
			names, err := Parse([]string{"pomerium/synthetic", "pomerium/new-synthetic"}, []string{"pomerium"})
			require.NoError(t, err)
			c, err := Factory(names)(wrapped, cache.Options{Scheme: scheme})
			require.NoError(t, err)
			cacheCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			done := make(chan error, 1)
			go func() { done <- c.Start(cacheCtx) }()
			defer func() { cancel(); require.NoError(t, <-done) }()
			informer, err := c.GetInformer(cacheCtx, &corev1.Secret{})
			require.NoError(t, err)
			var eventMu sync.Mutex
			events := map[string]int{}
			record := func(obj interface{}) {
				secret, ok := obj.(*corev1.Secret)
				if !ok {
					return
				}
				eventMu.Lock()
				events[secret.Name]++
				eventMu.Unlock()
			}
			registration, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{AddFunc: record, UpdateFunc: func(_, obj interface{}) { record(obj) }, DeleteFunc: record})
			require.NoError(t, err)
			require.Eventually(t, registration.HasSynced, 10*time.Second, 100*time.Millisecond)
			require.True(t, c.WaitForCacheSync(cacheCtx), "restricted Secret cache must start and synchronize")
			if label == "bootstrap" {
				require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return watches >= 3 }, 10*time.Second, 100*time.Millisecond)
			}
			list := &corev1.SecretList{}
			require.NoError(t, c.List(cacheCtx, list))
			require.Len(t, list.Items, 1)
			require.Error(t, c.Get(cacheCtx, client.ObjectKey{Namespace: "pomerium", Name: "oci-eso-api-credentials"}, &corev1.Secret{}))
			require.Equal(t, "pomerium", list.Items[0].Namespace)
			require.Error(t, c.Get(cacheCtx, client.ObjectKey{Namespace: "excluded", Name: "synthetic"}, &corev1.Secret{}))
			secret := &corev1.Secret{}
			require.NoError(t, admin.Get(ctx, client.ObjectKey{Namespace: "pomerium", Name: "synthetic"}, secret))
			secret.Data["test"] = []byte(label)
			require.NoError(t, admin.Update(ctx, secret))
			require.Eventually(t, func() bool {
				fetched := &corev1.Secret{}
				return c.Get(cacheCtx, client.ObjectKeyFromObject(secret), fetched) == nil && string(fetched.Data["test"]) == label
			}, 10*time.Second, 100*time.Millisecond)
			extra := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "new-synthetic", Namespace: "pomerium"}, Data: map[string][]byte{"test": []byte("synthetic")}}
			require.NoError(t, admin.Create(ctx, extra))
			require.Eventually(t, func() bool {
				return c.Get(cacheCtx, client.ObjectKeyFromObject(extra), &corev1.Secret{}) == nil
			}, 10*time.Second, 100*time.Millisecond)
			require.NoError(t, admin.Delete(ctx, extra))
			require.Eventually(t, func() bool {
				return apierrors.IsNotFound(c.Get(cacheCtx, client.ObjectKeyFromObject(extra), &corev1.Secret{}))
			}, 10*time.Second, 100*time.Millisecond)
			require.Eventually(t, func() bool { eventMu.Lock(); defer eventMu.Unlock(); return events["new-synthetic"] >= 2 }, 10*time.Second, 100*time.Millisecond)
			require.NoError(t, informer.RemoveEventHandler(registration))
			eventMu.Lock()
			require.Zero(t, events["oci-eso-api-credentials"])
			eventMu.Unlock()
		})
	}
	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, queries)
	require.GreaterOrEqual(t, watches, 7, "closed watch must reconnect with the same exact-name selector")
	for _, query := range queries {
		require.Contains(t, []string{"metadata.name=synthetic", "metadata.name=new-synthetic"}, query)
	}

}

type requestRecorder struct {
	next      http.RoundTripper
	record    func(*http.Request)
	reconnect *sync.Once
}

func (r requestRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.record(req)
	response, err := r.next.RoundTrip(req)
	if err == nil && req.URL.Query().Get("watch") == "true" {
		r.reconnect.Do(func() { time.AfterFunc(200*time.Millisecond, func() { _ = response.Body.Close() }) })
	}
	return response, err
}
func TestSecretAllowlistValidation(t *testing.T) {
	for _, entries := range [][]string{{}, {"pomerium/*"}, {"/name"}, {"pomerium/"}, {"other/name"}, {"pomerium/name "}} {
		_, err := Parse(entries, []string{"pomerium"})
		require.Error(t, err)
	}
	entries, err := Parse([]string{"pomerium/a", "pomerium/a", "pomerium/b"}, []string{"pomerium"})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	entries, err = Parse(nil, nil)
	require.NoError(t, err)
	require.Nil(t, entries)
}
