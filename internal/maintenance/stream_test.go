package maintenance

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type cancelAfterRead struct {
	cancel context.CancelFunc
	bytes.Reader
}

func (r *cancelAfterRead) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.cancel()
	return n, e
}
func TestArchiveCopyObservesCancellationWithinOneFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &cancelAfterRead{cancel, *bytes.NewReader(make([]byte, 8<<20))}
	n, err := io.Copy(io.Discard, cancellingReader{ctx, src})
	if !errors.Is(err, context.Canceled) || n == 0 || n > 64<<10 {
		t.Fatal(n, err)
	}
}
