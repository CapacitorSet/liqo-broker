package main

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"
	"k8s.io/klog/v2"
)

type BrokerGRPCServer struct {
	Server *grpc.Server
	Broker *Broker
	BrokerServer
	PushChannel Broker_SubscribeServer // A gRPC endpoint where to push update notifications
}

func (b *BrokerGRPCServer) Start(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf("%s%d", "0.0.0.0:", 7000))
	if err != nil {
		return err
	}
	b.Server = grpc.NewServer()
	RegisterBrokerServer(b.Server, b)
	go func() {
		<-ctx.Done()
		b.Server.GracefulStop()
	}()
	if err = b.Server.Serve(lis); err != nil {
		klog.Error(err)
		return err
	}
	return nil
}

func (b *BrokerGRPCServer) ReadResources(ctx context.Context, req *ReadRequest) (*ReadResponse, error) {
	response, err := b.Broker.ReadResources(ctx, req.GetOriginator())
	if err != nil {
		return &ReadResponse{}, err
	}
	protobufResponse := &ReadResponse{Resources: map[string]string{}}
	for name, value := range response {
		marshaled, err := value.MarshalJSON()
		if err != nil {
			return &ReadResponse{}, err
		}
		protobufResponse.Resources[name.String()] = string(marshaled)
	}
	return protobufResponse, nil
}

func (b *BrokerGRPCServer) Subscribe(req *SubscribeRequest, srv Broker_SubscribeServer) error {
	if b.PushChannel != nil {
		klog.Warningf("Subscribe(): a PushChannel was already configured, and was overwritten")
	}
	b.PushChannel = srv
	b.Broker.Register(b)
	select {}
}

// NotifyChange is called by the broker when resources change. It pushes the update to the upstream API.
func (b *BrokerGRPCServer) NotifyChange() {
	if b.PushChannel == nil {
		klog.Errorf("NotifyChange() was called with no configured PushChannel")
		return
	}
	err := b.PushChannel.Send(&UpdateNotification{})
	if err != nil {
		klog.Error(err)
	}
}

func (b *BrokerGRPCServer) RemoveCluster(ctx context.Context, req *RemoveRequest) (*RemoveResponse, error) {
	b.Broker.RemoveClusterID(req.GetCluster())
	return &RemoveResponse{}, nil
}
