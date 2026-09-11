/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package driver //nolint:revive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/deckhouse/sds-common-lib/logger"
	"github.com/deckhouse/sds-common-lib/monitoring"
	"github.com/deckhouse/sds-common-lib/monitoring/csimetrics"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/internal"
	"github.com/deckhouse/sds-replicated-volume/images/csi-driver/pkg/utils"
)

// metricsPrefix names the driver rather than the module: a module can ship a
// controller and a driver and their series have to be told apart.
const metricsPrefix = "sds_replicated_volume_csi"

const (
	// DefaultDriverName defines the name that is used in Kubernetes and the CSI
	// system for the canonical, official name of this plugin
	DefaultDriverName = "replicated.csi.storage.deckhouse.io"
	// DefaultAddress is the default address that the csi plugin will serve its
	// http handler on.
	DefaultAddress           = "127.0.0.1:12302"
	defaultWaitActionTimeout = 1 * time.Minute
)

var (
	version string
)

type Driver struct {
	name string

	csiAddress        string
	address           string
	hostID            string
	waitActionTimeout time.Duration

	srv     *grpc.Server
	httpSrv http.Server
	log     logger.Logger

	// metrics records what this driver serves. The zero value records nothing, so a
	// process started without METRICS_BIND_ADDRESS pays for no bookkeeping and needs
	// no nil check at the call site.
	metrics       csimetrics.Recorder
	metricsServer *monitoring.Server

	readyMu      sync.Mutex // protects ready
	ready        bool
	cl           client.Client
	storeManager utils.NodeStoreManager
	inFlight     *internal.InFlight

	csi.UnimplementedControllerServer
	csi.UnimplementedIdentityServer
	csi.UnimplementedNodeServer
}

// NewDriver returns a CSI plugin that contains the necessary gRPC
// interfaces to interact with Kubernetes over unix domain sockets for
// managing  disks
// NewDriver returns the driver. metricsBindAddress may be empty, which leaves the
// operation metrics unrecorded and opens no endpoint -- see Options.MetricsBindAddress.
func NewDriver(
	csiAddress, driverName, address string,
	nodeName *string,
	log logger.Logger,
	cl client.Client,
	metricsBindAddress string,
) (*Driver, error) {
	if driverName == "" {
		driverName = DefaultDriverName
	}

	st := utils.NewStore(log)

	d := &Driver{
		name:              driverName,
		hostID:            *nodeName,
		csiAddress:        csiAddress,
		address:           address,
		log:               log,
		waitActionTimeout: defaultWaitActionTimeout,
		cl:                cl,
		storeManager:      st,
		inFlight:          internal.NewInFlight(log),
	}

	if metricsBindAddress != "" {
		storage := monitoring.NewStorage()
		rec, err := csimetrics.New(storage, metricsPrefix)
		if err != nil {
			return nil, fmt.Errorf("declare the CSI metrics: %w", err)
		}
		d.metrics = rec
		d.metricsServer = monitoring.NewServer(metricsBindAddress, storage, log.Named("metrics"))
	}

	return d, nil
}

func (d *Driver) Run(ctx context.Context) error {
	u, err := url.Parse(d.csiAddress)
	if err != nil {
		return fmt.Errorf("unable to parse address: %q", err)
	}

	fmt.Print("d.csiAddress", d.csiAddress)
	fmt.Print("u", u)

	grpcAddr := path.Join(u.Host, filepath.FromSlash(u.Path))
	if u.Host == "" {
		grpcAddr = filepath.FromSlash(u.Path)
	}

	fmt.Print("grpcAddr", grpcAddr)

	// CSI plugins talk only over UNIX sockets currently
	if u.Scheme != "unix" {
		return fmt.Errorf("currently only unix domain sockets are supported, have: %s", u.Scheme)
	}
	// remove the socket if it's already there. This can happen if we
	// deploy a new version and the socket was created from the old running
	// plugin.
	d.log.Info(fmt.Sprintf("socket %s removing socket", grpcAddr))
	if err := os.Remove(grpcAddr); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove unix domain socket file %s, error: %s", grpcAddr, err)
	}

	grpcListener, err := net.Listen(u.Scheme, grpcAddr)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	// log response errors for better observability
	errHandler := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)
		if err != nil {
			d.log.Error(fmt.Sprintf("method %s method failed ", info.FullMethod), logger.Err(err))
		}
		return resp, err
	}

	// The recorder goes first so that it times the handler and the error logging
	// together; with no metrics configured its interceptor is not installed at all.
	interceptors := []grpc.UnaryServerInterceptor{errHandler}
	if d.metricsServer != nil {
		interceptors = append([]grpc.UnaryServerInterceptor{d.metrics.UnaryServerInterceptor()}, interceptors...)
	}

	d.srv = grpc.NewServer(grpc.ChainUnaryInterceptor(interceptors...))
	csi.RegisterIdentityServer(d.srv, d)
	csi.RegisterControllerServer(d.srv, d)
	csi.RegisterNodeServer(d.srv, d)

	httpListener, err := net.Listen("tcp", d.address)
	if err != nil {
		return fmt.Errorf("failed to listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	d.httpSrv = http.Server{
		Handler: mux,
	}

	d.ready = true
	d.log.Info(fmt.Sprintf("grpc_addr %s http_addr %s starting server", grpcAddr, d.address))

	var eg errgroup.Group
	eg.Go(func() error {
		<-ctx.Done()
		return d.httpSrv.Shutdown(context.Background()) // TODO: Should we use just ctx here?
	})
	eg.Go(func() error {
		go func() {
			<-ctx.Done()
			d.log.Info("server stopped")
			d.readyMu.Lock()
			d.ready = false
			d.readyMu.Unlock()
			d.srv.GracefulStop()
		}()
		return d.srv.Serve(grpcListener)
	})
	eg.Go(func() error {
		err := d.httpSrv.Serve(httpListener)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})
	if d.metricsServer != nil {
		eg.Go(func() error { return d.metricsServer.Start(ctx) })
	}

	return eg.Wait()
}
