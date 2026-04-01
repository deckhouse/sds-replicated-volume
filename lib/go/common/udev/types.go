/*
Copyright 2026 Flant JSC

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

// Package udev provides block device event monitoring via Linux netlink sockets.
//
// TODO: Reuse this package in sds-node-configurator to replace its inline
// go-udev/netlink usage in images/agent/internal/scanner/scanner.go.
package udev

// BlockEvent represents a udev event for a block device.
type BlockEvent struct {
	// Action is the event action: "add", "remove", "change", etc.
	Action string
	// KObj is the kernel object path, e.g. "/devices/virtual/block/dm-0".
	KObj string
	// Env contains all environment properties from the event
	// (e.g. SUBSYSTEM, DEVNAME, DM_NAME, etc.).
	Env map[string]string
}
