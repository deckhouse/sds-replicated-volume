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

          5. Before reconnecting, check every replica of the resource, not only this node. In the output of step 3, every diskful replica must be `UpToDate` and every connection `Ok`. On every node of the resource (enter its `linstor-satellite` container as in step 1), `drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --verbose` must show no `Outdated` or `Inconsistent` disk, no resync in progress and no `quorum:no`.

             If any of this does not hold, do not reconnect anything: the reconnect takes this connection down for a moment, and with another replica already out the resource can lose quorum and suspend I/O. Wait for the resync to finish, or deal with the failed replica first.

          6. Reconnect only this connection:

             ```shell
             drbdadm disconnect {{ "{{" }} $labels.name {{ "}}" }}:{{ "{{" }} $labels.conn_name {{ "}}" }}
             drbdadm connect {{ "{{" }} $labels.name {{ "}}" }}:{{ "{{" }} $labels.conn_name {{ "}}" }}
             ```

          7. Check that the device has no out-of-sync data with {{ "{{" }} $labels.conn_name {{ "}}" }}:

             ```
             drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --statistics
             ```

          8. Repeat step 5 before reconnecting any other connection of the resource: if the reconnect started a resync, wait until it finishes. When out-of-sync data is reported on several connections of the resource (one alert per connection), reconnect them one at a time and check all replicas after each. If a replica stays `Outdated` or `Inconsistent`, or a node loses quorum, stop and contact tech support.

          A diskless replica of the resource can show a diskful peer as `peer-disk:Consistent` instead of `UpToDate`. DRBD does this when the current data generation of the peer differs from the one the diskless replica works with; here it is a symptom of the same out-of-sync data, not a separate failure.

          For details, see the module FAQ section "How do I clear out-of-sync data on a DRBD device?".

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

          2. Before reconnecting, check every replica of the volume, not only this node. `<volume>` is the PersistentVolume name: the DRBD resource {{ "{{" }} $labels.name {{ "}}" }} is `sdsrv-<volume>-<N>`. Every diskful replica must be `UpToDate` and every replica must have quorum:

             ```shell
             d8 k --as=system:sudouser get drbdr -l sds-replicated-volume.deckhouse.io/replicated-volume=<volume>
             ```

             On every node of the list, step 1 (with the node and the DRBD resource `sdsrv-<DRBDResource name>` of that node) must show no `Outdated` or `Inconsistent` disk, no resync in progress and no `quorum:no`. If any of this does not hold, do not reconnect anything: the reconnect takes this connection down for a moment, and with another replica already out the volume can lose quorum and suspend I/O. Wait for the resync to finish, or deal with the failed replica first.

          3. Reconnect only this connection. DRBD compares the current generation UUIDs of both ends again, and only a Primary that writes while it is not connected to the peer creates a new one. If the Primary is on neither end of this connection (for example, it is a diskless replica on another node), or it did not write while the connection was down, DRBD clears the out-of-sync bits of this connection without transferring or comparing data. If the Primary at one end wrote meanwhile, the out-of-sync blocks are resynced from the bitmap together with the new writes, and the other end stays `Inconsistent` until the resync ends: for a residual covering the whole volume that is a full resync. The reconnect does not check the data. Replace `<peer-node-id>` with the id from step 1 (the connection re-establishes automatically):

             ```shell
             d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup disconnect {{ "{{" }} $labels.name {{ "}}" }} <peer-node-id>
             ```

          4. Check that the device has no out-of-sync data with {{ "{{" }} $labels.conn_name {{ "}}" }}:

             ```shell
             d8 k --as=system:sudouser -n d8-sds-replicated-volume exec -t $(d8 k -n d8-sds-replicated-volume get pod --field-selector=spec.nodeName={{ "{{" }} $labels.node {{ "}}" }} -l app=agent -o name) -c agent -- drbdsetup status {{ "{{" }} $labels.name {{ "}}" }} --statistics
             ```

          5. Repeat step 2 before reconnecting any other connection of the volume: if the reconnect started a resync, wait until it finishes. When out-of-sync data is reported on several connections of the volume (one alert per connection), reconnect them one at a time and check all replicas after each. If a replica stays `Outdated` or `Inconsistent`, or a replica loses quorum, stop and do not reconnect anything else; if the quorum is lost, see the module FAQ section "Diagnosing DRBD out-of-sync data or lost quorum (new control plane)" or contact tech support.

          6. If a small residual out-of-sync persists after the reconnect, see the module FAQ section "Diagnosing DRBD out-of-sync data or lost quorum (new control plane)" for online verification, full re-mirror, and the phantom-counter case.

          A diskless replica of the volume can show a diskful peer as `peer-disk:Consistent` instead of `UpToDate`. DRBD does this when the current data generation of the peer differs from the one the diskless replica works with; here it is a symptom of the same out-of-sync data, not a separate failure.

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
