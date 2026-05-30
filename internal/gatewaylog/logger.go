package gatewaylog

import (
	"log"
	"strconv"
	"strings"
	"sync/atomic"
)

// Level represents a logging level.
type Level int32

const (
	DEBUG Level = iota // 0
	INFO               // 1
	WARN               // 2
	ERROR              // 3
)

// String returns the string representation of the level.
func (l Level) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "INFO"
	}
}

// ParseLevel parses a string into a Level (case-insensitive; unknown defaults to INFO).
func ParseLevel(s string) Level {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARN":
		return WARN
	case "ERROR":
		return ERROR
	default:
		return INFO
	}
}

// Field is a key-value pair attached to a log message.
type Field struct {
	Key   string
	Value string
}

// F creates a string field.
func F(key, value string) Field {
	return Field{Key: key, Value: value}
}

// Fint creates an integer field.
func Fint(key string, value int64) Field {
	return Field{Key: key, Value: strconv.FormatInt(value, 10)}
}

// Ffloat creates a float field.
func Ffloat(key string, value float64) Field {
	return Field{Key: key, Value: strconv.FormatFloat(value, 'f', -1, 64)}
}

// Logger emits leveled log lines in logfmt format.
// All exported methods are safe for concurrent use.
type Logger struct {
	level atomic.Int32 // stores Level value
}

// New creates a Logger at the given level.
func New(level Level) *Logger {
	l := &Logger{}
	l.level.Store(int32(level))
	return l
}

// SetLevel changes the level atomically (no restart needed).
func (l *Logger) SetLevel(level Level) {
	l.level.Store(int32(level))
}

// Level returns the current level.
func (l *Logger) Level() Level {
	return Level(l.level.Load())
}

// Debug emits a DEBUG message only when effective level <= DEBUG.
func (l *Logger) Debug(msg string, fields ...Field) {
	if l.Level() > DEBUG {
		return
	}
	l.emit("DEBUG", msg, fields)
}

// Info emits an INFO message only when effective level <= INFO.
func (l *Logger) Info(msg string, fields ...Field) {
	if l.Level() > INFO {
		return
	}
	l.emit("INFO", msg, fields)
}

// Warn emits a WARN message only when effective level <= WARN.
func (l *Logger) Warn(msg string, fields ...Field) {
	if l.Level() > WARN {
		return
	}
	l.emit("WARN", msg, fields)
}

// Error emits an ERROR message always (ERROR is highest level).
func (l *Logger) Error(msg string, fields ...Field) {
	l.emit("ERROR", msg, fields)
}

// ShouldLog returns true if a message at the given level should be emitted.
func (l *Logger) ShouldLog(level Level) bool {
	return level >= Level(l.level.Load())
}

// log emits a structured log line at the given level without a level gate check.
// Callers are responsible for calling ShouldLog first.
func (l *Logger) log(level Level, msg string, fields ...Field) {
	l.emit(level.String(), msg, fields)
}

// emit formats and logs the message with fields in logfmt format.
func (l *Logger) emit(levelStr, msg string, fields []Field) {
	var buf strings.Builder
	buf.WriteString("level=")
	buf.WriteString(levelStr)
	buf.WriteString(" msg=")

	// msg value is quoted if it contains spaces, otherwise unquoted
	if strings.ContainsAny(msg, " ") {
		buf.WriteString(`"`)
		buf.WriteString(msg)
		buf.WriteString(`"`)
	} else {
		buf.WriteString(msg)
	}

	// Append fields in order
	for _, f := range fields {
		buf.WriteString(" ")
		buf.WriteString(f.Key)
		buf.WriteString("=")

		// Field values containing spaces or = are quoted
		if strings.ContainsAny(f.Value, " =") {
			buf.WriteString(`"`)
			buf.WriteString(f.Value)
			buf.WriteString(`"`)
		} else {
			buf.WriteString(f.Value)
		}
	}

	log.Print(buf.String())
}

// Default is the process-wide logger, starts at INFO.
var Default = New(INFO)
