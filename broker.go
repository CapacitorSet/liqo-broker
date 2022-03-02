package main

import (
	"context"
	"fmt"
	"time"

	offv1alpha1 "github.com/liqotech/liqo/apis/offloading/v1alpha1"
	sharingv1alpha1 "github.com/liqotech/liqo/apis/sharing/v1alpha1"
	liqoconst "github.com/liqotech/liqo/pkg/consts"
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

// ResourceUpdateNotifier represents an interface to receive resource updates.
type ResourceUpdateNotifier interface {
	// NotifyChange signals that a change in resources may have occurred.
	NotifyChange()
}

// Broker monitors resources on foreign clusters. It implements the ResourceMonitor interface.
type Broker struct {
	client.Client
	notifier ResourceUpdateNotifier

	// nodeResources holds a list of clusters ("provider") with the resources they offer.
	nodeResources map[string]corev1.ResourceList

	nodeFactory informers.SharedInformerFactory
	nsFactory   informers.SharedInformerFactory
	// nodeInformer reacts to changes in virtual nodes.
	// Note that we currently use an Informer on Nodes (not on ResourceOffers) because when ResourceOffer are created the
	// corresponding VirtualNode may not be ready, and thus we may not be able to offload workloads yet.
	nodeInformer cache.SharedIndexInformer
	// nsInformer reacts to namespaces being offloaded on the broker.
	nsInformer cache.SharedIndexInformer
}

// NewBroker creates a new Broker.
func NewBroker(clientset kubernetes.Interface, resyncPeriod time.Duration, k8sClient client.Client) *Broker {
	nodeFactory := informers.NewSharedInformerFactoryWithOptions(
		clientset, resyncPeriod, informers.WithTweakListOptions(virtualNodesFilter),
	)
	nodeInformer := nodeFactory.Core().V1().Nodes().Informer()
	nsFactory := informers.NewSharedInformerFactory(clientset, resyncPeriod)
	nsInformer := nsFactory.Core().V1().Namespaces().Informer()

	broker := &Broker{
		nodeResources: map[string]corev1.ResourceList{},
		nodeFactory:   nodeFactory,
		nsFactory:     nsFactory,
		nodeInformer:  nodeInformer,
		nsInformer:    nsInformer,
		Client:        k8sClient,
	}

	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: broker.onNodeAddOrUpdate,
		UpdateFunc: func(oldObj, newObj interface{}) {
			broker.onNodeAddOrUpdate(newObj)
		},
		DeleteFunc: broker.onNodeDelete,
	})
	nsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: broker.onNamespaceAdd,
		// DeleteFunc is not necessary: when the offloaded namespace goes away, the NamespaceOffloading will also be deleted
	})

	return broker
}

func (b *Broker) Start(ctx context.Context) error {
	b.nodeFactory.Start(ctx.Done())
	b.nodeFactory.WaitForCacheSync(ctx.Done())
	b.nsFactory.Start(ctx.Done())
	b.nsFactory.WaitForCacheSync(ctx.Done())
	return nil
}

// ReadResources returns the resources available for the given cluster.
func (b *Broker) ReadResources(ctx context.Context, clusterID string) (corev1.ResourceList, error) {
	totalResources := make(corev1.ResourceList)
	for cluster := range b.nodeResources {
		// Ignore requester
		if cluster == clusterID {
			continue
		}
		resources, err := b.getClusterOffer(ctx, cluster)
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
func (b *Broker) Register(notifier ResourceUpdateNotifier) {
	b.notifier = notifier
}

// RemoveClusterID removes a cluster from internal data structures.
func (b *Broker) RemoveClusterID(clusterID string) {
	delete(b.nodeResources, clusterID)
}

// onNodeAddOrUpdate reacts to virtual nodes being created, and registers the corresponding ResourceOffer.
func (b *Broker) onNodeAddOrUpdate(obj interface{}) {
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

	resources, err := b.getClusterOffer(context.Background(), clusterID)
	if err != nil {
		// todo: use informer/keep polling for ResourceOffer, in case it is added later
		klog.Errorf("Failed to register resources for node %s: %s", node.Name, err)
		return
	}
	klog.Infof("Registering ResourceOffer for cluster %s", clusterID)
	b.nodeResources[clusterID] = resources.DeepCopy()
	b.notifyOrWarn()
}

func (b *Broker) onNodeDelete(obj interface{}) {
	node := obj.(*corev1.Node)
	klog.V(5).Infof("Node delete: %s", node.Name)
	clusterID := node.Labels[liqoconst.RemoteClusterID]
	if clusterID == "" {
		klog.V(5).Infof("Not a Liqo node", node.Name)
		return
	}
	klog.Infof("Unregistering ResourceOffer for cluster %s", clusterID)
	delete(b.nodeResources, clusterID)
	b.notifyOrWarn()
}

// onNamespaceAdd reacts to namespaces being offloaded on the broker and offloads them in turn on the assigned providers.
func (b *Broker) onNamespaceAdd(obj interface{}) {
	ns := obj.(*corev1.Namespace)
	klog.V(5).Infof("Namespace add: %s", ns.Name)
	clusterID := ns.Labels[liqoconst.RemoteClusterID]
	if clusterID == "" {
		klog.V(5).Infof("Not a Liqo namespace")
		return
	}

	klog.Infof("Creating a NamespaceOffloading in response to new namespace %s", ns.Name)
	nsOffloading := &offv1alpha1.NamespaceOffloading{
		ObjectMeta: metav1.ObjectMeta{
			Name:      liqoconst.DefaultNamespaceOffloadingName,
			Namespace: ns.Name,
		},
		Spec: offv1alpha1.NamespaceOffloadingSpec{
			NamespaceMappingStrategy: offv1alpha1.EnforceSameNameMappingStrategyType,
			PodOffloadingStrategy:    offv1alpha1.RemotePodOffloadingStrategyType,
			ClusterSelector: corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      liqoconst.TypeLabel,
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{liqoconst.TypeNode},
						},
						{
							Key:      liqoconst.RemoteClusterID,
							Operator: corev1.NodeSelectorOpNotIn,
							Values:   []string{clusterID},
						},
					},
				}},
			},
		},
		Status: offv1alpha1.NamespaceOffloadingStatus{},
	}
	err := b.Client.Create(context.TODO(), nsOffloading)
	if err != nil {
		klog.Errorf("onNamespaceAdd: %s", err)
	}
	b.notifyOrWarn()
}

// getClusterOffer returns the resources corresponding to a cluster's ResourceOffer.
func (b *Broker) getClusterOffer(ctx context.Context, clusterID string) (corev1.ResourceList, error) {
	offerList := &sharingv1alpha1.ResourceOfferList{}
	err := b.Client.List(ctx, offerList, client.MatchingLabels{
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

func (b *Broker) notifyOrWarn() {
	if b.notifier == nil {
		klog.Warning("No notifier is configured, an update will be lost")
	} else {
		b.notifier.NotifyChange()
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
