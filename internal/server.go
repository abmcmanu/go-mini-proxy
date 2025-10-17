package internal

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Server struct {
	cfg     *Config
	servers []*http.Server
}

func NewServer(cfg *Config) *Server {
	return &Server{cfg: cfg}
}

func (s *Server) Start() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for _, p := range s.cfg.Proxies {
		proxy, err := NewProxy(p.Backends)
		if err != nil {
			return err
		}

		mux := http.NewServeMux()
		mux.Handle(p.PathPrefix, proxy.Handler())

		srv := &http.Server{
			Addr:    fmt.Sprintf(":%d", p.Listen),
			Handler: mux,
		}

		s.servers = append(s.servers, srv)

		go func(pc *ProxyConfig, lb *LB) {
			StartHealthChecks(ctx, lb, HealthCheckOptions{
				Path:             pc.HealthPath,
				Interval:         pc.HealthInterval,
				ConsiderStatusUp: 500,
			})
		}(p, proxy.LB)

		go func(srv *http.Server, name string) {
			log.Printf("[%s] listening on %s", name, srv.Addr)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("[%s] server error: %v", name, err)
			}
		}(srv, p.Name)
	}

	<-ctx.Done()
	log.Println("Arrêt demandé, shutdown...")

	for _, srv := range s.servers {
		ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctxTimeout); err != nil {
			log.Printf("Erreur shutdown: %v", err)
		}
	}
	return nil
}
