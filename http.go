package main

import (
	"context"
	discoveryv1alpha1 "github.com/liqotech/liqo/apis/discovery/v1alpha1"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog/v2"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// HTTPServer exposes an HTTP API with an endpoint /api/clusters that presents the list of known foreign clusters (used in both the catalog and the orchestrator).
// When acting as an orchestrator it also exposes an endpoint /api/toggle that can be used to enable or disable the use of a cluster.
type HTTPServer struct {
	client.Client
	isOrchestrator bool
	*Orchestrator
}

func (s *HTTPServer) GetClusters(rw http.ResponseWriter, req *http.Request) {
	klog.Infof("GetClusters()")
	var clusters discoveryv1alpha1.ForeignClusterList
	err := s.List(context.Background(), &clusters)
	if err != nil {
		panic(err)
	}
	resp, err := json.Marshal(clusters.Items)
	if err != nil {
		panic(err)
	}
	_, err = rw.Write(resp)
	if err != nil {
		panic(err)
	}
}

func (s *HTTPServer) ToggleCluster(rw http.ResponseWriter, req *http.Request) {
	klog.Infof("ToggleCluster()")
	params := req.URL.Query()
	id := params.Get("id")
	enable := params.Get("enable") == "true"
	isInArray := false
	var pos int
	for _pos, elt := range s.EnabledClusters {
		if elt == id {
			isInArray = true
			pos = _pos
			break
		}
	}
	// Skip no-ops
	if enable == isInArray {
		return
	}
	if enable {
		s.EnabledClusters = append(s.EnabledClusters, id)
	} else {
		s.EnabledClusters = append(s.EnabledClusters[:pos], s.EnabledClusters[pos+1:]...)
	}
	klog.Infof("Enabled clusters: %#v", s.EnabledClusters)
}

func (s *HTTPServer) Start(ctx context.Context) error {
	http.HandleFunc("/api/clusters", s.GetClusters)
	if s.isOrchestrator {
		http.HandleFunc("/api/toggle", s.ToggleCluster)
	}
	srv := &http.Server{Addr: ":7000"}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}
