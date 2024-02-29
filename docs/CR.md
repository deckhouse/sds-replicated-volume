---
title: "The sds-replicated-volume module: Custom Resources"
description: "The sds-replicated-volume module Custom Resources: ReplicatedStoragePool and ReplicatedStorageClass."
---

{{< alert level="warning" >}}
The module is guaranteed to work only in the following cases:
- if stock kernels shipped with the [supported distributions](https://deckhouse.io/documentation/v1/supported_versions.html#linux) are used;
- if a 10 Gbps network is used.

As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}
