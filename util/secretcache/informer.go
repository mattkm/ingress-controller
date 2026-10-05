/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Informer aggregation adapted from controller-runtime v0.24.1 multi_namespace_cache.go.
package secretcache

import (
	"fmt"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"strings"
	"time"
)

type Informer = cache.Informer

func newMultiNamespaceInformer(namespaceToInformer map[string]Informer) Informer {
	mni := &multiNamespaceInformer{
		synced:              make(chan struct{}),
		namespaceToInformer: namespaceToInformer,
	}
	go func() {
		for _, informer := range mni.namespaceToInformer {
			<-informer.HasSyncedChecker().Done()
		}
		close(mni.synced)
	}()
	return mni
}

// multiNamespaceInformer knows how to handle interacting with the underlying informer across multiple namespaces.
type multiNamespaceInformer struct {
	synced              chan struct{}
	namespaceToInformer map[string]Informer
}

func newMultiNamespaceInformerHandlerRegistration(handles map[string]toolscache.ResourceEventHandlerRegistration) toolscache.ResourceEventHandlerRegistration {
	hr := &multiNamespaceInformerHandlerRegistration{
		synced:  make(chan struct{}),
		handles: handles,
	}
	go func() {
		for _, handle := range hr.handles {
			<-handle.HasSyncedChecker().Done()
		}
		close(hr.synced)
	}()
	return hr
}

type multiNamespaceInformerHandlerRegistration struct {
	synced  chan struct{}
	handles map[string]toolscache.ResourceEventHandlerRegistration
}

// HasSynced asserts that the handler has been called for the full initial state of the informer.
func (h *multiNamespaceInformerHandlerRegistration) HasSynced() bool {
	for _, h := range h.handles {
		if !h.HasSynced() {
			return false
		}
	}
	return true
}

func (h *multiNamespaceInformerHandlerRegistration) HasSyncedChecker() toolscache.DoneChecker {
	return h
}

func (h *multiNamespaceInformerHandlerRegistration) Name() string {
	names := make([]string, 0, len(h.handles))
	for ns, handle := range h.handles {
		names = append(names, fmt.Sprintf("%s: %s", ns, handle.HasSyncedChecker().Name()))
	}
	return strings.Join(names, ", ")
}

func (h *multiNamespaceInformerHandlerRegistration) Done() <-chan struct{} {
	return h.synced
}

var _ Informer = &multiNamespaceInformer{}

// AddEventHandler adds the handler to each informer.
func (i *multiNamespaceInformer) AddEventHandler(handler toolscache.ResourceEventHandler) (toolscache.ResourceEventHandlerRegistration, error) {
	handles := make(map[string]toolscache.ResourceEventHandlerRegistration, len(i.namespaceToInformer))

	for ns, informer := range i.namespaceToInformer {
		registration, err := informer.AddEventHandler(handler)
		if err != nil {
			return nil, err
		}
		handles[ns] = registration
	}

	return newMultiNamespaceInformerHandlerRegistration(handles), nil
}

// AddEventHandlerWithResyncPeriod adds the handler with a resync period to each namespaced informer.
func (i *multiNamespaceInformer) AddEventHandlerWithResyncPeriod(handler toolscache.ResourceEventHandler, resyncPeriod time.Duration) (toolscache.ResourceEventHandlerRegistration, error) {
	handles := make(map[string]toolscache.ResourceEventHandlerRegistration, len(i.namespaceToInformer))

	for ns, informer := range i.namespaceToInformer {
		registration, err := informer.AddEventHandlerWithResyncPeriod(handler, resyncPeriod)
		if err != nil {
			return nil, err
		}
		handles[ns] = registration
	}

	return newMultiNamespaceInformerHandlerRegistration(handles), nil
}

// AddEventHandlerWithOptions adds the handler with options to each namespaced informer.
func (i *multiNamespaceInformer) AddEventHandlerWithOptions(handler toolscache.ResourceEventHandler, options toolscache.HandlerOptions) (toolscache.ResourceEventHandlerRegistration, error) {
	handles := make(map[string]toolscache.ResourceEventHandlerRegistration, len(i.namespaceToInformer))

	for ns, informer := range i.namespaceToInformer {
		registration, err := informer.AddEventHandlerWithOptions(handler, options)
		if err != nil {
			return nil, err
		}
		handles[ns] = registration
	}

	return newMultiNamespaceInformerHandlerRegistration(handles), nil
}

// RemoveEventHandler removes a previously added event handler given by its registration handle.
func (i *multiNamespaceInformer) RemoveEventHandler(h toolscache.ResourceEventHandlerRegistration) error {
	handles, ok := h.(*multiNamespaceInformerHandlerRegistration)
	if !ok {
		return fmt.Errorf("registration is not a registration returned by multiNamespaceInformer")
	}
	for ns, informer := range i.namespaceToInformer {
		registration, ok := handles.handles[ns]
		if !ok {
			continue
		}
		if err := informer.RemoveEventHandler(registration); err != nil {
			return err
		}
	}
	return nil
}

// AddIndexers adds the indexers to each informer.
func (i *multiNamespaceInformer) AddIndexers(indexers toolscache.Indexers) error {
	for _, informer := range i.namespaceToInformer {
		err := informer.AddIndexers(indexers)
		if err != nil {
			return err
		}
	}
	return nil
}

// HasSynced checks if each informer has synced.
func (i *multiNamespaceInformer) HasSynced() bool {
	for _, informer := range i.namespaceToInformer {
		if !informer.HasSynced() {
			return false
		}
	}
	return true
}

// HasSyncedChecker completes if each informer has synced.
func (i *multiNamespaceInformer) HasSyncedChecker() toolscache.DoneChecker {
	return i
}

func (i *multiNamespaceInformer) Name() string {
	names := make([]string, 0, len(i.namespaceToInformer))
	for ns, informer := range i.namespaceToInformer {
		names = append(names, fmt.Sprintf("%s: %s", ns, informer.HasSyncedChecker().Name()))
	}
	return strings.Join(names, ", ")
}

func (i *multiNamespaceInformer) Done() <-chan struct{} {
	return i.synced
}

// IsStopped checks if each namespaced informer has stopped, returns false if any are still running.
func (i *multiNamespaceInformer) IsStopped() bool {
	for _, informer := range i.namespaceToInformer {
		if stopped := informer.IsStopped(); !stopped {
			return false
		}
	}
	return true
}
