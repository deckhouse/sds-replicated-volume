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

// Package linstorlogger adapts the module logger to the logger interface of the
// LINSTOR REST client.
package linstorlogger

import (
	"fmt"

	lapi "github.com/LINBIT/golinstor/client"

	"github.com/deckhouse/sds-common-lib/logger"
)

// adapter satisfies golinstor's LeveledLogger on top of logger.Logger.
//
// lapi.Log type-switches its argument over the two interfaces the client knows,
// and rejects anything else at construction time, so the logger cannot be handed
// over directly: it carries slog-style methods, not printf-style ones. The
// interface is printf-style, so messages coming from the client arrive
// pre-formatted.
type adapter struct {
	log logger.Logger
}

var _ lapi.LeveledLogger = adapter{}

// New adapts log for use as the LINSTOR client's logger.
func New(log logger.Logger) lapi.LeveledLogger {
	return adapter{log: log.Named("linstor-client")}
}

func (a adapter) Errorf(format string, args ...interface{}) {
	a.log.Error(fmt.Sprintf(format, args...))
}

func (a adapter) Warnf(format string, args ...interface{}) {
	a.log.Warn(fmt.Sprintf(format, args...))
}

func (a adapter) Infof(format string, args ...interface{}) {
	a.log.Info(fmt.Sprintf(format, args...))
}

// Debugf logs at debug level. The client reports every request and every
// non-2xx response through it, so it is the noisiest of the four by far.
func (a adapter) Debugf(format string, args ...interface{}) {
	a.log.Debug(fmt.Sprintf(format, args...))
}
