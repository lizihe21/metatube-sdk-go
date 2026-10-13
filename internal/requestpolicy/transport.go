package requestpolicy

import (
	"io"
	"net/http"
	"sync"
)

// Transport wraps the actual HTTP transport. This ensures each retry and
// redirect consumes the shared request budget rather than just the entrypoint.
type Transport struct {
	Base     http.RoundTripper
	governor *Governor
}

func Wrap(base http.RoundTripper, governor *Governor) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &Transport{Base: base, governor: governor}
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	attempts := t.governor.config.MaxAttempts
	for attempt := 0; attempt < attempts; attempt++ {
		release, err := t.governor.acquire(req.Context())
		if err != nil {
			return nil, err
		}
		resp, err := t.Base.RoundTrip(req)
		if resp != nil {
			t.governor.ObserveResponse(resp.StatusCode, resp.Header)
		}
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			release()
			return nil, err
		}
		if resp == nil {
			release()
			return nil, io.ErrUnexpectedEOF
		}
		retry := attempt+1 < attempts && (req.Method == http.MethodGet || req.Method == http.MethodHead) && req.Body == nil &&
			(resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusBadGateway ||
				resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout)
		if retry {
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			release()
			continue
		}
		if resp.Body == nil {
			release()
		} else {
			resp.Body = &releaseBody{ReadCloser: resp.Body, release: release}
		}
		return resp, nil
	}
	panic("unreachable: policy retry budget must be positive")
}

type releaseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *releaseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.once.Do(b.release)
	}
	return n, err
}
func (b *releaseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}
