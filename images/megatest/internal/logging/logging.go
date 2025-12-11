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

package logging

import (
	"fmt"
	"log/slog"
	"os"
	"time"
)

// Logger provides structured logging for megatest goroutines
type Logger struct {
	log       *slog.Logger
	rvName    string
	goroutine string
}

// NewLogger creates a new Logger with the given RV name and goroutine type
func NewLogger(rvName, goroutine string) *Logger {
	return &Logger{
		log:       slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})),
		rvName:    rvName,
		goroutine: goroutine,
	}
}

// SetupGlobalLogger initializes the global logger with JSON format
func SetupGlobalLogger(level slog.Level) {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
}

// ActionParams represents parameters for an action being logged
type ActionParams map[string]any

// ActionStarted logs the start of an action
func (l *Logger) ActionStarted(action string, params ActionParams) {
	attrs := []any{
		slog.String("goroutine", l.goroutine),
		slog.String("action", action),
		slog.String("phase", "started"),
		slog.Time("timestamp", time.Now()),
	}
	if l.rvName != "" {
		attrs = append(attrs, slog.String("rv", l.rvName))
	}

	for k, v := range params {
		attrs = append(attrs, slog.Any(k, v))
	}

	l.log.Info("action_started", attrs...)
}

// ActionCompleted logs the completion of an action
func (l *Logger) ActionCompleted(action string, params ActionParams, result string, duration time.Duration) {
	attrs := []any{
		slog.String("goroutine", l.goroutine),
		slog.String("action", action),
		slog.String("phase", "completed"),
		slog.String("result", result),
		slog.Duration("duration", duration),
		slog.Time("timestamp", time.Now()),
	}
	if l.rvName != "" {
		attrs = append(attrs, slog.String("rv", l.rvName))
	}

	for k, v := range params {
		attrs = append(attrs, slog.Any(k, v))
	}

	l.log.Info("action_completed", attrs...)
}

// ActionFailed logs the failure of an action
func (l *Logger) ActionFailed(action string, params ActionParams, err error, duration time.Duration) {
	attrs := []any{
		slog.String("goroutine", l.goroutine),
		slog.String("action", action),
		slog.String("phase", "failed"),
		slog.String("error", err.Error()),
		slog.Duration("duration", duration),
		slog.Time("timestamp", time.Now()),
	}
	if l.rvName != "" {
		attrs = append(attrs, slog.String("rv", l.rvName))
	}

	for k, v := range params {
		attrs = append(attrs, slog.Any(k, v))
	}

	l.log.Error("action_failed", attrs...)
}

// StateChanged logs a state change observation (for watchers)
func (l *Logger) StateChanged(expectedState, observedState string, details ActionParams) {
	attrs := []any{
		slog.String("goroutine", l.goroutine),
		slog.String("expected_state", expectedState),
		slog.String("observed_state", observedState),
		slog.Time("timestamp", time.Now()),
	}
	if l.rvName != "" {
		attrs = append(attrs, slog.String("rv", l.rvName))
	}

	for k, v := range details {
		attrs = append(attrs, slog.Any(k, v))
	}

	l.log.Warn("state_changed", attrs...)
}

// Info logs an informational message
func (l *Logger) Info(msg string, args ...any) {
	attrs := []any{
		slog.String("goroutine", l.goroutine),
	}
	if l.rvName != "" {
		attrs = append(attrs, slog.String("rv", l.rvName))
	}
	attrs = append(attrs, args...)
	l.log.Info(msg, attrs...)
}

// Error logs an error message
func (l *Logger) Error(msg string, err error, args ...any) {
	attrs := []any{
		slog.String("goroutine", l.goroutine),
		slog.String("error", err.Error()),
	}
	if l.rvName != "" {
		attrs = append(attrs, slog.String("rv", l.rvName))
	}
	attrs = append(attrs, args...)
	l.log.Error(msg, attrs...)
}

// WithRV returns a new Logger for the specified RV name
func (l *Logger) WithRV(rvName string) *Logger {
	return &Logger{
		log:       l.log,
		rvName:    rvName,
		goroutine: l.goroutine,
	}
}

// WithGoroutine returns a new Logger for the specified goroutine type
func (l *Logger) WithGoroutine(goroutine string) *Logger {
	return &Logger{
		log:       l.log,
		rvName:    l.rvName,
		goroutine: goroutine,
	}
}

// GlobalLogger returns a logger without RV context
func GlobalLogger(goroutine string) *Logger {
	return NewLogger("", goroutine)
}

// GenerateInstanceID generates a unique instance ID for a goroutine
func GenerateInstanceID(goroutine string, index int) string {
	return fmt.Sprintf("%s-%d-%d", goroutine, index, time.Now().UnixNano())
}
