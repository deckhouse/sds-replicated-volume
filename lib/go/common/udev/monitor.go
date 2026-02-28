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

import (
	"context"
	"fmt"

	"github.com/pilebones/go-udev/netlink"
)

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

// MonitorBlockEvents connects to the kernel's udev netlink socket and monitors
// block subsystem events. Matched events are sent to eventCh.
//
// The function blocks until ctx is cancelled (returns nil) or a monitor error
// occurs (returns error). The caller is responsible for retrying on error.
func MonitorBlockEvents(ctx context.Context, eventCh chan<- BlockEvent) error {
	conn := new(netlink.UEventConn)
	if err := conn.Connect(netlink.UdevEvent); err != nil {
		return fmt.Errorf("connecting to udev netlink: %w", err)
	}

	rawCh := make(chan netlink.UEvent, 64)
	errCh := make(chan error, 1)
	matcher := &netlink.RuleDefinitions{
		Rules: []netlink.RuleDefinition{
			{Env: map[string]string{"SUBSYSTEM": "block"}},
		},
	}
	quit := conn.Monitor(rawCh, errCh, matcher)

	defer func() {
		select {
		case quit <- struct{}{}:
		default:
		}
		conn.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-rawCh:
			select {
			case eventCh <- BlockEvent{
				Action: string(ev.Action),
				KObj:   ev.KObj,
				Env:    ev.Env,
			}:
			case <-ctx.Done():
				return nil
			}
		case err := <-errCh:
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("udev monitor: %w", err)
		}
	}
}
