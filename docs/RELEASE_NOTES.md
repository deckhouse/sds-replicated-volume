---
title: "Release Notes"
---

## v0.8.6

* Added release notes
* Hooks switched from python to golang
* Docs improved

## v0.8.5

* Added additional mountings for containerd v2 support

## v0.8.4

* Added information about the need for snapshot-controller for module operation

## v0.8.3

* Documentation fixes
* Added dependency on snapshot-controller

## v0.8.2

* Certificate update hook fixes
* Removal of obsolete migration hooks

## v0.8.1

* Documentation fixes (added instruction for expanding ReplicatedStoragePool to a new cluster node)

## v0.8.0

* Module refactoring
* Documentation fixes
* Fixes for volume snapshot support

## v0.7.4

* If topology allows, controller removes annotation for StorageClass that prohibits ordering RWX volumes

## v0.7.3

* Module refactoring
* Fixed podAntiAffinity for sds-replicated-volume-controller

## v0.7.2

* Changes in hooks for correct manual certificate update process
* Fixed D8NodeHighUnknownMemoryUsage alert grouping

## v0.7.1

* Added CSI patch for full support of topologies specified in ReplicatedStorageClass (could be ignored)
* Added D8NodeHighUnknownMemoryUsage alert for detecting DRBD memory leak cases (report issues to team storage)

## v0.6.0

* Updated DRBD to version v9.2.12, solving a number of problems (particularly improving DRBD diskless replica stability)

## v0.5.1

* Fixed alert for incorrect number of resource replicas
* Fixed schedule job for Linstor database backup

## v0.5.0

* Multiple minor fixes in templates, monitoring alerts and documentation
* Transition from linstor scheduler-extender to internal Deckhouse mechanisms (KubeSchedulerWebhookConfiguration)
* Migration of images to distroless
* Fixed and enhanced script for outputting drbd resources from node

## v0.4.3

* Technical release. Fixes and additions to evict.sh script for resource eviction from node, fixes in templates and documentation

## v0.4.1

* In evict.sh script for cleaning node from DRBD resources, AutoplaceTarget parameter is now taken into account, moved replicas will not be moved to nodes with AutoplaceTarget value equal to false

## v0.4.0

* Updated golang API libraries for sds-node-configurator v0.4.0 support
* Multiple fixes in controllers and documentation

## v0.3.7

* {'Skipping version, all fixes and changes from versions': '0.3.5 will be applied'}

## v0.3.5

* Multiple fixes and improvements in evict.sh and replicas_managers.sh (also, they are now automatically installed in /opt/deckhouse/sbin)
* DRBD now correctly builds on ALT Linux and with Linux kernel 6.5+
* Added anti-affinity rules for controller pods
* Multiple fixes in dashboard and alerts
* isDefault parameter removed; use standard k8s annotation instead
* Added liveness and readiness checks for controllers
* Backup switched to dedicated CR instead of using secrets in module namespace
* Prohibited creation of pools on ephemeral nodes
* Multiple documentation fixes
* CSI endpoint migrates from linstor.csi.linbit.com to replicated.csi.deckhouse.io

## v0.2.9

* Add DRBD ports range settings
* Fix path in liveness-satellite
* Actual typo lvmVolumeGroups and thinPoolName in examples
* Add a check for a Linstor node's AutoplaceTarget property
* Changed lvmvolumegroups to lvmVolumeGroups in russian docs
* Fix linstor satellite VPA

## v0.2.8

* Add check if /etc/modules file exists
* Add liveness probe for linstor-node
* Add age field
* {'Patch CSI to 98544cadb6d111d27a86a11ec07de91b99704b82': 'Prevent Node Reboots on Volume Deletion'}

## v0.1.11

* Fix enabled script, module will not be disabled if sds-node-configurator module disappears from cluster
