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

func (b *BrokerGRPCServer) RemoveCluster(ctx context.Context, req *RemoveRequest) (*RemoveResponse, error) {
	b.Broker.RemoveClusterID(req.GetCluster())
	return &RemoveResponse{}, nil
}
