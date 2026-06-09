package gatewaylog

// DropPolicy controls what happens when the async log pipeline is full.
type DropPolicy int

const (
	// DropSilently discards the log entry and increments the drop counter.
	// Suitable for DEBUG/INFO under extreme load.
	DropSilently DropPolicy = iota
	// BlockOnFull causes the caller to block until space is available.
	// Use for WARN/ERROR where entries must not be lost.
	BlockOnFull
)

// LogWriter is the interface implemented by AsyncWriter.
// Logger.emit writes to this; nil during early startup (falls back to log.Print).
type LogWriter interface {
	Write(msg *LogBuf, policy DropPolicy)
	Stop()
	Drops() uint64
}
