// Package internal provides shared utilities used across myruntime.
// The logger writes structured, levelled messages to stderr.
package internal

import (
	"fmt"
	"io"
	"os"
	"time"
)

// Level represents a log severity level.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[Level]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError:  "ERROR",
}

// Logger is a simple levelled logger.
type Logger struct {
	level  Level
	output io.Writer
}

// defaultLogger is the package-level default instance.
var defaultLogger = &Logger{
	level:  LevelInfo,
	output: os.Stderr,
}

// SetLevel changes the minimum log level for the default logger.
func SetLevel(l Level) { defaultLogger.level = l }

// Debug logs a debug-level message.
func Debug(format string, args ...any) { defaultLogger.log(LevelDebug, format, args...) }

// Info logs an info-level message.
func Info(format string, args ...any) { defaultLogger.log(LevelInfo, format, args...) }

// Warn logs a warning-level message.
func Warn(format string, args ...any) { defaultLogger.log(LevelWarn, format, args...) }

// Error logs an error-level message.
func Error(format string, args ...any) { defaultLogger.log(LevelError, format, args...) }

// Fatal logs an error-level message then exits with status 1.
func Fatal(format string, args ...any) {
	defaultLogger.log(LevelError, format, args...)
	os.Exit(1)
}

func (l *Logger) log(level Level, format string, args ...any) {
	if level < l.level {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	name := levelNames[level]
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.output, "[%s] %s  %s\n", ts, name, msg)
}
