package log

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

//
type Logger struct {
	raw *log.Logger
}

//
type Entry struct {
	Time     string `json:"time"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

//
var defaultLogger = New(log.New(os.Stdout, "", 0))

//
func New(raw *log.Logger) *Logger {
	return &Logger{raw: raw}
}

//
func Error(msg string) {
	defaultLogger.Error(msg)
}

//
func Errorf(format string, a ...interface{}) {
	defaultLogger.Errorf(format, a...)
}

//
func Warn(msg string) {
	defaultLogger.Warn(msg)
}

//
func Warnf(format string, a ...interface{}) {
	defaultLogger.Warnf(format, a...)
}

//
func Info(msg string) {
	defaultLogger.Info(msg)
}

//
func Infof(format string, a ...interface{}) {
	defaultLogger.Infof(format, a...)
}

//
func Debug(msg string) {
	defaultLogger.Debug(msg)
}

//
func Debugf(format string, a ...interface{}) {
	defaultLogger.Debugf(format, a...)
}

//
func (l *Logger) Error(msg string) {
	l.Write("ERROR", msg)
}

//
func (l *Logger) Errorf(format string, a ...interface{}) {
	l.Write("ERROR", fmt.Sprintf(format, a...))
}

//
func (l *Logger) Warn(msg string) {
	l.Write("WARNING", msg)
}

//
func (l *Logger) Warnf(format string, a ...interface{}) {
	l.Write("WARNING", fmt.Sprintf(format, a...))
}

//
func (l *Logger) Info(msg string) {
	l.Write("INFO", msg)
}

//
func (l *Logger) Infof(format string, a ...interface{}) {
	l.Write("INFO", fmt.Sprintf(format, a...))
}

//
func (l *Logger) Debug(msg string) {
	l.Write("DEBUG", msg)
}

//
func (l *Logger) Debugf(format string, a ...interface{}) {
	l.Write("DEBUG", fmt.Sprintf(format, a...))
}

//
func (l *Logger) Write(severity, msg string) {
	now := time.Now().Format(time.RFC3339Nano)
	entry := Entry{
		Time:     now,
		Severity: severity,
		Message:  msg,
	}
	b, _ := json.Marshal(entry)
	l.raw.Print(string(b))
}
