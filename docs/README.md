---
title: "The sds-drbd module"
description: "The sds-drbd module: General Concepts and Principles."
moduleStatus: deprecated
---

{% alert level="danger" %}
Use the [sds-replicated-volume](https://deckhouse.ru/modules/sds-replicated-volume/stable/) module instead.
{% endalert %}

{{< alert level="warning" >}}
The module is guaranteed to work only in the following cases:
- if stock kernels shipped with the [supported distributions](https://deckhouse.io/documentation/v1/supported_versions.html#linux) are used;
- if a 10 Gbps network is used.

As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{{< /alert >}}

This module manages replicated block storage based on `DRBD`. Currently, `LINSTOR` is used as a control-plane. The module allows you to create a `Storage Pool` in `LINSTOR` as well as a `StorageClass` in `Kubernetes` by creating [Kubernetes custom resources](./cr.html). 
To create a `Storage Pool`, you will need the `LVMVolumeGroup` configured on the cluster nodes. The `LVM` configuration is done by the [sds-node-configurator](../../sds-node-configurator/) module.
> **Caution!** Before enabling the `sds-drbd` module, you must enable the `sds-node-configurator` module.
> 
> **Caution!** The user is not allowed to configure the `LINSTOR` backend directly.
>
> **Caution!** Data synchronization during volume replication is carried out in synchronous mode only, asynchronous mode is not supported.

After you enable the `sds-drbd` module in the Deckhouse configuration, your cluster will be automatically set to use the `LINSTOR` backend. You will only have to create [storage pools and StorageClasses](./usage.html#configuring-the-linstor-backend).

> **Caution!** The user is not allowed to create a `StorageClass` for the drbd.csi.storage.deckhouse.io CSI driver.

Two modes are supported: LVM and LVMThin.
Each mode has its advantages and disadvantages. Read [FAQ](./faq.html#what-is-difference-between-lvm-and-lvmthin) to learn more and compare them.
