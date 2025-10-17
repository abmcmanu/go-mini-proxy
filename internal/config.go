package internal

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type ProxyConfig struct {
	Name           string
	Listen         int
	PathPrefix     string
	LBStrategy     string
	HealthPath     string
	HealthInterval time.Duration
	Backends       []string
}

type Config struct {
	Proxies []*ProxyConfig
}

// LoadConfig lit un fichier texte simple style .conf et construit la configuration.
func LoadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var proxies []*ProxyConfig
	var current *ProxyConfig

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "proxy") {
			if current != nil {
				proxies = append(proxies, current)
			}
			current = &ProxyConfig{Name: strings.TrimSpace(strings.TrimPrefix(line, "proxy"))}
			continue
		}

		if current == nil {
			return nil, fmt.Errorf("clé hors section proxy: %q", line)
		}

		if parts := strings.SplitN(line, "=", 2); len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			switch key {
			case "listen":
				p, _ := strconv.Atoi(val)
				current.Listen = p
			case "path_prefix":
				current.PathPrefix = val
			case "lb_strategy":
				current.LBStrategy = val
			case "health_path":
				current.HealthPath = val
			case "health_interval":
				d, _ := time.ParseDuration(val)
				current.HealthInterval = d
			case "backends":
				current.Backends = strings.Split(val, ",")
				for i := range current.Backends {
					current.Backends[i] = strings.TrimSpace(current.Backends[i])
				}
			}
		}
	}

	if current != nil {
		proxies = append(proxies, current)
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}

	return &Config{Proxies: proxies}, nil
}
