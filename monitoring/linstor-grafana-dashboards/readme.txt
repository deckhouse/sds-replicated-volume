Grafana dashboards for the LINSTOR control plane.

They are rendered by templates/monitoring.yaml only when the new control plane
is disabled (sdsReplicatedVolume.internal.newControlPlane is false). Dashboards
for the new control plane live in monitoring/grafana-dashboards/.

The layout below this directory follows monitoring/grafana-dashboards/: the
first subdirectory is the Grafana folder, so storage/linstor_drbd.json keeps
the resource name d8-sds-replicated-volume-storage-linstor-drbd and the
"Storage" folder.

Caveat: dmt lint validates dashboard JSON (deprecated panel types, datasource
UIDs, the ds_prometheus variable, etc.) only under monitoring/grafana-dashboards/.
Dashboards in this directory are NOT checked by dmt, so review changes to them
manually.
