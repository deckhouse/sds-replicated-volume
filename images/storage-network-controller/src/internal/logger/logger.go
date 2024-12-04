/*
Copyright 2023 Flant JSC

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

package logger

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2/textlogger"
)

const (
	ErrorLevel   Verbosity = "0"
	WarningLevel Verbosity = "1"
	InfoLevel    Verbosity = "2"
	DebugLevel   Verbosity = "3"
	TraceLevel   Verbosity = "4"
)

const (
	warnLvl = iota + 1
	infoLvl
	debugLvl
	traceLvl
)

type (
	Verbosity string
	// struct just to be unique in context
	loggerKey struct{}
)

type Logger struct {
	log logr.Logger
}

func NewLogger(level Verbosity) (*Logger, error) {
	v, err := strconv.Atoi(string(level))
	if err != nil {
		return nil, err
	}

	log := textlogger.NewLogger(textlogger.NewConfig(textlogger.Verbosity(v))).WithCallDepth(1)

	return &Logger{log: log}, nil
}

func WithLogger(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

func FromContext(ctx context.Context) *Logger {
	if logger, ok := ctx.Value(loggerKey{}).(*Logger); ok {
		return logger
	}

	// WARNING! There will be a panic here if the logger was not initialized in the context
	return nil
}

func (l Logger) GetLogger() logr.Logger {
	return l.log
}

func (l Logger) Error(err error, message string, keysAndValues ...interface{}) {
	l.log.Error(err, fmt.Sprintf("ERROR %s", message), keysAndValues...)
}

func (l Logger) Warning(message string, keysAndValues ...interface{}) {
	l.log.V(warnLvl).Info(fmt.Sprintf("WARNING %s", message), keysAndValues...)
}

func (l Logger) Info(message string, keysAndValues ...interface{}) {
	l.log.V(infoLvl).Info(fmt.Sprintf("INFO %s", message), keysAndValues...)
}

func (l Logger) Debug(message string, keysAndValues ...interface{}) {
	l.log.V(debugLvl).Info(fmt.Sprintf("DEBUG %s", message), keysAndValues...)
}

func (l Logger) Trace(message string, keysAndValues ...interface{}) {
	l.log.V(traceLvl).Info(fmt.Sprintf("TRACE %s", message), keysAndValues...)
}

func (l *Logger) Printf(format string, args ...interface{}) {
	l.log.V(traceLvl).Info("%s", fmt.Sprintf(format, args...))
}
