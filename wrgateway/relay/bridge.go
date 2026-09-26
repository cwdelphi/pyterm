package relay

import (
	"io"
	"sync"
)

// Bridge transparently bridges a WebSocket connection and a DataChannel.
type Bridge struct {
	wsReader io.ReadCloser  // reads from WebSocket
	wsWriter io.WriteCloser // writes to WebSocket
	dcReader io.ReadCloser  // reads from DataChannel
	dcWriter io.WriteCloser // writes to DataChannel
	done     chan struct{}
	errOnce  sync.Once
	err      error
}

func NewBridge(wsConn io.ReadWriteCloser, dcReader io.ReadCloser, dcWriter io.WriteCloser) *Bridge {
	return &Bridge{
		wsReader: wsConn,
		wsWriter: wsConn,
		dcReader: dcReader,
		dcWriter: dcWriter,
		done:     make(chan struct{}),
	}
}

// Start begins bidirectional bridging.
func (b *Bridge) Start() {
	go b.wsToDC()
	go b.dcToWS()
}

// Wait blocks until one direction closes.
func (b *Bridge) Wait() error {
	<-b.done
	return b.err
}

func (b *Bridge) wsToDC() {
	defer b.signalDone()
	buf := make([]byte, 65536)
	for {
		n, err := b.wsReader.Read(buf)
		if n > 0 {
			if _, werr := b.dcWriter.Write(buf[:n]); werr != nil {
				b.setError(werr)
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				b.setError(err)
			}
			return
		}
	}
}

func (b *Bridge) dcToWS() {
	defer b.signalDone()
	buf := make([]byte, 65536)
	for {
		n, err := b.dcReader.Read(buf)
		if n > 0 {
			if _, werr := b.wsWriter.Write(buf[:n]); werr != nil {
				b.setError(werr)
				return
			}
		}
		if err != nil {
			if err != io.EOF {
				b.setError(err)
			}
			return
		}
	}
}

func (b *Bridge) signalDone() {
	b.errOnce.Do(func() { close(b.done) })
}

func (b *Bridge) setError(err error) {
	b.err = err
}

func (b *Bridge) Close() {
	b.wsReader.Close()
	b.wsWriter.Close()
	b.dcReader.Close()
	b.dcWriter.Close()
}
