package internal

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type Backend struct {
	URL *url.URL
	up  bool
	mu  sync.RWMutex
}

func NewBackend(u *url.URL) *Backend {
	return &Backend{URL: u, up: true}
}
func (b *Backend) IsUp() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.up
}
func (b *Backend) SetUp(v bool) {
	b.mu.Lock()
	b.up = v
	b.mu.Unlock()
}

type LB struct {
	backends []*Backend
	n        int
	counter  uint64
}

var (
	ErrNoBackends        = errors.New("no backends configured")
	ErrNoHealthyBackends = errors.New("no healthy backends")
)

func NewLB(backends []*Backend) *LB {
	return &LB{backends: backends, n: len(backends)}
}

func (l *LB) Pick() (*Backend, error) {
	if l.n == 0 {
		return nil, ErrNoBackends
	}
	start := int(atomic.AddUint64(&l.counter, 1)-1) % l.n
	for i := 0; i < l.n; i++ {
		b := l.backends[(start+i)%l.n]
		if b.IsUp() {
			return b, nil
		}
	}
	return nil, ErrNoHealthyBackends
}

type HealthCheckOptions struct {
	Path             string
	Interval         time.Duration
	Timeout          time.Duration
	ConsiderStatusUp int
	Client           *http.Client
}

func StartHealthChecks(ctx context.Context, l *LB, opts HealthCheckOptions) {
	if l == nil || l.n == 0 {
		return
	}
	if opts.Interval <= 0 {
		opts.Interval = 5 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 3 * time.Second
	}
	if opts.ConsiderStatusUp == 0 {
		opts.ConsiderStatusUp = 500
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: opts.Timeout}
	}

	ticker := time.NewTicker(opts.Interval)
	go func() {
		defer ticker.Stop()
		checkAll(l, opts)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				checkAll(l, opts)
			}
		}
	}()
}

func checkAll(l *LB, opts HealthCheckOptions) {
	var wg sync.WaitGroup
	for _, b := range l.backends {
		wg.Add(1)
		go func(b *Backend) {
			defer wg.Done()
			up := probe(b, opts)
			b.SetUp(up)
		}(b)
	}
	wg.Wait()
}

func probe(b *Backend, opts HealthCheckOptions) bool {
	u := *b.URL
	u.Path = opts.Path

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := opts.Client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < opts.ConsiderStatusUp
}
