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

package linstorlogger

import (
	"strings"
	"testing"

	lapi "github.com/LINBIT/golinstor/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/deckhouse/sds-common-lib/logger"
)

func newTestLinstorLogger(t *testing.T, level logger.Verbosity) (lapi.LeveledLogger, *strings.Builder) {
	t.Helper()

	buf := &strings.Builder{}
	l, err := logger.NewLoggerToWriter(buf, level)
	require.NoError(t, err)
	return New(l), buf
}

// TestClientAcceptsTheAdapter is the regression this package exists for: lapi.Log
// type-switches its argument and refuses anything that is neither of its two
// logger interfaces, so handing over the module logger itself failed at startup
// with "Invalid logger type, expected Logger or LeveledLogger".
func TestClientAcceptsTheAdapter(t *testing.T) {
	log, _ := newTestLinstorLogger(t, logger.TraceLevel)

	_, err := lapi.NewClient(lapi.Log(log))
	require.NoError(t, err)
}

func TestAdapterFormatsMessages(t *testing.T) {
	log, buf := newTestLinstorLogger(t, logger.TraceLevel)

	log.Debugf("GET %s returned %d", "/v1/nodes", 200)

	out := buf.String()
	assert.Contains(t, out, "GET /v1/nodes returned 200")
	// The client's own records are namespaced, so they can be told apart from
	// the module's.
	assert.Contains(t, out, "linstor-client")
}

func TestAdapterMapsLevels(t *testing.T) {
	tests := []struct {
		name  string
		emit  func(l lapi.LeveledLogger)
		level string
	}{
		{name: "Debugf", emit: func(l lapi.LeveledLogger) { l.Debugf("m") }, level: `"level":"debug"`},
		{name: "Infof", emit: func(l lapi.LeveledLogger) { l.Infof("m") }, level: `"level":"info"`},
		{name: "Warnf", emit: func(l lapi.LeveledLogger) { l.Warnf("m") }, level: `"level":"warn"`},
		{name: "Errorf", emit: func(l lapi.LeveledLogger) { l.Errorf("m") }, level: `"level":"error"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, buf := newTestLinstorLogger(t, logger.TraceLevel)
			tt.emit(log)
			assert.Contains(t, buf.String(), tt.level)
		})
	}
}

// TestAdapterRespectsLevel guards the noisiest of the four: the client reports
// every request through Debugf.
func TestAdapterRespectsLevel(t *testing.T) {
	log, buf := newTestLinstorLogger(t, logger.ErrorLevel)

	log.Debugf("GET %s", "/v1/nodes")

	assert.Empty(t, buf.String())
}
