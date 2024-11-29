/*
Copyright 2024 Flant JSC

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

package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	controllercache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"storage-network-controller/internal/logger"
)

// SharedInformerCache  is an eventually consistent cache. The cache interface
// provides APIs to fetch specific k8s objects from the cache. Only a subset
// of k8s objects are currently managed by this cache.
// DO NOT USE it when you need the latest and accurate copy of a CR.
type SharedInformerCache interface {
	// GetNode returns the node info if present in the cache.
	GetNode(nodeName string) (*corev1.Node, error)
}

type cache struct {
	controllerCache controllercache.Cache
}

var (
	cacheLock sync.Mutex
	instance  SharedInformerCache
)

const (
	cacheNotInitializedErr = "shared informer cache has not been initialized yet"
)

func CreateSharedInformerCache(mgr manager.Manager, log *logger.Logger) error {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	if instance != nil {
		return fmt.Errorf("shared informer cache already initialized")
	}

	var err error

	sharedInformerCache := &cache{}
	// Set the global instance
	instance = sharedInformerCache
	sharedInformerCache.controllerCache, err = controllercache.New(mgr.GetConfig(), controllercache.Options{
		Scheme: mgr.GetScheme(),
		// flant.com TODO: make transformMap as in https://github.com/libopenstorage/stork/blob/828e9a057905b93cf1ad43155d9adac5ac8fe8c0/pkg/cache/cache.go#L72
		// when controller-runtime package will be at least v0.12.0
		// TransformByObject: transformMap,
	})
	if err != nil {
		log.Error(err, "error creating shared informer cache")
		return err
	}

	// indexing nodes by nodeName so that we can get node info by it's name
	err = sharedInformerCache.controllerCache.IndexField(context.Background(), &corev1.Node{}, "metadata.name", func(obj client.Object) []string {
		nodeObject, ok := obj.(*corev1.Node)
		if !ok {
			return []string{}
		}
		return []string{nodeObject.Name}
	})
	if err != nil {
		log.Error(err, "error indexing field name for nodes")
		return err
	}

	go sharedInformerCache.controllerCache.Start(context.Background())

	synced := sharedInformerCache.controllerCache.WaitForCacheSync(context.Background())
	if !synced {
		return fmt.Errorf("error syncing the shared informer cache")
	}

	log.Trace("Shared informer cache synced")

	return nil
}

func Instance() SharedInformerCache {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	return instance
}

// Only used for UTs
func SetTestInstance(s SharedInformerCache) {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	instance = s
}

// GetNode returns the node info if present in the cache.
func (c *cache) GetNode(nodeName string) (*corev1.Node, error) {
	if c == nil || c.controllerCache == nil {
		return nil, errors.New(cacheNotInitializedErr)
	}
	node := &corev1.Node{}
	if err := c.controllerCache.Get(context.Background(), client.ObjectKey{Name: nodeName}, node); err != nil {
		return nil, err
	}
	return node, nil
}
