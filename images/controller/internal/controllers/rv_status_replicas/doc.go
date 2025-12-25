/*
Copyright 2025 Flant JSC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package rvstatusreplicas implements the rv-status-replicas-controller, which
// maintains the rv.status.replicas field with current replica information.
//
// # What It Does
//
// When a replica is created, modified, or deleted, the controller updates
// rv.status.replicas to reflect the current state of all replicas belonging
// to that ReplicatedVolume.
//
// # Status Updates
//
// The controller maintains rv.status.replicas as an array containing information
// about each replica (name, node, type, deletion status).
//
// # Why It Exists
//
// This provides a convenient view of all replicas in the ReplicatedVolume status
// without needing to query ReplicatedVolumeReplica resources separately.
package rvstatusreplicas
