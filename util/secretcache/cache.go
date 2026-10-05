// Package secretcache composes exact-name Secret watches without broad Secret LISTs.
package secretcache

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Parse validates and deduplicates explicit namespace/name entries.
func Parse(entries, namespaces []string) ([]types.NamespacedName, error) {
	if entries == nil {
		return nil, nil
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("Secret allowlist must not be empty")
	}
	permitted := map[string]bool{}
	for _, ns := range namespaces {
		permitted[ns] = true
	}
	seen := map[types.NamespacedName]bool{}
	for _, entry := range entries {
		parts := strings.Split(entry, "/")
		if len(parts) != 2 || len(validation.IsDNS1123Label(parts[0])) != 0 || len(validation.IsDNS1123Subdomain(parts[1])) != 0 {
			return nil, fmt.Errorf("invalid Secret allowlist entry")
		}
		if namespaces != nil && !permitted[parts[0]] {
			return nil, fmt.Errorf("Secret allowlist entry outside namespace scope")
		}
		seen[types.NamespacedName{Namespace: parts[0], Name: parts[1]}] = true
	}
	result := make([]types.NamespacedName, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result, nil
}

// Factory creates the ordinary cache plus independently name-filtered Secret shards.
func Factory(names []types.NamespacedName) cache.NewCacheFunc {
	names = append([]types.NamespacedName(nil), names...)
	return func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
		if len(names) == 0 {
			return nil, fmt.Errorf("Secret allowlist must not be empty")
		}
		ordinary, err := cache.New(config, opts)
		if err != nil {
			return nil, err
		}
		result := &namedCache{Cache: ordinary, shards: map[types.NamespacedName]cache.Cache{}}
		for _, key := range names {
			if _, exists := result.shards[key]; exists {
				continue
			}
			shardOpts := opts
			shardOpts.ByObject = nil
			shardOpts.DefaultNamespaces = map[string]cache.Config{key.Namespace: {FieldSelector: fields.OneTermEqualSelector("metadata.name", key.Name)}}
			shardOpts.DefaultFieldSelector = fields.OneTermEqualSelector("metadata.name", key.Name)
			shard, err := cache.New(config, shardOpts)
			if err != nil {
				return nil, err
			}
			// Register before Start/WaitForCacheSync; never construct an unfiltered Secret informer.
			if _, err = shard.GetInformer(context.Background(), &corev1.Secret{}, cache.BlockUntilSynced(false)); err != nil {
				return nil, err
			}
			result.shards[key] = shard
			result.order = append(result.order, key)
		}
		return result, nil
	}
}

type namedCache struct {
	cache.Cache
	shards map[types.NamespacedName]cache.Cache
	order  []types.NamespacedName
}

func isSecret(obj client.Object) bool {
	if _, ok := obj.(*corev1.Secret); ok {
		return true
	}
	g := obj.GetObjectKind().GroupVersionKind()
	return g.Group == "" && g.Kind == "Secret"
}
func (c *namedCache) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if !isSecret(obj) {
		return c.Cache.Get(ctx, key, obj, opts...)
	}
	shard, ok := c.shards[key]
	if !ok {
		return apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, key.Name, fmt.Errorf("outside explicit allowlist"))
	}
	return shard.Get(ctx, key, obj, opts...)
}
func (c *namedCache) List(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) error {
	list, ok := obj.(*corev1.SecretList)
	if !ok {
		if g := obj.GetObjectKind().GroupVersionKind(); g.Group == "" && g.Kind == "SecretList" {
			return fmt.Errorf("unsupported Secret list representation")
		}
		return c.Cache.List(ctx, obj, opts...)
	}
	options := (&client.ListOptions{}).ApplyOptions(opts)
	if options.Continue != "" || options.Limit != 0 {
		return fmt.Errorf("Secret cache pagination is unsupported")
	}
	list.Items = nil
	list.ResourceVersion = ""
	list.Continue = ""
	list.RemainingItemCount = nil
	for _, key := range c.order {
		if options.Namespace != "" && options.Namespace != key.Namespace {
			continue
		}
		part := &corev1.SecretList{}
		if err := c.shards[key].List(ctx, part, opts...); err != nil {
			return err
		}
		list.Items = append(list.Items, part.Items...)
	}
	return nil
}
func (c *namedCache) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	if !isSecret(obj) {
		return c.Cache.GetInformer(ctx, obj, opts...)
	}
	shards := map[string]cache.Informer{}
	for _, key := range c.order {
		i, err := c.shards[key].GetInformer(ctx, obj, opts...)
		if err != nil {
			return nil, err
		}
		shards[key.String()] = i
	}
	return newMultiNamespaceInformer(shards), nil
}
func (c *namedCache) GetInformerForKind(ctx context.Context, g schema.GroupVersionKind, opts ...cache.InformerGetOption) (cache.Informer, error) {
	if g.Group == "" && g.Kind == "Secret" {
		if g.Version != corev1.SchemeGroupVersion.Version {
			return nil, fmt.Errorf("unsupported Secret version")
		}
		return c.GetInformer(ctx, &corev1.Secret{}, opts...)
	}
	return c.Cache.GetInformerForKind(ctx, g, opts...)
}
func (c *namedCache) RemoveInformer(ctx context.Context, obj client.Object) error {
	if !isSecret(obj) {
		return c.Cache.RemoveInformer(ctx, obj)
	}
	for _, key := range c.order {
		if err := c.shards[key].RemoveInformer(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}
func (c *namedCache) IndexField(ctx context.Context, obj client.Object, field string, extract client.IndexerFunc) error {
	if !isSecret(obj) {
		return c.Cache.IndexField(ctx, obj, field, extract)
	}
	for _, key := range c.order {
		if err := c.shards[key].IndexField(ctx, obj, field, extract); err != nil {
			return err
		}
	}
	return nil
}
func (c *namedCache) Start(ctx context.Context) error {
	group, child := errgroup.WithContext(ctx)
	group.Go(func() error { return c.Cache.Start(child) })
	for _, key := range c.order {
		shard := c.shards[key]
		group.Go(func() error { return shard.Start(child) })
	}
	return group.Wait()
}
func (c *namedCache) WaitForCacheSync(ctx context.Context) bool {
	if !c.Cache.WaitForCacheSync(ctx) {
		return false
	}
	for _, key := range c.order {
		if !c.shards[key].WaitForCacheSync(ctx) {
			return false
		}
	}
	return true
}

var _ cache.Cache = (*namedCache)(nil)
