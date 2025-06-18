/*
Copyright 2025 Flant JSC

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
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	clientCache "k8s.io/client-go/tools/cache"
	controllercache "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// SharedInformerCache  is an eventually consistent cache. The cache interface
// provides APIs to fetch specific k8s objects from the cache. Only a subset
// of k8s objects are currently managed by this cache.
// DO NOT USE it when you need the latest and accurate copy of a CR.
type SharedInformerCache interface {
	// block added by flant.com
	// GetNode returns the node info if present in the cache.
	GetNode(nodeName string) (*corev1.Node, error)
	// end of flant.com block

	// WatchPods registers the pod event handlers with the informer cache
	WatchPods(fn func(object interface{})) error

	// GetPersistentVolumeClaim returns the PersistentVolumeClaim in a namespace from the cache after applying TransformFunc
	GetPersistentVolumeClaim(pvcName string, namespace string) (*corev1.PersistentVolumeClaim, error)
}

type cache struct {
	controllerCache controllercache.Cache
}

var (
	cacheLock sync.Mutex
	instance  SharedInformerCache

	cacheNotInitializedErr = "shared informer cache has not been initialized yet"
)

func CreateSharedInformerCache(mgr manager.Manager) error {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	if instance != nil {
		return fmt.Errorf("shared informer cache already initialized")
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}

	sharedInformerCache := &cache{}
	// Set the global instance
	instance = sharedInformerCache
	sharedInformerCache.controllerCache, err = controllercache.New(config, controllercache.Options{
		Scheme: mgr.GetScheme(),
		// flant.com TODO: make transformMap as in https://github.com/libopenstorage/stork/blob/828e9a057905b93cf1ad43155d9adac5ac8fe8c0/pkg/cache/cache.go#L72
		// when controller-runtime package will be at least v0.12.0
		// TransformByObject: transformMap,
	})
	if err != nil {
		logrus.Errorf("error creating shared informer cache: %v", err)
		return err
	}
	// indexing pods by nodeName so that we can list all pods running on a node
	err = sharedInformerCache.controllerCache.IndexField(context.Background(), &corev1.Pod{}, "spec.nodeName", func(obj client.Object) []string {
		podObject, ok := obj.(*corev1.Pod)
		if !ok {
			return []string{}
		}
		return []string{podObject.Spec.NodeName}
	})
	if err != nil {
		logrus.Errorf("error indexing field spec.nodeName for pods: %v", err)
		return err
	}

	// block added by flant.com
	// indexing nodes by nodeName so that we can get node info by it's name
	err = sharedInformerCache.controllerCache.IndexField(context.Background(), &corev1.Node{}, "metadata.name", func(obj client.Object) []string {
		nodeObject, ok := obj.(*corev1.Node)
		if !ok {
			return []string{}
		}
		return []string{nodeObject.Name}
	})
	if err != nil {
		logrus.Errorf("error indexing field name for nodes: %v", err)
		return err
	}
	// end of flant.com block

	go sharedInformerCache.controllerCache.Start(context.Background())

	synced := sharedInformerCache.controllerCache.WaitForCacheSync(context.Background())
	if !synced {
		return fmt.Errorf("error syncing the shared informer cache")
	}

	logrus.Tracef("Shared informer cache synced")

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

// block added by flant.com
// GetNode returns the node info if present in the cache.
func (c *cache) GetNode(nodeName string) (*corev1.Node, error) {
	if c == nil || c.controllerCache == nil {
		return nil, fmt.Errorf(cacheNotInitializedErr)
	}
	node := &corev1.Node{}
	if err := c.controllerCache.Get(context.Background(), client.ObjectKey{Name: nodeName}, node); err != nil {
		return nil, err
	}
	return node, nil
}

// end of flant.com block

// WatchPods uses handlers for different pod events with shared informers.
func (c *cache) WatchPods(fn func(object interface{})) error {
	informer, err := c.controllerCache.GetInformer(context.Background(), &corev1.Pod{})
	if err != nil {
		logrus.WithError(err).Error("error getting the informer for pods")
		return err
	}

	informer.AddEventHandler(clientCache.ResourceEventHandlerFuncs{
		AddFunc: fn,
		UpdateFunc: func(oldObj, newObj interface{}) {
			// Only considering the new pod object
			fn(newObj)
		},
		DeleteFunc: fn,
	})

	return nil
}

// GetPersistentVolumeClaim returns the transformed PersistentVolumeClaim if present in the cache.
func (c *cache) GetPersistentVolumeClaim(pvcName string, namespace string) (*corev1.PersistentVolumeClaim, error) {
	if c == nil || c.controllerCache == nil {
		return nil, fmt.Errorf(cacheNotInitializedErr)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.controllerCache.Get(context.Background(), client.ObjectKey{Name: pvcName, Namespace: namespace}, pvc); err != nil {
		return nil, err
	}
	return pvc, nil
}
