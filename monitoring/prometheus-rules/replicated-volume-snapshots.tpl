{{- if .Values.sdsReplicatedVolume.internal.newControlPlane }}
- name: kubernetes.replicated.volume.snapshots
  rules:
    - alert: D8ReplicatedVolumeSnapshotFailed
      expr: sds_rvs_failed == 1
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_labels_as_annotations: "name"
        plk_create_group_if_not_exists__d8_replicated_volume_snapshots: "D8ReplicatedVolumeSnapshots,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_replicated_volume_snapshots: "D8ReplicatedVolumeSnapshots,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: ReplicatedVolume snapshot failed
        description: |
          The ReplicatedVolumeSnapshot `{{ "{{" }} $labels.name {{ "}}" }}` is in the `Failed` phase, so the snapshot cannot be used to restore data. The failure is permanent: the snapshot will not retry on its own.

          Read the cause from the resource status:

          `kubectl get replicatedvolumesnapshot {{ "{{" }} $labels.name {{ "}}" }} -o jsonpath='{.status.message}{"\n"}'`

          Common causes:
          - The source volume is not thin-provisioned. Snapshots require a ReplicatedStoragePool of type `LVMThin`.
          - A thin pool ran out of space on one of the nodes holding a replica.
          - The DRBD kernel module does not support the cluster-wide admin lock (`DRBD_FF_ADMIN_LOCK`), which the snapshot needs to freeze the volume consistently.

          Fix the underlying cause, then delete the failed snapshot and create it again:

          `kubectl delete replicatedvolumesnapshot {{ "{{" }} $labels.name {{ "}}" }}`

    - alert: D8ReplicatedVolumeSnapshotStuck
      expr: sds_rvs_unfinished_age_seconds > 1800
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_labels_as_annotations: "name,phase"
        plk_create_group_if_not_exists__d8_replicated_volume_snapshots: "D8ReplicatedVolumeSnapshots,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_replicated_volume_snapshots: "D8ReplicatedVolumeSnapshots,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: ReplicatedVolume snapshot has not completed for over 30 minutes
        description: |
          The ReplicatedVolumeSnapshot `{{ "{{" }} $labels.name {{ "}}" }}` has been in the `{{ "{{" }} $labels.phase {{ "}}" }}` phase for more than 30 minutes without reaching `Ready` or `Failed`.

          Taking a snapshot involves snapshotting every diskful replica and then synchronizing the replicas that lag behind, so a large volume with several replicas legitimately takes minutes. Half an hour without a result means the process is not progressing.

          Inspect the snapshot and its per-replica children:

          `kubectl get replicatedvolumesnapshot {{ "{{" }} $labels.name {{ "}}" }} -o yaml`
          `kubectl get replicatedvolumereplicasnapshot --field-selector spec.replicatedVolumeSnapshotName={{ "{{" }} $labels.name {{ "}}" }}`

          Where to look, by phase:
          - `InProgress`: a per-replica snapshot is not being created. Check the ReplicatedVolumeReplicaSnapshot objects and the LVMLogicalVolumeSnapshot objects behind them, and check free space in the thin pools.
          - `Synchronizing`: replicas are not catching up. Check that all replicas of the source volume are connected and healthy.

          A snapshot stuck this long usually also blocks the corresponding VolumeSnapshot request. Deleting the ReplicatedVolumeSnapshot cancels the attempt and cleans up the temporary resources.
{{- end }}
