package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
	srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	sv1 "k8s.io/api/storage/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/yaml"

	"scheduler-extender/pkg/cache"
	"scheduler-extender/pkg/controller"
	"scheduler-extender/pkg/kubutils"
	"scheduler-extender/pkg/logger"
	"scheduler-extender/pkg/scheduler"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	defaultDivisor                = 1
	defaultListenAddr             = ":8000"
	defaultCacheSize              = 10
	defaultcertFile               = "/etc/sds-local-volume-scheduler-extender/certs/tls.crt"
	defaultkeyFile                = "/etc/sds-local-volume-scheduler-extender/certs/tls.key"
	defaultConfigMapUpdateTimeout = 5
	defaultCacheCheckInterval     = 1
	defaultCachePVCTTL            = 3600
	defaultCachePVCCheckInterval  = 3600
	defaultLogLevel               = "2"
)

type Config struct {
	DefaultDivisor         float64 `json:"default-divisor"`
	ListenAddr             string  `json:"listen"`
	LogLevel               string  `json:"log-level"`
	HealthProbeBindAddress string  `json:"health-probe-bind-address"`
	CertFile               string  `json:"cert-file"`
	KeyFile                string  `json:"key-file"`
	CacheSize              int     `json:"cache-size"`
	PVCTTL                 int     `json:"pvc-ttl"`
	CfgMapUpdateTimeout    int     `json:"configmap-update-timeout"`
	CacheCheckInterval     int     `json:"cache-check-interval"`
	CachePVCCheckInterval  int     `json:"cache-pvc-check-interval"`
}

var cfgFilePath string

var resourcesSchemeFuncs = []func(*runtime.Scheme) error{
	srv.AddToScheme,
	snc.AddToScheme,
	v1.AddToScheme,
	sv1.AddToScheme,
}

var config = &Config{
	ListenAddr:            defaultListenAddr,
	DefaultDivisor:        defaultDivisor,
	LogLevel:              defaultLogLevel,
	CacheSize:             defaultCacheSize,
	CertFile:              defaultcertFile,
	KeyFile:               defaultkeyFile,
	PVCTTL:                defaultCachePVCTTL,
	CfgMapUpdateTimeout:   defaultConfigMapUpdateTimeout,
	CacheCheckInterval:    defaultCacheCheckInterval,
	CachePVCCheckInterval: defaultCachePVCCheckInterval,
}

var rootCmd = &cobra.Command{
	Use:     "sds-replicated-volume-scheduler",
	Version: "development",
	Short:   "a scheduler-extender for sds-replicated-volume",
	Long: `A scheduler-extender for sds-replicated-volume.
The extender implements filter and prioritize verbs.
The filter verb is "filter" and served at "/filter" via HTTP.
It filters out nodes that have less storage capacity than requested.
The prioritize verb is "prioritize" and served at "/prioritize" via HTTP.
It scores nodes with this formula:
    min(10, max(0, log2(capacity >> 30 / divisor)))
The default divisor is 1.  It can be changed with a command-line option.
`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// to avoid printing usage information when error is returned
		cmd.SilenceUsage = true
		// to avoid printing errors (we log it closer to the place where it has happened)
		cmd.SilenceErrors = true
		return subMain(cmd.Context())
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFilePath, "config", "", "config file")
}

func main() {
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		// we expect err to be logged already
		os.Exit(1)
	}
}

func subMain(ctx context.Context) error {
	if len(cfgFilePath) != 0 {
		b, err := os.ReadFile(cfgFilePath)
		if err != nil {
			print(err)
			return err
		}

		if err = yaml.Unmarshal(b, config); err != nil {
			print(err)
			return err
		}
	}

	log, err := logger.NewLogger(logger.Verbosity(config.LogLevel))
	if err != nil {
		print(fmt.Sprintf("[subMain] unable to initialize logger, err: %s", err))
		return err
	}
	log.Info(fmt.Sprintf("[subMain] logger has been initialized, log level: %s", config.LogLevel))
	ctrl.SetLogger(log.GetLogger())

	kConfig, err := kubutils.KubernetesDefaultConfigCreate()
	if err != nil {
		log.Error(err, "[subMain] unable to KubernetesDefaultConfigCreate")
		return err
	}
	log.Info("[subMain] kubernetes config has been successfully created.")

	scheme := runtime.NewScheme()
	for _, f := range resourcesSchemeFuncs {
		if err := f(scheme); err != nil {
			log.Error(err, "[subMain] unable to add scheme to func")
			return err
		}
	}
	log.Info("[subMain] successfully read scheme CR")

	managerOpts := manager.Options{
		Scheme:                 scheme,
		Logger:                 log.GetLogger(),
		HealthProbeBindAddress: config.HealthProbeBindAddress,
		BaseContext:            func() context.Context { return ctx },
	}

	mgr, err := manager.New(kConfig, managerOpts)
	if err != nil {
		log.Error(err, "[subMain] unable to create manager for creating controllers")
		return err
	}

	сache := cache.NewCache(log)
	cacheMrg := cache.NewCacheManager(сache, &sync.Mutex{}, log)
	log.Info("[subMain] scheduler cache manager initialized")

	go cacheMrg.RunCleaner(ctx, time.Duration(config.CachePVCCheckInterval))
	log.Info("[subMain] scheduler cleanup process started")

	go cacheMrg.RunSaver(ctx, time.Duration(config.CacheCheckInterval), time.Duration(config.CfgMapUpdateTimeout))
	log.Info("[subMain] scheduler cache saver started")

	h, err := scheduler.NewHandler(ctx, mgr.GetClient(), *log, сache, config.DefaultDivisor)
	if err != nil {
		log.Error(err, "[subMain] unable to create http.Handler of the scheduler extender")
		return err
	}
	log.Info("[subMain] scheduler handler initialized")

	if err = controller.RunPVCWatcherCacheController(mgr, *log, cacheMrg); err != nil {
		log.Error(err, fmt.Sprintf("[subMain] unable to run %s controller", controller.PVCWatcherCacheCtrlName))
		return err
	}
	log.Info(fmt.Sprintf("[subMain] successfully ran %s controller", controller.PVCWatcherCacheCtrlName))

	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "[subMain] unable to mgr.AddHealthzCheck")
		return err
	}
	log.Info("[subMain] successfully AddHealthzCheck")

	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "[subMain] unable to mgr.AddReadyzCheck")
		return err
	}
	log.Info("[subMain] successfully AddReadyzCheck")

	serv := &http.Server{
		Addr:         config.ListenAddr,
		Handler:      accessLogHandler(ctx, h),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	log.Info("[subMain] server was initialized")

	return runServer(ctx, serv, mgr, log)
}

func runServer(ctx context.Context, serv *http.Server, mgr manager.Manager, log *logger.Logger) error {
	ctx, stop := context.WithCancel(ctx)

	var wg sync.WaitGroup
	defer wg.Wait()
	defer stop() // stop() should be called before wg.Wait() to stop the goroutine correctly.
	wg.Add(1)

	go func() {
		defer wg.Done()
		<-ctx.Done()
		if err := serv.Shutdown(ctx); err != nil {
			log.Error(err, "[runServer] failed to shutdown gracefully")
		}
	}()

	go func() {
		log.Info("[runServer] kube manager will start now")
		if err := mgr.Start(ctx); err != nil {
			log.Error(err, "[runServer] unable to mgr.Start")
		}
	}()

	log.Info(fmt.Sprintf("[runServer] starts serving on: %s", config.ListenAddr))

	if err := serv.ListenAndServeTLS(config.CertFile, config.KeyFile); !errors.Is(err, http.ErrServerClosed) {
		log.Error(err, "[runServer] unable to run the server")
		return err
	}

	return nil
}
