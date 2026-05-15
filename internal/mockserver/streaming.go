package mockserver

import (
	"io"
	"time"
)

// FlushWriter is satisfied by http.ResponseWriter (which implements
// http.Flusher in HTTP/2 and chunked HTTP/1.1 modes). Tests use a fake
// implementation; the real handler wraps a *nethttp.ResponseWriter.
type FlushWriter interface {
	io.Writer
	Flush()
}

// streamResponse splits body into `chunks` roughly-equal slices, writes
// each one, calls Flush, then sleeps `delayMs` before the next. The
// final chunk is not followed by a sleep.
//
// chunks <= 1 emits the body verbatim with a single flush. When chunks
// does not divide evenly, the leading chunks each carry one extra byte
// so total length is preserved.
func streamResponse(w FlushWriter, body []byte, chunks, delayMs int) {
	if chunks <= 1 {
		w.Write(body)
		w.Flush()
		return
	}
	base := len(body) / chunks
	extra := len(body) - base*chunks
	pos := 0
	for i := 0; i < chunks; i++ {
		size := base
		if i < extra {
			size++
		}
		w.Write(body[pos : pos+size])
		w.Flush()
		pos += size
		if i < chunks-1 && delayMs > 0 {
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
		}
	}
}
