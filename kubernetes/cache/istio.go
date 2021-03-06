package cache

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/tools/cache"

	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/log"
	"k8s.io/apimachinery/pkg/labels"
)

type (
	IstioCache interface {
		CheckIstioResource(resourceType string) bool
		GetIstioObjects(namespace string, resourceType string, labelSelector string) ([]kubernetes.IstioObject, error)
	}
)

func (c *kialiCacheImpl) CheckIstioResource(resourceType string) bool {
	// cacheIstioTypes stores the single types but for compatibility with kubernetes api resourceType will use plurals
	_, exist := c.cacheIstioTypes[kubernetes.PluralType[resourceType]]
	return exist
}

func (c *kialiCacheImpl) createIstioInformers(namespace string, informer *typeCache) {
	// Networking API
	if c.CheckIstioResource(kubernetes.VirtualServices) {
		(*informer)[kubernetes.VirtualServices] = createIstioIndexInformer(c.istioNetworkingGetter, kubernetes.VirtualServices, c.refreshDuration, namespace)
	}
	if c.CheckIstioResource(kubernetes.DestinationRules) {
		(*informer)[kubernetes.DestinationRules] = createIstioIndexInformer(c.istioNetworkingGetter, kubernetes.DestinationRules, c.refreshDuration, namespace)
	}
	if c.CheckIstioResource(kubernetes.Gateways) {
		(*informer)[kubernetes.Gateways] = createIstioIndexInformer(c.istioNetworkingGetter, kubernetes.Gateways, c.refreshDuration, namespace)
	}
	if c.CheckIstioResource(kubernetes.ServiceEntries) {
		(*informer)[kubernetes.ServiceEntries] = createIstioIndexInformer(c.istioNetworkingGetter, kubernetes.ServiceEntries, c.refreshDuration, namespace)
	}
}

func (c *kialiCacheImpl) isIstioSynced(namespace string) bool {
	var isSynced bool
	if nsCache, exist := c.nsCache[namespace]; exist {
		isSynced = nsCache[kubernetes.VirtualServices].HasSynced() &&
			nsCache[kubernetes.DestinationRules].HasSynced() &&
			nsCache[kubernetes.Gateways].HasSynced() &&
			nsCache[kubernetes.ServiceEntries].HasSynced()
	} else {
		isSynced = false
	}
	return isSynced
}

func createIstioIndexInformer(getter cache.Getter, resourceType string, refreshDuration time.Duration, namespace string) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(cache.NewListWatchFromClient(getter, resourceType, namespace, fields.Everything()),
		&kubernetes.GenericIstioObject{},
		refreshDuration,
		cache.Indexers{},
	)
}

func (c *kialiCacheImpl) GetIstioObjects(namespace string, resourceType string, labelSelector string) ([]kubernetes.IstioObject, error) {
	if !c.CheckIstioResource(resourceType) {
		return nil, fmt.Errorf("Kiali cache doesn't support [resourceType: %s]", resourceType)
	}
	if nsCache, nsOk := c.nsCache[namespace]; nsOk {
		resources := nsCache[resourceType].GetStore().List()
		lenResources := len(resources)
		if lenResources > 0 {
			_, ok := resources[0].(*kubernetes.GenericIstioObject)
			if !ok {
				return nil, fmt.Errorf("bad GenericIstioObject type found in cache for [resourceType: %s]", resourceType)
			}
			iResources := make([]kubernetes.IstioObject, lenResources)
			for i, r := range resources {
				iResources[i] = (r.(*kubernetes.GenericIstioObject)).DeepCopyIstioObject()
				// TODO iResource[i].SetTypeMeta(typeMeta) is missing/needed ??
			}
			if labelSelector != "" {
				if selector, err := labels.Parse(labelSelector); err == nil {
					iResources = kubernetes.FilterIstioObjectsForSelector(selector, iResources)
				} else {
					return []kubernetes.IstioObject{}, err
				}
			}
			log.Tracef("[Kiali Cache] Get [resourceType: %s] for [namespace: %s] =  %d", resourceType, namespace, lenResources)
			return iResources, nil
		}
	}
	return []kubernetes.IstioObject{}, nil
}
