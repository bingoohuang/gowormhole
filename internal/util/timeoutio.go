package util

import (
	"errors"
	"io"
	"time"
)

// https://go.dev/play/p/CX5ojtTOm4
// https://github.com/onsi/gomega/blob/master/gbytes/io_wrappers.go
// https://github.com/hshimamoto/go-iorelay/blob/master/timeout.go

// ErrTimeout is returned by TimeoutCloser, TimeoutReader, and TimeoutWriter when the underlying Closer/Reader/Writer does not return within the specified timeout
var ErrTimeout = errors.New("timeout occurred")

// TimeoutCloser returns an io.Closer that wraps the passed-in io.Closer.  If the underlying Closer fails to close within the allotted timeout ErrTimeout is returned.
func TimeoutCloser(c io.Closer, timeout time.Duration) io.Closer {
	return timeoutReaderWriterCloser{c: c, d: timeout}
}

// TimeoutReader returns an io.Reader that wraps the passed-in io.Reader.  If the underlying Reader fails to read within the allotted timeout ErrTimeout is returned.
func TimeoutReader(r io.Reader, timeout time.Duration) io.Reader {
	return timeoutReaderWriterCloser{r: r, d: timeout}
}

// TimeoutWriter returns an io.Writer that wraps the passed-in io.Writer.  If the underlying Writer fails to write within the allotted timeout ErrTimeout is returned.
func TimeoutWriter(w io.Writer, timeout time.Duration) io.Writer {
	return timeoutReaderWriterCloser{w: w, d: timeout}
}

// TimeoutReadWriter returns an io.ReadWriter that wraps the passed-in io.ReadWriter.  If the underlying ReadWriter fails to read/write within the allotted timeout ErrTimeout is returned.
func TimeoutReadWriter(rw io.ReadWriter, timeout time.Duration) io.ReadWriter {
	return timeoutReaderWriterCloser{w: rw, r: rw, d: timeout}
}

type timeoutReaderWriterCloser struct {
	c io.Closer
	w io.Writer
	r io.Reader
	d time.Duration
}

func (t timeoutReaderWriterCloser) Close() (err error) {
	done := make(chan struct{})

	go func() {
		err = t.c.Close()
		close(done)
	}()

	timer := time.NewTimer(t.d)
	defer timer.Stop()

	select {
	case <-done:
		return err
	case <-timer.C:
		return ErrTimeout
	}
}

func (t timeoutReaderWriterCloser) Read(p []byte) (n int, err error) {
	done := make(chan struct{})
	go func() {
		n, err = t.r.Read(p)
		close(done)
	}()

	timer := time.NewTimer(t.d)
	defer timer.Stop()

	select {
	case <-done:
		return n, err
	case <-timer.C:
		return 0, ErrTimeout
	}
}

func (t timeoutReaderWriterCloser) Write(p []byte) (n int, err error) {
	done := make(chan struct{})
	go func() {
		n, err = t.w.Write(p)
		close(done)
	}()

	timer := time.NewTimer(t.d)
	defer timer.Stop()

	select {
	case <-done:
		return n, err
	case <-timer.C:
		return 0, ErrTimeout
	}
}
