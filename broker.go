package main

import (
	"context"
	"time"

	offv1alpha1 "github.com/liqotech/liqo/apis/offloading/v1alpha1"
	liqoconst "github.com/liqotech/liqo/pkg/consts"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Broker struct {
	client.Client
	EnabledClusters []string

	nsFactory informers.SharedInformerFactory
}

// NewBroker creates a new Broker.
func NewBroker(clientset kubernetes.Interface, resyncPeriod time.Duration, k8sClient client.Client) *Broker {
	nsFactory := informers.NewSharedInformerFactory(clientset, resyncPeriod)
	nsInformer := nsFactory.Core().V1().Namespaces().Informer()

	broker := &Broker{
		nsFactory:       nsFactory,
		EnabledClusters: []string{},
		Client:          k8sClient,
	}

	nsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: broker.onNamespaceAdd,
		// DeleteFunc is not necessary: when the offloaded namespace goes away, the NamespaceOffloading will also be deleted
	})

	return broker
}

func (b *Broker) Start(ctx context.Context) error {
	b.nsFactory.Start(ctx.Done())
	b.nsFactory.WaitForCacheSync(ctx.Done())
	return nil
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
							// Disable originating cluster
							Key:      liqoconst.RemoteClusterID,
							Operator: corev1.NodeSelectorOpNotIn,
							Values:   []string{clusterID},
						},
						{
							Key:      liqoconst.RemoteClusterID,
							Operator: corev1.NodeSelectorOpIn,
							Values:   b.EnabledClusters,
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
}
