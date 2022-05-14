package main

import (
	"context"
	"fmt"
	"time"

	sharingv1alpha1 "github.com/liqotech/liqo/apis/sharing/v1alpha1"
	liqoconst "github.com/liqotech/liqo/pkg/consts"
	monitors "github.com/liqotech/liqo/pkg/liqo-controller-manager/resource-request-controller/resource-monitors"
	"github.com/liqotech/liqo/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Aggregator monitors resources on foreign clusters, which can be read with the monitors.ResourceReader interface.
type Aggregator struct {
	client.Client
	notifier monitors.ResourceUpdateNotifier

	// nodeResources holds a list of clusters ("provider") with the resources they offer.
	nodeResources map[string]corev1.ResourceList

	nodeFactory informers.SharedInformerFactory
	// nodeInformer reacts to changes in virtual nodes.
	// Note that we currently use an Informer on Nodes (not on ResourceOffers) because when ResourceOffer are created the
	// corresponding VirtualNode may not be ready, and thus we may not be able to offload workloads yet.
	nodeInformer cache.SharedIndexInformer
}

// NewAggregator creates a new Aggregator.
func NewAggregator(clientset kubernetes.Interface, resyncPeriod time.Duration, k8sClient client.Client) *Aggregator {
	nodeFactory := informers.NewSharedInformerFactoryWithOptions(
		clientset, resyncPeriod, informers.WithTweakListOptions(virtualNodesFilter),
	)
	nodeInformer := nodeFactory.Core().V1().Nodes().Informer()

	broker := &Aggregator{
		nodeResources: map[string]corev1.ResourceList{},
		nodeFactory:   nodeFactory,
		nodeInformer:  nodeInformer,
		Client:        k8sClient,
	}

	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: broker.onNodeAddOrUpdate,
		UpdateFunc: func(oldObj, newObj interface{}) {
			broker.onNodeAddOrUpdate(newObj)
		},
		DeleteFunc: broker.onNodeDelete,
	})

	return broker
}

func (a *Aggregator) Start(ctx context.Context) error {
	a.nodeFactory.Start(ctx.Done())
	a.nodeFactory.WaitForCacheSync(ctx.Done())
	return nil
}

// ReadResources returns the resources available for the given cluster.
func (a *Aggregator) ReadResources(ctx context.Context, clusterID string) (corev1.ResourceList, error) {
	totalResources := make(corev1.ResourceList)
	for cluster := range a.nodeResources {
		// Ignore requester
		if cluster == clusterID {
			continue
		}
		resources, err := a.getClusterOffer(ctx, cluster)
		if err != nil {
			klog.Errorf("Reading cluster offer for %s: %s", cluster, err)
			return nil, err
		}
		// Simple aggregation policy
		mergeResources(totalResources, resources)
	}

	if resourceIsEmpty(totalResources) {
		klog.Warningf("No resources found for cluster %s", clusterID)
	}
	return totalResources, nil
}

// Register registers a notifier.
func (a *Aggregator) Register(notifier monitors.ResourceUpdateNotifier) {
	a.notifier = notifier
}

// RemoveClusterID removes a cluster from internal data structures.
func (a *Aggregator) RemoveClusterID(clusterID string) {
	delete(a.nodeResources, clusterID)
}

// onNodeAddOrUpdate reacts to virtual nodes being created, and registers the corresponding ResourceOffer.
func (a *Aggregator) onNodeAddOrUpdate(obj interface{}) {
	node := obj.(*corev1.Node)
	klog.V(5).Infof("Node add: %s", node.Name)
	// Do not register the ResourceOffer until the node is ready
	if !utils.IsNodeReady(node) {
		klog.V(5).Infof("Node is not ready", node.Name)
		return
	}
	clusterID := node.Labels[liqoconst.RemoteClusterID]
	if clusterID == "" {
		return
	}

	resources, err := a.getClusterOffer(context.Background(), clusterID)
	if err != nil {
		// todo: use informer/keep polling for ResourceOffer, in case it is added later
		klog.Errorf("Failed to register resources for node %s: %s", node.Name, err)
		return
	}
	klog.Infof("Registering ResourceOffer for cluster %s", clusterID)
	a.nodeResources[clusterID] = resources.DeepCopy()
	a.notifyOrWarn()
}

func (a *Aggregator) onNodeDelete(obj interface{}) {
	node := obj.(*corev1.Node)
	klog.V(5).Infof("Node delete: %s", node.Name)
	clusterID := node.Labels[liqoconst.RemoteClusterID]
	if clusterID == "" {
		klog.V(5).Infof("Not a Liqo node", node.Name)
		return
	}
	klog.Infof("Unregistering ResourceOffer for cluster %s", clusterID)
	delete(a.nodeResources, clusterID)
	a.notifyOrWarn()
}

// getClusterOffer returns the resources corresponding to a cluster's ResourceOffer.
func (a *Aggregator) getClusterOffer(ctx context.Context, clusterID string) (corev1.ResourceList, error) {
	offerList := &sharingv1alpha1.ResourceOfferList{}
	err := a.Client.List(ctx, offerList, client.MatchingLabels{
		liqoconst.ReplicationOriginLabel: clusterID,
	})
	if err != nil {
		return nil, err
	}

	if len(offerList.Items) != 1 {
		return nil, fmt.Errorf("too many offers for cluster %s", clusterID)
	}

	return offerList.Items[0].Spec.ResourceQuota.Hard, nil
}

func (a *Aggregator) notifyOrWarn() {
	if a.notifier == nil {
		klog.Warning("No notifier is configured, an update will be lost")
	} else {
		a.notifier.NotifyChange()
	}
}

// resourceIsEmpty checks if the ResourceList is empty.
func resourceIsEmpty(list corev1.ResourceList) bool {
	for _, val := range list {
		if !val.IsZero() {
			return false
		}
	}
	return true
}

// mergeResources adds the resources of b to a.
func mergeResources(a, b corev1.ResourceList) {
	for key, val := range b {
		if prev, ok := a[key]; ok {
			prev.Add(val)
			a[key] = prev
		} else {
			a[key] = val.DeepCopy()
		}
	}
}

// virtualNodesFilter filters only virtual nodes.
func virtualNodesFilter(options *metav1.ListOptions) {
	req, err := labels.NewRequirement(liqoconst.TypeLabel, selection.Equals, []string{liqoconst.TypeNode})
	if err != nil {
		return
	}
	options.LabelSelector = labels.NewSelector().Add(*req).String()
}
