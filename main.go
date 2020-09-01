package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	ecds "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	fault "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/fault/v3"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	configdiscovery "github.com/envoyproxy/go-control-plane/envoy/service/extension/v3"
	envoytype "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/types"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/golang/protobuf/ptypes"
	"google.golang.org/grpc"
)

var ecdsTypeUrl string

func init() {
	m := ecds.TypedExtensionConfig{}
	ecdsTypeUrl = "type.googleapis.com/" + string(m.ProtoReflect().Descriptor().FullName())
}

// Wrapper around the server, just so we can implement the specific gRPC API.
type ecdsServer struct {
	serverv3.Server
}

func (s *ecdsServer) StreamExtensionConfigs(ss configdiscovery.ExtensionConfigDiscoveryService_StreamExtensionConfigsServer) error {
	return s.StreamHandler(ss, ecdsTypeUrl)
}

func (*ecdsServer) DeltaExtensionConfigs(configdiscovery.ExtensionConfigDiscoveryService_DeltaExtensionConfigsServer) error {
	return nil
}
func (*ecdsServer) FetchExtensionConfigs(context.Context, *discoverygrpc.DiscoveryRequest) (*discoverygrpc.DiscoveryResponse, error) {
	return nil, nil
}

type faultHandler struct {
	ch chan<- uint32
}

func (f *faultHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fault := r.URL.Query().Get("fault")
	faultValue, err := strconv.Atoi(fault)
	if err != nil {
		w.WriteHeader(400)
		return
	}

	f.ch <- uint32(faultValue)
	w.WriteHeader(200)
}

func startHttpServer(ctx context.Context) <-chan uint32 {
	ch := make(chan uint32)

	http.Handle("/fault", &faultHandler{ch: ch})
	s := http.Server{}

	l, err := net.Listen("tcp", ":8000")
	if err != nil {
		panic(err)
	}

	go s.Serve(l)

	go func() {
		<-ctx.Done()
		s.Shutdown(context.Background())
	}()

	return ch
}

func makeFaultConfig(percentage uint32) *ecds.TypedExtensionConfig {
	faultFilter := &fault.HTTPFault{
		Abort: &fault.FaultAbort{
			ErrorType: &fault.FaultAbort_HttpStatus{
				HttpStatus: 503,
			},
			Percentage: &envoytype.FractionalPercent{
				Numerator:   percentage,
				Denominator: envoytype.FractionalPercent_HUNDRED,
			},
		},
	}

	a, err := ptypes.MarshalAny(faultFilter)
	if err != nil {
		panic(err)
	}
	return &ecds.TypedExtensionConfig{
		Name:        "experiments",
		TypedConfig: a,
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())

	// Start a HTTP server that handles calls to update the fault config.
	ch := startHttpServer(ctx)

	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
	}()

	// We need to use the new linear cache to serve ECDS since its not natively supported by gcp.
	cache := cachev3.NewLinearCache(string(ecdsTypeUrl), cache.WithInitialResources(map[string]types.Resource{"experiments": makeFaultConfig(0)}))
	server := &ecdsServer{serverv3.NewServer(ctx, cache, nil)}

	go func() {
		for {
			select {
			case v := <-ch:
				err := cache.UpdateResource("experiments", makeFaultConfig(v))
				if err != nil {
					panic(err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	grpcServer := grpc.NewServer()

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", 1100))
	if err != nil {
		log.Fatal(err)
	}

	configdiscovery.RegisterExtensionConfigDiscoveryServiceServer(grpcServer, server)

	grpcServer.Serve(l)
}
