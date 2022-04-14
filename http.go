package main

import (
	"context"
	discoveryv1alpha1 "github.com/liqotech/liqo/apis/discovery/v1alpha1"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/klog/v2"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type HTTPServer struct {
	client.Client
	*Broker
}

/*
  clusterID: "a0e4c7ae-5c82-488c-9205-560ffd3f0460",
  name: "TIM",
  pic: "https://www.top-ix.org/topix-dynamic-pages/cache/consorziati_logo/4061a8f5-1cc9-4b1f-8904-b30a623e00cc.jpg",
  authURL: "https://194.116.77.110:32632",
  uptime: "97.5%",
  academia: "No",
*/
type ClusterResponse struct {
	ClusterID string `json:"clusterID"`
	Name      string `json:"TIM"`
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

func (s *HTTPServer) GrafanaAPI(rw http.ResponseWriter, req *http.Request) {
	rw.Header().Set("Access-Control-Allow-Origin", "*")
	rw.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
	rw.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization,X-CSRF-Token")
	rw.Header().Set("Access-Control-Expose-Headers", "Authorization")
	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		panic(err)
	}
	klog.Infof(string(body))
	klog.Infof("GetClusters()")
	var clusters discoveryv1alpha1.ForeignClusterList
	err = s.List(context.Background(), &clusters)
	if err != nil {
		panic(err)
	}
	rw.Write([]byte(`[
  {
    "target":"pps in",
    "datapoints":[
      [622,1450754160000],
      [365,1450754220000]
    ]
  },
  {
    "target":"pps out",
    "datapoints":[
      [861,1450754160000],
      [767,1450754220000]
    ]
  },
  {
    "target":"errors out",
    "datapoints":[
      [861,1450754160000],
      [767,1450754220000]
    ]
  },
  {
    "target":"errors in",
    "datapoints":[
      [861,1450754160000],
      [767,1450754220000]
    ]
  }
]
`))
}

func (s *HTTPServer) Start(ctx context.Context) error {
	http.HandleFunc("/api/clusters", s.GetClusters)
	http.HandleFunc("/api/grafana", s.GrafanaAPI)
	http.HandleFunc("/api/grafana/search", s.GrafanaAPI)
	http.HandleFunc("/api/toggle", s.ToggleCluster)
	srv := &http.Server{Addr: ":7000"}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	return srv.ListenAndServe()
}
