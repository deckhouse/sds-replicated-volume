package validators

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-logr/logr"
	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"net/http"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"webhooks/funcs"
	"webhooks/linstor"
)

type mc struct {
	Spec     mcSpec   `json:"spec"`
	Metadata Metadata `json:"metadata"`
}

type mcSpec struct {
	Settings mcSettings `json:"settings"`
}

type mcSettings struct {
	DrbdPortRange drbdPortRange `json:"drbdPortRange"`
}

type drbdPortRange struct {
	MinPort *int `json:"minPort" `
	MaxPort *int `json:"maxPort"`
}

const (
	propContainerName = "d2ef39f4afb6fbe91ab4c9048301dc4826d84ed221a5916e92fa62fdb99deef0"
	propKey           = "TcpPortAutoRange"
	propInstance      = "/CTRLCFG"
)

func MCValidate(w http.ResponseWriter, r *http.Request) {
	logf.SetLogger(logr.Logger{})

	arReview := v1beta1.AdmissionReview{}
	if err := json.NewDecoder(r.Body).Decode(&arReview); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	} else if arReview.Request == nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	raw := arReview.Request.Object.Raw

	arReview.Response = &v1beta1.AdmissionResponse{
		UID:     arReview.Request.UID,
		Allowed: true,
	}

	mcJson := mc{}
	klog.Infof("Retrieving MC object %v", json.Unmarshal(raw, &mcJson))

	if mcJson.Spec.Settings.DrbdPortRange.MinPort == nil && mcJson.Spec.Settings.DrbdPortRange.MaxPort == nil {
		cl, _ := funcs.NewKubeClient()
		ctx := context.Background()
		objs := linstor.PropsContainersList{}
		klog.Infof("Retrieveng list of prop containers %v", cl.List(ctx, &objs, &client.ListOptions{}))

		klog.Info("Deleting port range in crd")

		for _, item := range objs.Items {
			if item.Name == propContainerName {
				klog.Infof("Item with ports range deleted %v", cl.Delete(ctx, &item, &client.DeleteOptions{}))
			}
		}
	} else if (mcJson.Spec.Settings.DrbdPortRange.MinPort != nil && mcJson.Spec.Settings.DrbdPortRange.MaxPort == nil) ||
		(mcJson.Spec.Settings.DrbdPortRange.MinPort == nil && mcJson.Spec.Settings.DrbdPortRange.MaxPort != nil) {
		arReview.Response.Allowed = false
		arReview.Response.Result = &metav1.Status{
			Message: fmt.Sprintf("Both or none minPort, maxPort values must present in ModuleConfig."),
		}
		klog.Infof("Both or none minPort, maxPort values must present in ModuleConfig.")
	} else if mcJson.Spec.Settings.DrbdPortRange.MinPort != nil && mcJson.Spec.Settings.DrbdPortRange.MaxPort != nil {
		var minPort = *mcJson.Spec.Settings.DrbdPortRange.MinPort
		var maxPort = *mcJson.Spec.Settings.DrbdPortRange.MaxPort
		if minPort < 7000 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("DRBD port range start (%d) must be more then 7000", minPort),
			}
			klog.Infof("DRBD port range start (%d) must be more then 7000", minPort)
		} else if maxPort > 49151 {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("DRBD port range end (%d) must be less then 49152", maxPort),
			}
			klog.Infof("DRBD port range end (%d) must be less then 49152", maxPort)
		} else if !(minPort < maxPort) {
			arReview.Response.Allowed = false
			arReview.Response.Result = &metav1.Status{
				Message: fmt.Sprintf("DRBD port range start (%d) must be less then end (%d)", minPort, maxPort),
			}
			klog.Infof("DRBD port range start (%d) must be less then end (%d)", minPort, maxPort)
		} else {
			cl, _ := funcs.NewKubeClient()
			ctx := context.Background()
			objs := linstor.PropsContainersList{}
			klog.Infof("Retrieveng list of prop containers %v", cl.List(ctx, &objs, &client.ListOptions{}))
			propContainerExists := false

			klog.Info("Updating port range in crd")

			for _, item := range objs.Items {
				if item.Name == propContainerName {
					propContainerExists = true
					if item.Spec.PropValue != fmt.Sprintf("%d-%d", minPort, maxPort) {
						item.Spec.PropValue = fmt.Sprintf("%d-%d", minPort, maxPort)
						klog.Infof("Item updated to %d-%d (%v)", minPort, maxPort, cl.Update(ctx, &item, &client.UpdateOptions{}))
					}
				}
			}

			if propContainerExists == false {
				klog.Infof("Item created with range %d-%d (%v)", minPort, maxPort, cl.Create(ctx, &linstor.PropsContainers{
					ObjectMeta: metav1.ObjectMeta{
						Name: propContainerName,
					},
					Spec: linstor.PropsContainersSpec{
						PropKey:       propKey,
						PropValue:     fmt.Sprintf("%d-%d", minPort, maxPort),
						PropsInstance: propInstance,
					},
				}))
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	klog.Infof("Returning answer %v", json.NewEncoder(w).Encode(&arReview))
}
