package drbdsetup

import (
	"bufio"
	"context"
	"fmt"
	"iter"
	"os/exec"
	"strings"
	"time"
)

type Events2Result interface {
	_isEvents2Result()
}

type Event struct {
	Timestamp time.Time
	// "exists" for an existing object;
	//
	// "create", "destroy", and "change" if an object
	// is created, destroyed, or changed;
	//
	// "call" or "response" if an event handler
	// is called or it returns;
	//
	// or "rename" when the name of an object is changed
	Kind string
	// "resource", "device", "connection", "peer-device", "path", "helper", or
	// a dash ("-") to indicate that the current state has been dumped
	// completely
	Object string
	// Identify the object and describe the state that the object is in
	State map[string]string
}

var _ Events2Result = &Event{}

func (*Event) _isEvents2Result() {}

type UnparsedEvent struct {
	RawEventLine string
	Err          error
}

var _ Events2Result = &UnparsedEvent{}

func (u UnparsedEvent) _isEvents2Result() {}

type Events2 struct {
	cmd *exec.Cmd
}

func NewEvents2(ctx context.Context) *Events2 {
	return &Events2{
		cmd: exec.CommandContext(
			ctx,
			DRBDSetupCommand,
			DRBDSetupEvents2Args...,
		),
	}
}

func (e *Events2) Run(resultErr *error) iter.Seq[Events2Result] {
	if resultErr == nil {
		panic("resultErr is required to be non-nil pointer")
	}
	return func(yield func(Events2Result) bool) {
		stderr, err := e.cmd.StderrPipe()
		if err != nil {
			*resultErr = fmt.Errorf("getting stderr pipe: %w", err)
			return
		}

		if err := e.cmd.Start(); err != nil {
			*resultErr = fmt.Errorf("starting command: %w", err)
			return
		}

		scanner := bufio.NewScanner(stderr)
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

		if err := e.cmd.Wait(); err != nil {
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

	kind := fields[1]
	object := fields[2]

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
