{{- if .Values.sdsReplicatedVolume.internal.newControlPlane }}
- name: kubernetes.replicated.volume.migration_labels
  rules:
    - alert: D8ReplicatedVolumeNoPersistentVolume
      expr: sds_rv_no_persistent_volume == 1
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_labels_as_annotations: "name"
        plk_create_group_if_not_exists__d8_replicated_volume_migration_labels: "D8ReplicatedVolumeMigrationLabels,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_replicated_volume_migration_labels: "D8ReplicatedVolumeMigrationLabels,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: ReplicatedVolume has no matching PersistentVolume
        description: |
          The ReplicatedVolume `{{ "{{" }} $labels.name {{ "}}" }}` is marked with the `sds-replicated-volume.deckhouse.io/no-persistent-volume` label, which means there is no corresponding PersistentVolume in the cluster. This ReplicatedVolume is orphaned and can be safely deleted.

          Verify that no workload depends on it, then remove the ReplicatedVolume:

          `kubectl delete replicatedvolume {{ "{{" }} $labels.name {{ "}}" }}`

    - alert: D8ReplicatedVolumeAutoConfigurationBlocked
      expr: sds_rv_auto_configuration_blocked == 1
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_labels_as_annotations: "name"
        plk_create_group_if_not_exists__d8_replicated_volume_migration_labels: "D8ReplicatedVolumeMigrationLabels,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_replicated_volume_migration_labels: "D8ReplicatedVolumeMigrationLabels,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: ReplicatedVolume cannot be switched to Auto configuration
        description: |
          The ReplicatedVolume `{{ "{{" }} $labels.name {{ "}}" }}` is marked with the `sds-replicated-volume.deckhouse.io/auto-configuration-blocked` label and stays in Manual mode. It cannot be switched to Auto configuration automatically and requires manual intervention.

          Possible reasons:
          - The referenced ReplicatedStorageClass does not exist (the StorageClass has no matching ReplicatedStorageClass).
          - The referenced ReplicatedStorageClass is in a terminal phase (`InsufficientNodes`, `InvalidConfiguration`, `PartiallyAligned`, or `Terminating`).
          - The referenced ReplicatedStorageClass is in `WaitingForStoragePool` and the conversion ReplicatedStoragePool could not be created.

          Inspect the ReplicatedStorageClass to identify the specific cause and fix it, then manually switch the ReplicatedVolume to Auto configuration with the required ReplicatedStorageClass and remove the label:

          `kubectl get replicatedstorageclass <rsc-name>`
          `kubectl patch replicatedvolume {{ "{{" }} $labels.name {{ "}}" }} --type=json -p '[{"op":"replace","path":"/spec/configurationMode","value":"Auto"},{"op":"add","path":"/spec/replicatedStorageClassName","value":"<rsc-name>"},{"op":"remove","path":"/spec/manualConfiguration"}]'`
          `kubectl label replicatedvolume {{ "{{" }} $labels.name {{ "}}" }} sds-replicated-volume.deckhouse.io/auto-configuration-blocked-`
{{- end }}
