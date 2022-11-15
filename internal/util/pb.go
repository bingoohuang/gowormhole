package util

import (
	"io"

	"github.com/bingoohuang/pb"
)

func CreateProgressBar(pb ProgressBar, progress bool) ProgressBar {
	if pb != nil {
		return pb
	}

	if progress {
		return &CliProgressBar{}
	}

	return &NoopProgressBar{}
}

// NewProxyReader creates a wrapper for given reader, but with progress handle
// Takes io.Reader or io.ReadCloser
// Also, it automatically switches progress bar to handle units as bytes
func NewProxyReader(r io.Reader, pb ProgressBar) *Reader {
	return &Reader{Reader: r, ProgressBar: pb}
}

// NewProxyWriter creates a wrapper for given writer, but with progress handle
// Takes io.Writer or io.WriteCloser
// Also, it automatically switches progress bar to handle units as bytes
func NewProxyWriter(r io.Writer, pb ProgressBar) *Writer {
	return &Writer{Writer: r, ProgressBar: pb}
}

type ProgressBar interface {
	Start(filename string, n uint64)
	Add(n uint64)
	Finish()
}

type NoopProgressBar struct{}

func (n *NoopProgressBar) Start(string, uint64) {}
func (n *NoopProgressBar) Add(uint64)           {}
func (n *NoopProgressBar) Finish()              {}

type CliProgressBar struct {
	bar *pb.ProgressBar
}

func (c *CliProgressBar) Start(filename string, n uint64) { c.bar = pb.Full.Start64(int64(n)) }
func (c *CliProgressBar) Add(n uint64)                    { c.bar.Add64(int64(n)) }
func (c *CliProgressBar) Finish()                         { c.bar.Finish() }

// Reader it's a wrapper for given reader, but with progress handle
type Reader struct {
	io.Reader
	ProgressBar
}

// Read reads bytes from wrapped reader and add amount of bytes to progress bar
func (r *Reader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	r.ProgressBar.Add(uint64(n))
	if err == io.EOF {
		r.ProgressBar.Finish()
	}
	return
}

// Close the wrapped reader when it implements io.Closer
func (r *Reader) Close() (err error) {
	r.ProgressBar.Finish()
	if closer, ok := r.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return
}

// Writer it's a wrapper for given writer, but with progress handle
type Writer struct {
	io.Writer
	ProgressBar
}

// Write writes bytes to wrapped writer and add amount of bytes to progress bar
func (r *Writer) Write(p []byte) (n int, err error) {
	n, err = r.Writer.Write(p)
	r.ProgressBar.Add(uint64(n))
	return
}

// Close the wrapped reader when it implements io.Closer
func (r *Writer) Close() (err error) {
	r.ProgressBar.Finish()
	if closer, ok := r.Writer.(io.Closer); ok {
		return closer.Close()
	}
	return
}
