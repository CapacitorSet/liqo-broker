package main

import (
	"flag"
	discoveryv1alpha1 "github.com/liqotech/liqo/apis/discovery/v1alpha1"
	"os"
	"time"

	offloadingv1alpha1 "github.com/liqotech/liqo/apis/offloading/v1alpha1"
	sharingv1alpha1 "github.com/liqotech/liqo/apis/sharing/v1alpha1"
	"github.com/liqotech/liqo/pkg/utils/mapper"
	"github.com/liqotech/liqo/pkg/utils/restcfg"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = sharingv1alpha1.AddToScheme(scheme)
	_ = offloadingv1alpha1.AddToScheme(scheme)
	_ = discoveryv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	metricsAddr := flag.String("metrics-address", ":8080", "The address the metric endpoint binds to")
	probeAddr := flag.String("health-probe-address", ":8081", "The address the health probe endpoint binds to")

	isCatalog := flag.Bool("with-catalog", false, "Enable catalog features")
	isOrchestrator := flag.Bool("with-orchestrator", false, "Enable orchestrator features")
	isAggregator := flag.Bool("with-aggregator", false, "Enable aggregator features")

	// Global parameters
	resyncPeriod := flag.Duration("resync-period", 10*time.Hour, "The resync period for the informers")

	restcfg.InitFlags(nil)
	klog.InitFlags(nil)
	flag.Parse()

	if !*isCatalog && !*isOrchestrator && !*isAggregator {
		klog.Error("You must select one or more of --with-catalog, --with-orchestrator or --with-aggregator")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	config := restcfg.SetRateLimiter(ctrl.GetConfigOrDie())

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		MapperProvider:         mapper.LiqoMapperProvider(scheme),
		Scheme:                 scheme,
		MetricsBindAddress:     *metricsAddr,
		HealthProbeBindAddress: *probeAddr,
		LeaderElection:         false,
		Port:                   9443,
	})
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		klog.Error(err, " unable to set up health check")
		os.Exit(1)
	}
	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		klog.Error(err, " unable to set up ready check")
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal(err)
	}

	if *isAggregator {
		aggregator := NewAggregator(clientset, *resyncPeriod, mgr.GetClient())
		if err = mgr.Add(aggregator); err != nil {
			klog.Fatal(err)
		}
		grpcServer := &AggregatorGRPCServer{Aggregator: aggregator}
		if err = mgr.Add(grpcServer); err != nil {
			klog.Fatal(err)
		}
		if !*isOrchestrator {
			// We still need an orchestrator to reflect namespaces
			orchestrator := NewTrivialOrchestrator(clientset, *resyncPeriod, mgr.GetClient())
			if err = mgr.Add(orchestrator); err != nil {
				klog.Fatal(err)
			}
		}
	}

	if *isOrchestrator { // Also applies when both aggregator and orchestrator are selected
		orchestrator := NewOrchestrator(clientset, *resyncPeriod, mgr.GetClient())
		if err = mgr.Add(orchestrator); err != nil {
			klog.Fatal(err)
		}
		http := &HTTPServer{mgr.GetClient(), true, orchestrator}
		if err = mgr.Add(http); err != nil {
			klog.Fatal(err)
		}
	} else if *isCatalog {
		http := &HTTPServer{mgr.GetClient(), false, nil}
		if err = mgr.Add(http); err != nil {
			klog.Fatal(err)
		}
	}

	klog.Info("starting")
	if err = mgr.Start(ctx); err != nil {
		klog.Error(err)
		os.Exit(1)
	}
}
