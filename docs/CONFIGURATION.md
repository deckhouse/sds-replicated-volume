---
title: "The sds-drbd module: configuration"
force_searchable: true
description: The sds-drbd Deckhouse module's configuration.
---

{% alert level="danger" %}
This version of the module is deprecated and is no longer supported. Use the [sds-replicated-volume](https://deckhouse.ru/modules/sds-replicated-volume/stable/) module instead.
{% endalert %}

{{< alert level="warning" >}}
The module is guaranteed to work only in the following cases:
- if stock kernels shipped with the [supported distributions](https://deckhouse.io/documentation/v1/supported_versions.html#linux) are used;
- if a 10 Gbps network is used.

As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}
