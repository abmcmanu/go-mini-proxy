package internal

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

type Proxy struct {
	LB *LB
}

func NewProxy(backends []string) (*Proxy, error) {
	var bks []*Backend
	for _, addr := range backends {
		u, err := url.Parse(addr)
		if err != nil {
			return nil, err
		}
		bks = append(bks, NewBackend(u))
	}
	return &Proxy{LB: NewLB(bks)}, nil
}

func (p *Proxy) Handler() http.Handler {
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			b, err := p.LB.Pick()
			if err != nil {
				log.Printf("No backend available: %v", err)
				return
			}
			req.URL.Scheme = b.URL.Scheme
			req.URL.Host = b.URL.Host
			req.Host = b.URL.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Proxy error: %v", err)
			http.Error(w, "Bad gateway", http.StatusBadGateway)
		},
	}
	return proxy
}
