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

package drbdutils

import (
	"bufio"
	"context"
	"fmt"
	"iter"
	"strings"
	"time"
)

var Events2Args = []string{"events2", "--timestamps", "--statistics"}

type Events2Result interface {
	_isEvents2Result()
}

type Event struct {
	Timestamp time.Time
	Kind      EventKind
	Object    EventObject
	State     map[string]string
}

var _ Events2Result = &Event{}

func (*Event) _isEvents2Result() {}

// EventKind represents the verb in an events2 line.
type EventKind string

const (
	EventKindExists   EventKind = "exists"
	EventKindCreate   EventKind = "create"
	EventKindDestroy  EventKind = "destroy"
	EventKindChange   EventKind = "change"
	EventKindCall     EventKind = "call"
	EventKindResponse EventKind = "response"
	EventKindRename   EventKind = "rename"
)

// parseEventKind returns the typed kind or empty string for unrecognized values.
func parseEventKind(s string) (EventKind, bool) {
	switch EventKind(s) {
	case EventKindExists, EventKindCreate, EventKindDestroy, EventKindChange,
		EventKindCall, EventKindResponse, EventKindRename:
		return EventKind(s), true
	default:
		return "", false
	}
}

// EventObject represents the object type in an events2 line.
type EventObject string

const (
	EventObjectResource   EventObject = "resource"
	EventObjectDevice     EventObject = "device"
	EventObjectConnection EventObject = "connection"
	EventObjectPeerDevice EventObject = "peer-device"
	EventObjectPath       EventObject = "path"
	EventObjectHelper     EventObject = "helper"
	EventObjectDumpDone   EventObject = "-"
)

// parseEventObject returns the typed object or empty string for unrecognized values.
func parseEventObject(s string) (EventObject, bool) {
	switch EventObject(s) {
	case EventObjectResource, EventObjectDevice, EventObjectConnection,
		EventObjectPeerDevice, EventObjectPath, EventObjectHelper,
		EventObjectDumpDone:
		return EventObject(s), true
	default:
		return "", false
	}
}

type UnparsedEvent struct {
	RawEventLine string
	Err          error
}

var _ Events2Result = &UnparsedEvent{}

func (u UnparsedEvent) _isEvents2Result() {}

func ExecuteEvents2(
	ctx context.Context,
	resultErr *error,
) iter.Seq[Events2Result] {
	if resultErr == nil {
		panic("resultErr is required to be non-nil pointer")
	}

	return func(yield func(Events2Result) bool) {
		cmd := ExecCommandContext(
			ctx,
			DRBDSetupCommand,
			Events2Args...,
		)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			*resultErr = fmt.Errorf("getting stdout pipe: %w", err)
			return
		}

		if err := cmd.Start(); err != nil {
			*resultErr = fmt.Errorf("starting command: %w", err)
			return
		}

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if !yield(parseLine(line)) {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			*resultErr = fmt.Errorf("error reading command output: %w", err)
			return
		}

		if err := cmd.Wait(); err != nil {
			*resultErr = fmt.Errorf("command finished with error: %w", err)
			return
		}
	}
}

// parseLine parses a single line of drbdsetup events2 output
func parseLine(line string) Events2Result {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return &UnparsedEvent{
			RawEventLine: line,
			Err:          fmt.Errorf("line has fewer than 3 fields"),
		}
	}

	// ISO 8601 timestamp
	tsStr := fields[0]
	ts, err := time.Parse(time.RFC3339Nano, tsStr)
	if err != nil {
		return &UnparsedEvent{
			RawEventLine: line,
			Err:          fmt.Errorf("invalid timestamp %q: %v", tsStr, err),
		}
	}

	kind, kindOk := parseEventKind(fields[1])
	if !kindOk {
		return &UnparsedEvent{
			RawEventLine: line,
			Err:          fmt.Errorf("unrecognized event kind %q", fields[1]),
		}
	}

	object, objectOk := parseEventObject(fields[2])
	if !objectOk {
		return &UnparsedEvent{
			RawEventLine: line,
			Err:          fmt.Errorf("unrecognized event object %q", fields[2]),
		}
	}

	state := make(map[string]string)
	for _, kv := range fields[3:] {
		parts := strings.SplitN(kv, ":", 2)
		if len(parts) != 2 {
			return &UnparsedEvent{
				RawEventLine: line,
				Err:          fmt.Errorf("invalid key-value pair: %s", kv),
			}
		}
		state[parts[0]] = parts[1]
	}

	return &Event{
		Timestamp: ts,
		Kind:      kind,
		Object:    object,
		State:     state,
	}
}
