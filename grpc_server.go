package main

import (
	"context"
	"fmt"
	"net"

	"github.com/liqotech/liqo/pkg/liqo-controller-manager/resource-request-controller/resource-monitors"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

// AggregatorGRPCServer exposes the Aggregator over gRPC.
type AggregatorGRPCServer struct {
	Server *grpc.Server
	Aggregator *Aggregator
	resourcemonitors.ResourceReaderServer
	PushChannel resourcemonitors.ResourceReader_SubscribeServer // A gRPC endpoint where to push update notifications
}

func (a *AggregatorGRPCServer) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf("%s%d", "0.0.0.0:", 7000))
	if err != nil {
		return err
	}
	a.Server = grpc.NewServer()
	resourcemonitors.RegisterResourceReaderServer(a.Server, a)
	go func() {
		<-ctx.Done()
		a.Server.GracefulStop()
	}()
	if err = a.Server.Serve(lis); err != nil {
		klog.Error(err)
		return err
	}
	return nil
}

func (a *AggregatorGRPCServer) ReadResources(ctx context.Context, req *resourcemonitors.ReadRequest) (*resourcemonitors.ReadResponse, error) {
	response, err := a.Aggregator.ReadResources(ctx, req.GetOriginator())
	if err != nil {
		return &resourcemonitors.ReadResponse{}, err
	}
	protobufResponse := &resourcemonitors.ReadResponse{Resources: map[string]string{}}
	for name, value := range response {
		marshaled, err := value.MarshalJSON()
		if err != nil {
			return &resourcemonitors.ReadResponse{}, err
		}
		protobufResponse.Resources[name.String()] = string(marshaled)
	}
	return protobufResponse, nil
}

func (a *AggregatorGRPCServer) Subscribe(req *resourcemonitors.SubscribeRequest, srv resourcemonitors.ResourceReader_SubscribeServer) error {
	if a.PushChannel != nil {
		klog.Warningf("Subscribe(): a PushChannel was already configured, and was overwritten")
	}
	a.PushChannel = srv
	a.Aggregator.Register(a)
	select {}
}

// NotifyChange is called by the broker when resources change. It pushes the update to the upstream API.
func (a *AggregatorGRPCServer) NotifyChange() {
	if a.PushChannel == nil {
		klog.Errorf("NotifyChange() was called with no configured PushChannel")
		return
	}
	err := a.PushChannel.Send(&resourcemonitors.UpdateNotification{})
	if err != nil {
		klog.Error(err)
	}
}

func (a *AggregatorGRPCServer) RemoveCluster(ctx context.Context, req *resourcemonitors.RemoveRequest) (*resourcemonitors.RemoveResponse, error) {
	a.Aggregator.RemoveClusterID(req.GetCluster())
	return &resourcemonitors.RemoveResponse{}, nil
}
