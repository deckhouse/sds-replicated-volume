{{- if and (ne "dev" .Values.global.deckhouseVersion) (semverCompare "<1.64" .Values.global.deckhouseVersion) }}
- name: kubernetes.linstor.scheduler_state
  rules:
    - alert: D8LinstorSchedulerAdmissionPodIsNotReady
      expr: min by (pod) (kube_pod_status_ready{condition="true", namespace="d8-sds-replicated-volume", pod=~"linstor-scheduler-admission-.*"}) != 1
      for: 10m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_protocol_version: "1"
        plk_markup_format: "markdown"
        plk_labels_as_annotations: "pod"
        plk_create_group_if_not_exists__d8_linstor_scheduler_health: "D8LinstorSchedulerAdmissionHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_linstor_scheduler_health: "D8LinstorSchedulerHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: The linstor-scheduler-admission Pod is NOT Ready.
        description: |
          The recommended course of action:
          1. Retrieve details of the Deployment: `kubectl -n d8-sds-replicated-volume describe deploy linstor-scheduler-admission`
          2. View the status of the Pod and try to figure out why it is not running: `kubectl -n d8-sds-replicated-volume describe pod -l app=linstor-scheduler-admission`

    - alert: D8LinstorSchedulerAdmissionPodIsNotRunning
      expr: absent(kube_pod_status_phase{namespace="d8-sds-replicated-volume",phase="Running",pod=~"linstor-scheduler-admission-.*"})
      for: 2m
      labels:
        severity_level: "6"
        tier: cluster
      annotations:
        plk_protocol_version: "1"
        plk_markup_format: "markdown"
        plk_create_group_if_not_exists__d8_linstor_scheduler_health: "D8LinstorSchedulerAdmissionHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        plk_grouped_by__d8_linstor_scheduler_health: "D8LinstorSchedulerAdmissionHealth,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes"
        summary: The linstor-scheduler-admission Pod is NOT Running.
        description: |
          The recommended course of action:
          1. Retrieve details of the Deployment: `kubectl -n d8-sds-replicated-volume describe deploy linstor-scheduler-admission`
          2. View the status of the Pod and try to figure out why it is not running: `kubectl -n d8-sds-replicated-volume describe pod -l app=linstor-scheduler-admission`
{{- end }}
