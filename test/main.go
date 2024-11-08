package main

import (
    "context"
    "fmt"
    "strconv"
    "strings"

    lapi "github.com/LINBIT/golinstor/client"
    snc "github.com/deckhouse/sds-node-configurator/api/v1alpha1"
    "github.com/deckhouse/sds-replicated-volume/api/linstor"
    srv "github.com/deckhouse/sds-replicated-volume/api/v1alpha1"
    v1 "k8s.io/api/core/v1"
    sv1 "k8s.io/api/storage/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
    apiruntime "k8s.io/apimachinery/pkg/runtime"
    clientgoscheme "k8s.io/client-go/kubernetes/scheme"
    "sigs.k8s.io/controller-runtime/pkg/cache"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/manager"

    "sds-replicated-volume-controller/config"
    "sds-replicated-volume-controller/pkg/controller"
    kubutils "sds-replicated-volume-controller/pkg/kubeutils"
    "sds-replicated-volume-controller/pkg/logger"

    _ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

const (
    PVCSIDriver                             = "replicated.csi.storage.deckhouse.io"
    placementCountSCKey                     = "placementCount"
)

var (
    resourcesSchemeFuncs = []func(*apiruntime.Scheme) error{
        srv.AddToScheme,
        snc.AddToScheme,
        linstor.AddToScheme,
        clientgoscheme.AddToScheme,
        extv1.AddToScheme,
        v1.AddToScheme,
        sv1.AddToScheme,
    }
)


func main() {
    // Create default config Kubernetes client
    kConfig, err := kubutils.KubernetesDefaultConfigCreate()
    if err != nil {
        fmt.Println(err)
        panic("error by reading a kubernetes configuration")
    }

    fmt.Println("  step 1.1")

    // Setup scheme for all resources
    scheme := apiruntime.NewScheme()
    for _, f := range resourcesSchemeFuncs {
        err := f(scheme)
        if err != nil {
            panic("failed to add to scheme")
        }
    }

    fmt.Println("  step 1.2")

    cfgParams, err := config.NewConfig()
    if err != nil {
            fmt.Println("unable to create NewConfig " + err.Error())
    }

    fmt.Println("ControllerNamespace: " + cfgParams.ControllerNamespace)

    fmt.Println("  step 1.2.1")

    log, err := logger.NewLogger(cfgParams.Loglevel)
    if err != nil {
            fmt.Printf("unable to create NewLogger, err: %v\n", err)
            panic("unable to create NewLogger")
    }

    cacheOpt := cache.Options{
        DefaultNamespaces: map[string]cache.Config{
            cfgParams.ControllerNamespace: {},
        },
    }

    fmt.Println("  step 1.3")

    managerOpts := manager.Options{
        Scheme: scheme,
        // MetricsBindAddress: cfgParams.MetricsPort,
        HealthProbeBindAddress:  cfgParams.HealthProbeBindAddress,
        Cache:                   cacheOpt,
        LeaderElection:          true,
        LeaderElectionNamespace: cfgParams.ControllerNamespace,
        LeaderElectionID:        config.ControllerName,
    }

    fmt.Println("  step 1.4")

    mgr, err := manager.New(kConfig, managerOpts)
    if err != nil {
            fmt.Println(err)
            panic("failed to create a manager")
    }

    lc, err := lapi.NewClient()
    if err != nil {
            fmt.Println(err)
            panic("failed to create a linstor client")
    }

    fmt.Println("  step 1.5")


    // VVVVVVVVVVVVVVVVVVVVVVVVVVVVV

    if _, err := controller.NewLinstorNode(mgr, lc, cfgParams.ConfigSecretName, cfgParams.ScanInterval, *log); err != nil {
            fmt.Println(err)
            panic("failed to create the NewLinstorNode controller")
    }

    if _, err := controller.NewReplicatedStorageClass(mgr, cfgParams, *log); err != nil {
            fmt.Println(err)
            panic("failed to create the NewReplicatedStorageClass controller")
    }

    if _, err := controller.NewReplicatedStoragePool(mgr, lc, cfgParams.ScanInterval, *log); err != nil {
            fmt.Println(err)
            panic("failed to create the NewReplicatedStoragePool controller")
    }

    if err = controller.NewLinstorPortRangeWatcher(mgr, lc, cfgParams.ScanInterval, *log); err != nil {
            fmt.Println(err)
            panic("failed to create the NewLinstorPortRangeWatcher controller")
    }

    if err = controller.NewLinstorLeader(mgr, cfgParams.LinstorLeaseName, cfgParams.ScanInterval, *log); err != nil {
            fmt.Println(err)
            panic("failed to create the NewLinstorLeader controller")
    }

    if err = controller.NewStorageClassAnnotationsReconciler(mgr, cfgParams.ScanInterval, *log); err != nil {
            fmt.Println(err)
            panic("failed to create the NewStorageClassAnnotationsReconciler controller")
    }

    // AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA


    NewLinstorResourcesWatcher(mgr, lc, cfgParams.LinstorResourcesReconcileInterval)

//    fmt.Println("  step 1.6")
//    fmt.Println(mgr)
//
//    fmt.Println("  step 1.7")
//    fmt.Println(lc)
}

func GetStorageClasses(ctx context.Context, cl client.Client) ([]sv1.StorageClass, error) {
    listStorageClasses := &sv1.StorageClassList{
        TypeMeta: metav1.TypeMeta{
                Kind:       "StorageClass",
                APIVersion: "storage.k8s.io/v1",
        },      
    }                       
    err := cl.List(ctx, listStorageClasses)
    if err != nil {
            return nil, err
    }               
    return listStorageClasses.Items, nil
}

func NewLinstorResourcesWatcher(mgr manager.Manager, lc *lapi.Client, interval int) {
    cl := mgr.GetClient()
    ctx := context.Background()

    scs, err := GetStorageClasses(ctx, cl)
    if err != nil {
            fmt.Println(err)
            panic("[NewLinstorResourcesWatcher] unable to get Kubernetes Storage Classes")
    }

    scMap := make(map[string]sv1.StorageClass, len(scs))
    for _, sc := range scs {
            scMap[sc.Name] = sc
    }

    rgs, err := lc.ResourceGroups.GetAll(ctx)
    if err != nil { 
            fmt.Println(err)
            panic("[NewLinstorResourcesWatcher] unable to get Linstor Resource Groups")
    }

    rds, err := lc.ResourceDefinitions.GetAll(ctx, lapi.RDGetAllRequest{})
    if err != nil {
            fmt.Println(err)
            panic("[NewLinstorResourcesWatcher] unable to get Linstor Resource Definitions")
    }

    rdMap := make(map[string]lapi.ResourceDefinitionWithVolumeDefinition, len(rds))
    for _, rd := range rds {
            rdMap[rd.Name] = rd
    }

    rgMap := make(map[string]lapi.ResourceGroup, len(rgs))
    for _, rg := range rgs {
            rgMap[rg.Name] = rg
    }

    ReconcileParams(ctx, cl, lc, scMap, rdMap, rgMap)
}


func GetListPV(ctx context.Context, cl client.Client) ([]v1.PersistentVolume, error) {
    PersistentVolumeList := &v1.PersistentVolumeList{}
    err := cl.List(ctx, PersistentVolumeList)
    if err != nil {
            return nil, err
    }
    return PersistentVolumeList.Items, nil
}

func ReconcileParams(
    ctx context.Context,
    cl client.Client,
    lc *lapi.Client,
    scs map[string]sv1.StorageClass,
    rds map[string]lapi.ResourceDefinitionWithVolumeDefinition,
    rgs map[string]lapi.ResourceGroup,
) {
    pvs, err := GetListPV(ctx, cl)
    if err != nil {
        fmt.Println(err)
        panic("[ReconcileParams] unable to get Persistent Volumes")
    }

    for _, pv := range pvs {
        if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == PVCSIDriver {
            sc := scs[pv.Spec.StorageClassName]

            RGName := rds[pv.Name].ResourceGroupName
            rg := rgs[RGName]
            getMissMatchedParams(sc, rg)
        }
    }
}

func removePrefixes(params map[string]string) map[string]string {
    tmp := make(map[string]string, len(params))
    for k, v := range params {
        tmpKey := strings.Split(k, "/")
        if len(tmpKey) > 0 {
            newKey := tmpKey[len(tmpKey)-1]
            tmp[newKey] = v
        }               
    }
    return tmp
}

func getMissMatchedParams(sc sv1.StorageClass, rg lapi.ResourceGroup) {
    scParams := removePrefixes(sc.Parameters)
    fmt.Println("!!!!!!!> " + scParams[placementCountSCKey])

    placeCount := strconv.Itoa(int(rg.SelectFilter.PlaceCount))
    fmt.Println("!!!!!!!> " + placeCount)
}
