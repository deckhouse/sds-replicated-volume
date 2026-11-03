{{- if .Values.sdsReplicatedVolume.internal.newControlPlane }}
- name: kubernetes.replicated.volume.current_metrics
  rules:
    - alert: D8SdsReplicatedVolumeCurrentMetricsUnavailable
      expr: absent(sds_metrics_collector_up == 1)
      # Hands over with the alerts built on current metrics:
      #   - for equals their keep_firing_for: this alert fires as they resolve because the
      #     metrics are gone; still well above the gap of about a minute on a controller
      #     restart or a leader change;
      #   - keep_firing_for equals the for of D8ReplicatedVolumeNotReady: once the metrics
      #     return, this alert keeps firing while that one is pending again, so the page
      #     alert has no gap in either direction.
      # The ticket alerts are not covered on the way back (accepted): D8ReplicatedVolumeNotResilient
      # (for 30m) and D8ReplicatedVolumeLayoutDegraded (for 15m) fire again only after their own
      # for, and keep_firing_for does not hold a pending alert, so even a short gap in current
      # metrics restarts their wait.
      for: 5m
      keep_firing_for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_sds_replicated_volume_current_metrics: "D8SdsReplicatedVolumeCurrentMetrics,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_sds_replicated_volume_current_metrics: "D8SdsReplicatedVolumeCurrentMetrics,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: Current metrics of the sds-replicated-volume controller are unavailable
        description: |
          The leader replica of the sds-replicated-volume controller has not exported current metrics (the state of ReplicatedVolumes, replicas, attachments, storage pools and storage classes) for at least 5 minutes. Either the metrics collector fails (`sds_metrics_collector_up` is 0), or no leader collector is scraped at all: the controller is down, no replica holds the leader lease, or Prometheus cannot scrape the controller.

          While current metrics are unavailable, alerts built on them cannot fire, and the ones that are firing resolve, for example `D8ReplicatedVolumeNotReady`, `D8ReplicatedVolumeLayoutDegraded` and `D8ReplicatedStoragePoolNotReady`. The volumes themselves are not affected. Once current metrics return, this alert keeps firing for 5 more minutes, while the alerts built on them wait out their own `for` before firing again.

          The recommended course of action:

          1. Check that the controller is running and ready:

             ```shell
             kubectl -n d8-sds-replicated-volume get pods -l app=controller
             ```

          2. Check which replica holds the leader lease (the holder identity is `<pod name>_<uuid>`; empty if no replica holds it):

             ```shell
             kubectl -n d8-sds-replicated-volume get lease sds-replicated-volume-controller -o jsonpath='{.spec.holderIdentity}'
             ```

          3. Look for collector errors (`current metrics`) in the logs of the leader replica:

             ```shell
             LEADER=$(kubectl -n d8-sds-replicated-volume get lease sds-replicated-volume-controller -o jsonpath='{.spec.holderIdentity}' | cut -d_ -f1)
             kubectl -n d8-sds-replicated-volume logs "$LEADER" -c controller | grep 'current metrics'
             ```

          4. Check the scrape target of the controller on the Prometheus targets page (job `sds-replicated-volume-controller`).
{{- end }}
