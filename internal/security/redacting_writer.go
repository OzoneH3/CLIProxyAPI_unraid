package security

import (
	"io"
	"strings"
	"sync"
)

// RedactingWriter forwards complete, bounded log lines after suppressing lines
// that can contain credentials, callbacks, or authorization material.
type RedactingWriter struct {
	mu  sync.Mutex
	dst io.Writer
	buf string
}

func NewRedactingWriter(dst io.Writer) *RedactingWriter { return &RedactingWriter{dst: dst} }

func (w *RedactingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf += string(p)
	for {
		i := strings.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i]
		w.buf = w.buf[i+1:]
		if _, err := io.WriteString(w.dst, Redact(line)+"\n"); err != nil {
			return 0, err
		}
	}
	// Do not permit a hostile/no-newline child process to retain unbounded memory.
	if len(w.buf) > 4096 {
		if _, err := io.WriteString(w.dst, "[long log line omitted]\n"); err != nil {
			return 0, err
		}
		w.buf = ""
	}
	return len(p), nil
}
