package cmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

/*
Mini reverse-proxy en Go
- config simple (voir exemple.conf plus haut)
- round-robin + health checks
- concurrent request handling via net/http (goroutines gérées par net/http)
*/

// ---------- Config structures ----------

type Config struct {
	Proxies []*ProxyConfig
}

type ProxyConfig struct {
	Name           string
	Listen         int
	PathPrefix     string
	LBStrategy     string
	HealthPath     string
	HealthInterval time.Duration
	Backends       []*Backend
}

type Backend struct {
	RawURL string
	URL    *url.URL

	mu sync.RWMutex
	up bool

	// stats could be added: active connections, successes, fails
}

func (b *Backend) SetUp(v bool) {
	b.mu.Lock()
	b.up = v
	b.mu.Unlock()
}
func (b *Backend) IsUp() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.up
}

// ---------- Config parser (très simple) ----------

func ParseConfig(r io.Reader) (*Config, error) {
	scanner := bufio.NewScanner(r)
	cfg := &Config{}
	var current *ProxyConfig
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		raw := scanner.Text()
		// strip comments
		if idx := strings.Index(raw, "#"); idx >= 0 {
			raw = raw[:idx]
		}
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		// tokenization
		toks := strings.Fields(line)
		if len(toks) == 0 {
			continue
		}

		switch toks[0] {
		case "proxy":
			// proxy <name> {
			if len(toks) < 3 || toks[2] != "{" {
				return nil, fmt.Errorf("ligne %d: syntaxe attendue: proxy <name> {", lineNo)
			}
			current = &ProxyConfig{
				Name:           toks[1],
				LBStrategy:     "round_robin",
				HealthPath:     "/",
				HealthInterval: 5 * time.Second,
			}
			cfg.Proxies = append(cfg.Proxies, current)
		case "}":
			current = nil
		default:
			if current == nil {
				return nil, fmt.Errorf("ligne %d: directive hors bloc proxy", lineNo)
			}
			switch toks[0] {
			case "listen":
				if len(toks) < 2 {
					return nil, fmt.Errorf("ligne %d: listen <port>", lineNo)
				}
				p, err := strconv.Atoi(toks[1])
				if err != nil {
					return nil, fmt.Errorf("ligne %d: port invalide: %v", lineNo, err)
				}
				current.Listen = p
			case "path":
				if len(toks) < 2 {
					return nil, fmt.Errorf("ligne %d: path <prefix>", lineNo)
				}
				current.PathPrefix = toks[1]
			case "lb":
				if len(toks) < 2 {
					return nil, fmt.Errorf("ligne %d: lb <strategy>", lineNo)
				}
				current.LBStrategy = toks[1]
			case "health_path":
				if len(toks) < 2 {
					return nil, fmt.Errorf("ligne %d: health_path <path>", lineNo)
				}
				current.HealthPath = toks[1]
			case "health_interval":
				if len(toks) < 2 {
					return nil, fmt.Errorf("ligne %d: health_interval <duration>", lineNo)
				}
				d, err := time.ParseDuration(toks[1])
				if err != nil {
					return nil, fmt.Errorf("ligne %d: duration invalide: %v", lineNo, err)
				}
				current.HealthInterval = d
			case "backend":
				if len(toks) < 2 {
					return nil, fmt.Errorf("ligne %d: backend <url>", lineNo)
				}
				rawurl := toks[1]
				u, err := url.Parse(rawurl)
				if err != nil {
					return nil, fmt.Errorf("ligne %d: url backend invalide: %v", lineNo, err)
				}
				b := &Backend{RawURL: rawurl, URL: u, up: true}
				current.Backends = append(current.Backends, b)
			default:
				return nil, fmt.Errorf("ligne %d: directive inconnue: %s", lineNo, toks[0])
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// validation
	for _, p := range cfg.Proxies {
		if p.Listen == 0 {
			return nil, fmt.Errorf("proxy %s: listen non défini", p.Name)
		}
		if p.PathPrefix == "" {
			return nil, fmt.Errorf("proxy %s: path non défini", p.Name)
		}
		if len(p.Backends) == 0 {
			return nil, fmt.Errorf("proxy %s: aucun backend", p.Name)
		}
	}
	return cfg, nil
}

// ---------- Load balancer (round-robin) ----------

type LB struct {
	backends []*Backend
	counter  uint64 // atomique
	n        int
}

func NewLB(bks []*Backend) *LB {
	return &LB{backends: bks, n: len(bks)}
}

// pick returns (backend, error). It will try up to n backends to find an up backend.
func (l *LB) Pick() (*Backend, error) {
	if l.n == 0 {
		return nil, errors.New("no backends")
	}
	start := int(atomic.AddUint64(&l.counter, 1) - 1)
	for i := 0; i < l.n; i++ {
		idx := (start + i) % l.n
		b := l.backends[idx]
		if b.IsUp() {
			return b, nil
		}
	}
	return nil, errors.New("no healthy backends")
}

// ---------- Proxy handler construction ----------

type ProxyInstance struct {
	cfg *ProxyConfig
	lb  *LB
	// we create a reverse proxy giving a director that selects backend for each request
	proxy *httputil.ReverseProxy
}

func NewProxyInstance(cfg *ProxyConfig) *ProxyInstance {
	lb := NewLB(cfg.Backends)

	// director will be called per request; pick backend here
	director := func(req *http.Request) {
		backend, err := lb.Pick()
		if err != nil {
			// When ReverseProxy's Director can't choose, we still let the ServeHTTP handle error.
			// But Director has no return; we stash the error in context header to be handled by wrapper.
			req.Header.Set("X-MiniProxy-Error", "no-backend")
			return
		}

		// rewrite URL
		// keep original path, but if the proxy defines a prefix to strip, handle in wrapper
		req.URL.Scheme = backend.URL.Scheme
		req.URL.Host = backend.URL.Host
		// If backend URL had a path, join with req.URL.Path
		if backend.URL.Path != "" && backend.URL.Path != "/" {
			req.URL.Path = singleJoiningSlash(backend.URL.Path, req.URL.Path)
		}
		// Preserve Host header or set to backend host
		req.Host = backend.URL.Host
		// X-Forwarded-For
		if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
			prior := req.Header.Get("X-Forwarded-For")
			if prior == "" {
				req.Header.Set("X-Forwarded-For", clientIP)
			} else {
				req.Header.Set("X-Forwarded-For", prior+", "+clientIP)
			}
		}
	}
	rp := &httputil.ReverseProxy{
		Director: director,
		ModifyResponse: func(resp *http.Response) error {
			// optionally modify responses (metrics, headers)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			// Custom error handling
			log.Printf("[proxy %s] error proxying %s: %v", cfg.Name, r.URL, err)
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		},
	}
	return &ProxyInstance{cfg: cfg, lb: lb, proxy: rp}
}

// wrapper handler that strips prefix and handles no-backend condition set by director
func (p *ProxyInstance) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ensure path matches prefix
		if !strings.HasPrefix(r.URL.Path, p.cfg.PathPrefix) {
			http.NotFound(w, r)
			return
		}
		// strip the prefix when forwarding, so backend sees trimmed path (config choice)
		origPath := r.URL.Path
		r2 := r.Clone(r.Context())
		trimmed := strings.TrimPrefix(origPath, p.cfg.PathPrefix)
		if trimmed == "" {
			trimmed = "/"
		}
		r2.URL.Path = trimmed

		// Only continue if there is at least one backend up (pre-check)
		if _, err := p.lb.Pick(); err != nil {
			http.Error(w, "Service Unavailable (no healthy backend)", http.StatusServiceUnavailable)
			return
		}

		// Call reverse proxy (its director will pick a backend)
		p.proxy.ServeHTTP(w, r2)
	})
}

// helper to join paths
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	default:
		return a + b
	}
}

// ---------- Health checks ----------

func StartHealthChecks(ctx context.Context, cfg *ProxyConfig) {
	interval := cfg.HealthInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, b := range cfg.Backends {
					go func(bk *Backend) {
						client := &http.Client{Timeout: 3 * time.Second}
						u := *bk.URL
						u.Path = cfg.HealthPath
						resp, err := client.Get(u.String())
						up := false
						if err == nil {
							if resp.StatusCode < 500 {
								up = true
							}
							_ = resp.Body.Close()
						}
						prev := bk.IsUp()
						bk.SetUp(up)
						if prev != up {
							log.Printf("[health] backend %s changed state: %v -> %v", bk.RawURL, prev, up)
						}
					}(b)
				}
			}
		}
	}()
}

// ---------- main & server startup ----------

func main() {
	confPath := flag.String("c", "example.conf", "config file")
	flag.Parse()

	f, err := os.Open(*confPath)
	if err != nil {
		log.Fatalf("ouverture config: %v", err)
	}
	cfg, err := ParseConfig(f)
	if err != nil {
		log.Fatalf("parse config: %v", err)
	}

	// Map listen port -> http.ServeMux (we allow multiple proxies on same port)
	muxes := map[int]*http.ServeMux{}
	proxies := []*ProxyInstance{}

	for _, p := range cfg.Proxies {
		inst := NewProxyInstance(p)
		proxies = append(proxies, inst)
		if _, ok := muxes[p.Listen]; !ok {
			muxes[p.Listen] = http.NewServeMux()
		}
		// register handler for path prefix
		muxes[p.Listen].Handle(p.PathPrefix, http.StripPrefix(p.PathPrefix, inst.Handler()))
		// Also register prefix with trailing slash if not present
		if !strings.HasSuffix(p.PathPrefix, "/") {
			muxes[p.Listen].Handle(p.PathPrefix+"/", http.StripPrefix(p.PathPrefix+"/", inst.Handler()))
		}
	}

	// start health checks
	ctx, cancel := context.WithCancel(context.Background())
	for _, p := range cfg.Proxies {
		StartHealthChecks(ctx, p)
	}

	// Start servers (one per listen port)
	servers := []*http.Server{}
	for port, mux := range muxes {
		addr := fmt.Sprintf(":%d", port)
		srv := &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  60 * time.Second,
		}
		servers = append(servers, srv)

		go func(s *http.Server) {
			log.Printf("Listening on %s", s.Addr)
			if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("server listen failed: %v", err)
			}
		}(srv)
	}

	// graceful shutdown
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigc
	log.Printf("signal %v received: shutting down...", sig)
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Add(1)
		go func(s *http.Server) {
			defer wg.Done()
			if err := s.Shutdown(shutdownCtx); err != nil {
				log.Printf("shutdown error: %v", err)
			}
		}(srv)
	}
	wg.Wait()
	log.Println("stopped.")
}
