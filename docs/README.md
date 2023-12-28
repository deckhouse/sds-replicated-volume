---
title: "The SDS-DRDB module"
description: "The SDS-DRBD module: General Concepts and Principles."
---
{% alert level="warning" %}
The module is guaranteed to work only in the following cases:
- if stock kernels shipped with the [supported distributions](../../supported_versions.html#linux) are used;
- if a 10 Gbps network is used.

As for any other configurations, the module may work, but its smooth operation is not guaranteed.
{% endalert %}

This module manages replicated block storage based on `LINSTOR` and the `DRBD` kernel module and allows to create a `Storage Pool` in `Linstor` and a `Storage Class` in `Kubernetes` by creating [Kubernetes custom resources](resources).
`LINSTOR` is an orchestrator that acts as an abstraction layer. It:
- automates volume creation using well-known and proven technologies such as `LVM` and `ZFS`;
- configures volume replication using `DRBD`.

The SDS-DRBD module makes it easy to use LINSTOR-based storage in your cluster. Once you enable the SDS-DRBD module in your Deckhouse configuration, your cluster will be automatically configured to use `LINSTOR`. All that remains is to create a `Storage pool` (link).

Two modes are supported: LVM and LVMThin.

Each mode has its advantages and disadvantages. Read [FAQ](faq) to learn more and compare them.

Note that the module's [Kubernetes custom resources](resources) are Cluster-scoped.
