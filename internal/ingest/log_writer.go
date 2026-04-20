package ingest

import (
	"io"
	"os"
	"time"
)

// NewLogWriter returns an io.Writer that forwards each Write call as a KindLog
// event into the pipeline. Intended use: log.SetOutput(ingest.NewLogWriter(p))
//
// If p is nil, or if no sink is wired to KindLog, writes fall back to os.Stderr
// so startup log lines (emitted before pipeline init) are never lost.
func NewLogWriter(p *Pipeline) io.Writer {
	if p == nil {
		return os.Stderr
	}
	return &logWriter{p: p}
}

type logWriter struct{ p *Pipeline }

func (w *logWriter) Write(b []byte) (int, error) {
	numSinks := w.p.NumSinksForKind(KindLog)
	if numSinks == 0 {
		// KindLog not wired to any sink — write to stderr so nothing is lost.
		_, _ = os.Stderr.Write(b)
		return len(b), nil
	}

	e := Event{
		Kind:        KindLog,
		TimestampNs: time.Now().UnixNano(),
	}
	// Copy bytes: log package may reuse the buffer before the sink worker flushes.
	cp := make([]byte, len(b))
	copy(cp, b)
	e.SetPayload(cp, numSinks)
	w.p.Emit(e)
	return len(b), nil
}
