# mini-proxy (Go)

A minimal reverse proxy written in Go, built to learn how web servers, load balancing and concurrency work with goroutines.

Features:
- HTTP reverse proxy based on `httputil.ReverseProxy`
- round-robin load balancing across several backends
- periodic health checks that skip unavailable backends
- several proxies in one process, each on its own port
- graceful shutdown on `Ctrl+C` / `SIGTERM`

No external dependencies, standard library only.

---

## Project layout

```
cmd/main.go            entry point, loads the config and starts the server
internal/config.go     parser for the .conf file
internal/proxy.go      reverse proxy handler
internal/lb.go         backends, round-robin picker, health checks
internal/server.go     starts one HTTP server per proxy, handles shutdown
server/backend*.go     two test backends (ports 9001 and 9002)
examples/example.conf  sample configuration
```

---

## Concepts covered

| Concept | Where it is used |
|---------|------------------|
| Goroutine | Each proxy server, the health check loop and each probe run in their own goroutine. |
| Channel / select | The health check loop waits on `ticker.C` and `ctx.Done()`. |
| Context | `signal.NotifyContext` cancels the health checks and triggers the shutdown. |
| Mutex | `Backend` protects its `up` flag with a `sync.RWMutex`. |
| Atomic | `LB` uses `atomic.AddUint64` for the round-robin counter. |
| WaitGroup | `checkAll` waits for all probes of one round to finish. |
| Graceful shutdown | `http.Server.Shutdown` lets in-flight requests finish (5s timeout). |

---

## Getting started

Requires Go 1.24 or later.

Start the two test backends, each in its own terminal:

```sh
go run server/backend1.go
go run server/backend2.go
```

The files in `server/` each declare `main`, so run them one file at a time (`go build ./...` fails on that package).

Start the proxy with the default config (`examples/example.conf`) or pass a path:

```sh
go run ./cmd
go run ./cmd path/to/my.conf
```

Send a few requests and watch the responses alternate:

```sh
curl http://localhost:8080/api/
curl http://localhost:8080/api/
```

Stop `backend2` and the proxy sends all traffic to `backend1` after the next health check.

---

## Configuration file

Each block starts with `proxy <name>`, followed by `key = value` lines. Lines starting with `#` are comments.

```conf
proxy api
listen = 8080
path_prefix = /api/
lb_strategy = round_robin
health_path = /health
health_interval = 5s
backends = http://localhost:9001, http://localhost:9002
```

| Key | Description | Default |
|-----|-------------|---------|
| `listen` | Port to listen on. | none |
| `path_prefix` | Path handled by the proxy (`http.ServeMux` pattern). A trailing `/` matches every sub-path. | none |
| `lb_strategy` | Load balancing strategy. Only round-robin is implemented; the value is read but not used. | ignored |
| `health_path` | Path probed on each backend. A status below 500 marks the backend as up. | `/` (empty path) |
| `health_interval` | Delay between two health check rounds, Go duration format (`5s`, `1m`). | `5s` |
| `backends` | Comma-separated list of backend URLs. | none |

The request path is forwarded unchanged: a request to `/api/users` reaches the backend as `/api/users`.

---

## Known limitations

- Invalid values for `listen` or `health_interval` are silently ignored: the value stays at zero, so a bad `listen` makes the server pick a random port and a bad `health_interval` falls back to `5s`.
- Unknown keys are ignored without warning.
- When no backend is healthy, the request fails and the client receives `502 Bad Gateway`.
- No TLS, no retries, no request timeout on the proxy side.
