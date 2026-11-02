{{- if not .Values.sdsReplicatedVolume.internal.newControlPlane }}
- name: kubernetes.drbd.device_state
  rules:
    - alert: D8LinstorVolumeIsNotHealthy
      expr: max by (node, resource) ((linstor_volume_state != 1) != 4)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: LINSTOR volume is not healthy
        description: |
          LINSTOR volume {{ "{{" }} $labels.resource {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} is not healthy

          Please, contact tech support for assistance.

    - alert: D8DrbdDeviceHasNoQuorum
      expr: max by (node, name) (drbd_device_quorum == 0)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD device has no quorum
        description: |
          DRBD device {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} has no quorum.

          Please, contact tech support for assistance.

    - alert: D8DrbdResourceIOSuspended
      expr: max by (node, name) (drbd_resource_suspended == 1) unless on (node, name) max by (node, name) (drbd_device_quorum == 0)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD resource has its I/O suspended
        description: |
          DRBD resource {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} has had its I/O suspended for more than 5 minutes. Reads and writes to the volume hang instead of failing, so the workload using it is stuck.

          The alert does not fire while the resource has no quorum: that case is reported by `D8DrbdDeviceHasNoQuorum`. The other reasons DRBD suspends I/O are:

          - `no-data`: no replica with up-to-date data is reachable;
          - `fencing`: DRBD waits for a fencing handler;
          - `user`: I/O was suspended by hand with `drbdsetup suspend-io`.

          The `suspended:` field of `drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --verbose` on the node shows which of them it is.

          Please, contact tech support for assistance.

    - alert: D8DrbdDeviceIsUnintentionalDiskless
      expr: max by (node, name) (drbd_device_unintentionaldiskless == 1)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD device is unintentional diskless
        description: |
          DRBD device {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} unintentionally switched to diskless mode

          Please, contact tech support for assistance.

    - alert: D8DrbdPeerDeviceIsOutOfSync
      expr: max by (node, conn_name, name) (drbd_peerdevice_outofsync_bytes > 0)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD device has out-of-sync data
        description: |
          DRBD device {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} has out-of-sync data with {{ "{{" }} $labels.conn_name {{ "}}" }}

          The recommended course of action:
          1. Login into node with the problem:

             ```
             kubectl -n d8-sds-replicated-volume exec -ti $(kubectl -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=linstor-node -o name) -c linstor-satellite -- bash
             ```

          2. Check the LINSTOR peer node state:

             ```
             linstor node list -n {{ "{{" }} $labels.conn_name {{ "}}" }}
             ```

          3. Check the LINSTOR resource states:

             ```
             linstor resource list -r {{ "{{" }} $labels.name {{ "}}" }}
             ```

          4. View the status of the DRBD device on the node and try to figure out why it has out-of-sync data with {{ "{{" }} $labels.conn_name {{ "}}" }}:

             ```shell
             drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --statistics
             dmesg --color=always | grep 'drbd {{ "{{" }} $labels.name {{ "}}" }}'
             ```

          5. Consider reconnect resource with the peer node:

             ```shell
             drbdadm disconnect {{ "{{" }} $labels.name {{ "}}" }}:{{ "{{" }} $labels.conn_name {{ "}}" }}
             drbdadm connect {{ "{{" }} $labels.name {{ "}}" }}:{{ "{{" }} $labels.conn_name {{ "}}" }}
             ```

          6. Check if problem has solved, device should have no out-of-sync data:

             ```
             drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --statistics
             ```

    - alert: D8DrbdDeviceIsNotConnected
      expr: max by (node, conn_name, name) (drbd_connection_state{drbd_connection_state!="UpToDate", drbd_connection_state!="Connected"} == 1)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD device is not connected
        description: |
          DRBD device {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} is not connected with {{ "{{" }} $labels.conn_name {{ "}}" }}


          Please, contact tech support for assistance.
{{- else }}
- name: kubernetes.drbd.device_state
  rules:
    - alert: D8DrbdDeviceHasNoQuorum
      expr: max by (node, name) (drbd_device_quorum == 0)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD device has no quorum
        description: |
          DRBD device {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} has no quorum, its I/O is suspended.

          A frequent cause is a diskful replica that is stuck resyncing near completion, so not enough replicas are `UpToDate` to form a quorum.

          The recommended course of action:
          1. Inspect the DRBD device state. The `agent` container is distroless, so run the binary directly (no shell). `--as=system:sudouser` is required to exec into the Pod:

             ```shell
             d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --verbose --statistics
             ```

          2. If a replica is `Inconsistent` and its resync is stalled (`out-of-sync` and `received` are not moving), reconnect that peer connection to restart the resync. Find the peer node-id of the up-to-date source (`peer-N node-id:X`) and, on the stalled node, run:

             ```shell
             d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName=<stalled-node> -l app=agent -o name) -c agent -- drbdsetup disconnect {{ "{{" }} $labels.name {{ "}}" }} <peer-node-id>
             ```

             The connection re-establishes automatically. Once a second replica reaches `UpToDate`, the quorum is restored and I/O resumes.

          3. For the detailed procedure and edge cases, see the module FAQ section "Diagnosing DRBD out-of-sync data or lost quorum (new control plane)". If the quorum is not restored, contact tech support.

    - alert: D8DrbdResourceIOSuspended
      expr: max by (node, name) (drbd_resource_suspended == 1) unless on (node, name) max by (node, name) (drbd_device_quorum == 0)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD resource has its I/O suspended
        description: |
          DRBD resource {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} has had its I/O suspended for more than 5 minutes. Reads and writes to the volume hang instead of failing, so the workload using it is stuck.

          The alert does not fire while the resource has no quorum: that case is reported by `D8DrbdDeviceHasNoQuorum`. The other reasons DRBD suspends I/O are:

          - `no-data`: no replica with up-to-date data is reachable;
          - `fencing`: DRBD waits for a fencing handler;
          - `user`: I/O was suspended by hand with `drbdsetup suspend-io`.

          Inspect the resource; the `suspended:` field shows the reason (the `agent` container is distroless, run the binary directly; `--as=system:sudouser` is required to exec into the Pod):

          ```shell
          d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --verbose --statistics
          ```

          - For `no-data`, bring back a node holding an up-to-date replica of the resource: I/O resumes once it connects. If no up-to-date replica can come back, contact tech support before doing anything else with the resource.
          - For `user`, resume I/O with `drbdsetup resume-io {{ "{{" }} $labels.name {{ "}}" }}` (same `exec` as above) once the reason it was suspended for is gone.

          If I/O is still suspended, contact tech support.

    - alert: D8DrbdDeviceIsUnintentionalDiskless
      expr: max by (node, name) (drbd_device_unintentionaldiskless == 1)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD device is unintentional diskless
        description: |
          DRBD device {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} unintentionally switched to diskless mode.

          Inspect the DRBD device state (the `agent` container is distroless, run the binary directly; `--as=system:sudouser` is required to exec into the Pod):

          ```shell
          d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --verbose --statistics
          ```

          Please, contact tech support for assistance.

    - alert: D8DrbdPeerDeviceIsOutOfSync
      expr: max by (node, conn_name, name) (drbd_peerdevice_outofsync_bytes > 0)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD device has out-of-sync data
        description: |
          DRBD device {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} has out-of-sync data with {{ "{{" }} $labels.conn_name {{ "}}" }}

          The recommended course of action (the `agent` container is distroless, run the binary directly, no shell; `--as=system:sudouser` is required to exec into the Pod):
          1. View the status of the DRBD device. Note the peer node-id of {{ "{{" }} $labels.conn_name {{ "}}" }} shown as `peer-N node-id:X`:

             ```shell
             d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --verbose --statistics
             ```

          2. Reconnect the peer connection to trigger a bitmap-based resync of the out-of-sync extents. Replace `<peer-node-id>` with the id from step 1 (the connection re-establishes automatically):

             ```shell
             d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup disconnect {{ "{{" }} $labels.name {{ "}}" }} <peer-node-id>
             ```

          3. Check if the problem is solved, the device should have no out-of-sync data:

             ```shell
             d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --statistics
             ```

          4. If a small residual out-of-sync persists after the reconnect, see the module FAQ section "Diagnosing DRBD out-of-sync data or lost quorum (new control plane)" for online verification, full re-mirror, and the phantom-counter case.

    - alert: D8DrbdDeviceIsNotConnected
      expr: max by (node, conn_name, name) (drbd_connection_state{drbd_connection_state!="UpToDate", drbd_connection_state!="Connected"} == 1)
      for: 5m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_drbd_device_health: "D8DrbdDeviceHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: DRBD device is not connected
        description: |
          DRBD device {{ "{{" }} $labels.name {{ "}}" }} on node {{ "{{" }} $labels.node {{ "}}" }} is not connected with {{ "{{" }} $labels.conn_name {{ "}}" }}

          The recommended course of action (the `agent` container is distroless, run the binary directly; `--as=system:sudouser` is required to exec into the Pod):
          1. Inspect the connection state. A connection stuck in `Connecting`/`WFBitMap` is a half-open handshake:

             ```shell
             d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --verbose --statistics
             ```

          2. Reconnect the stuck peer connection. Find the peer node-id of {{ "{{" }} $labels.conn_name {{ "}}" }} (`peer-N node-id:X`) and run (the connection re-establishes automatically):

             ```shell
             d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup disconnect {{ "{{" }} $labels.name {{ "}}" }} <peer-node-id>
             ```

          3. If the connection does not recover, verify the peers are reachable over the DRBD ports and contact tech support.
{{- end }}
