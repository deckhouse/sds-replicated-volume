- name: kubernetes.certs.expiring_soon_check
  rules:
    - alert: D8LinstorCertificateExpiringIn30d
      expr: max by (namespace, secret) (kube_secret_labels{label_storage_deckhouse_io_sds_replicated_volume_cert_expire_in_30d="true"}) > 0
      for: 5m
      labels:
        severity_level: "4"
        tier: cluster
      annotations:
        plk_markup_format: "markdown"
        plk_protocol_version: "1"
        plk_create_group_if_not_exists__cluster_has_expiring_certificates: ClusterCertificatesExpiringIn30d,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes
        plk_grouped_by__cluster_has_expiring_certificates: ClusterCertificatesExpiringIn30d,tier=~tier,prometheus=deckhouse,kubernetes=~kubernetes
        summary: Secret {{`{{ $labels.secret }}`}} holds a certificate expiring within 30 days
        description: |
          The `sds-replicated-volume` module keeps its own certificates up to date: a certificate is
          renewed 45 days before it expires. This alert means the renewal has been failing for two
          weeks, so the certificate in the `{{`{{ $labels.secret }}`}}` secret is about to expire.

          Steps to troubleshoot:

          1. Check the certificate itself:

             ```shell
             kubectl -n d8-sds-replicated-volume get secret {{`{{ $labels.secret }}`}} -o jsonpath='{.data.tls\.crt}' \
               | base64 -d | openssl x509 -noout -subject -dates
             ```

          2. Look for errors reported by the certificate hooks of the module:

             ```shell
             kubectl -n d8-system logs deploy/deckhouse | grep -i 'sds-replicated-volume.*cert'
             ```

          3. Make sure the Deckhouse queue is not stuck, since the certificates are renewed before
             the module is deployed:

             ```shell
             kubectl -n d8-system exec -ti deploy/deckhouse -- deckhouse-controller queue main
             ```

          If the renewal cannot be unblocked, follow "FAQ: How to renew the module certificates
          manually?" in the module documentation.
