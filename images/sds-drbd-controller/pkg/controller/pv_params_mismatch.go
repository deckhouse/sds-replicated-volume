/*
Copyright 2023 Flant JSC

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

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	lapi "github.com/LINBIT/golinstor/client"
	core "k8s.io/api/core/v1"
	v1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	PvParamsMismatchControllerName = "pv-params-mismatch-controller"
	PVCSIDriver                    = "linstor.csi.linbit.com"
)

var (
	MatchParams = []string{
		"auto-quorum", "on-no-data-accessible", "on-suspended-primary-outdated",
		"rr-conflict", "placementCount", "storagePool",
	}
)

type rdParams struct {
	Props  map[string]string
	RGname string
}

type rgParams struct {
	Props       map[string]string
	PlaceCount  int32
	StoragePool string
}

func NewAlertController(
	mgr manager.Manager,
	lc *lapi.Client,
	interval int,
) (controller.Controller, error) {
	cl := mgr.GetClient()
	log := mgr.GetLogger()
	ctx := context.Background()

	c, err := controller.New(PvParamsMismatchControllerName, mgr, controller.Options{
		Reconciler: reconcile.Func(func(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
			return reconcile.Result{}, nil
		}),
	})

	if err != nil {
		log.Error(err, fmt.Sprintf(`[NewAlertController] unable to create controller: "%s"`, PvParamsMismatchControllerName))
		return nil, err
	}

	go func() {
		for {
			time.Sleep(time.Second * time.Duration(interval*60))

			scToParams := make(map[string]map[string]string)
			storageClasses, err := GetListStorageClass(ctx, cl)
			if err != nil {
				log.Error(err, "[GetListStorageClass]")
			}
			for _, sc := range storageClasses {
				scToParams[sc.Name] = sc.Parameters
			}

			rdsObj := make(map[string]rdParams)
			rds, _ := lc.ResourceDefinitions.GetAll(ctx, lapi.RDGetAllRequest{})
			for _, rd := range rds {
				rdsObj[rd.Name] = rdParams{
					Props:  rd.Props,
					RGname: rd.ResourceGroupName,
				}
			}

			rgsObj := make(map[string]rgParams)
			rgs, _ := lc.ResourceGroups.GetAll(ctx)
			for _, rg := range rgs {
				rgsObj[rg.Name] = rgParams{
					Props:       rg.Props,
					PlaceCount:  rg.SelectFilter.PlaceCount,
					StoragePool: rg.SelectFilter.StoragePool,
				}
			}

			pvs, err := GetListPV(ctx, cl)
			if err != nil {
				log.Error(err, "[GetListPV]")
			}

			kubeParams := make(map[string]string)
			linstorParams := make(map[string]string)
			var RGName string

			for _, pv := range pvs {
				if pv.Spec.CSI != nil && len(pv.Spec.CSI.Driver) != 0 && pv.Spec.CSI.Driver == PVCSIDriver {
					kubeParams = scToParams[pv.Spec.StorageClassName]

					RGName = rdsObj[pv.Name].RGname
					linstorParams = rgsObj[RGName].Props
					linstorParams["placeCount"] = string(rgsObj[RGName].PlaceCount)
					linstorParams["storagePool"] = rgsObj[RGName].StoragePool

					if missMatchParams(cutParams(kubeParams), cutParams(linstorParams), MatchParams) {
						log.Info(fmt.Sprintf("PV %s missmatch", pv.Name))
						if pv.Labels == nil {
							pv.Labels = make(map[string]string)
						}
						pv.Labels["storage.deckhouse.io/linstor-settings-mismatch"] = "true"
						err = UpdatePV(ctx, cl, &pv)
						if err != nil {
							log.Error(err, "[UpdatePV]")
							return
						}
					}
				}
			}
		}
	}()
	return c, err
}

func GetListStorageClass(ctx context.Context, cl client.Client) ([]v1.StorageClass, error) {
	listStorageClasses := &v1.StorageClassList{
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

func GetListPV(ctx context.Context, cl client.Client) ([]core.PersistentVolume, error) {
	PersistentVolumeList := &core.PersistentVolumeList{}
	err := cl.List(ctx, PersistentVolumeList)
	if err != nil {
		return nil, err
	}
	return PersistentVolumeList.Items, nil
}

func GetPV(ctx context.Context, cl client.Client, namespace, name string) (*core.PersistentVolume, error) {
	obj := &core.PersistentVolume{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, obj)
	if err != nil {
		return nil, err
	}
	return obj, err
}

func UpdatePV(ctx context.Context, cl client.Client, pv *core.PersistentVolume) error {
	err := cl.Update(ctx, pv)
	if err != nil {
		return err
	}
	return nil
}

func cutParams(params map[string]string) map[string]string {
	tmp := make(map[string]string)
	for k, v := range params {
		tmpKey := strings.Split(k, "/")
		newKey := tmpKey[len(tmpKey)-1]
		tmp[newKey] = v
	}
	return tmp
}

func missMatchParams(paramList1, paramList2 map[string]string, matchParams []string) bool {
	for _, parameter := range matchParams {
		if paramList1[parameter] != paramList2[parameter] {
			return true
		}
	}
	return false
}
