# 🌀 mini-proxy (Go)

A minimal **reverse proxy** written in Go to learn how web servers, load balancing, and concurrency management work with **goroutines** and **channels**.

This project demonstrates how to build from scratch:
- a simple HTTP proxy,
- a **round-robin** load balancer,
- a **health check** system for backend servers.

---

## ⚙️ Key Concepts Covered

| Concept | Explanation |
|----------|--------------|
| **Goroutine** | Lightweight thread managed by Go (`go f()`). Enables concurrent execution with minimal overhead. |
| **Channel** | Communication pipe between goroutines (`make(chan T)`, `<-ch`, `ch <- v`). Used for safe synchronization without explicit locks. |
| **Reverse Proxy** | A server that receives client requests and forwards them to internal backend servers. |
| **Load Balancer (Round-Robin)** | Distributes incoming requests evenly and sequentially across multiple backends. |
| **Health Check** | Periodically checks the health of backend servers to skip unavailable ones. |
| **Graceful Shutdown** | Gracefully stops the server by closing all active connections and goroutines before exiting. |
| **Mutex / Atomic** | Ensures thread-safe access to shared state or memory. |

---

## 🧩 Configuration File (`example.conf`)

The configuration file defines which proxies to start.  
Each block starts with `proxy <name>` followed by key-value pairs (`key = value`).

```conf
# example.conf

proxy app1
listen = 8080
path_prefix = /
lb_strategy = round_robin
health_path = /health
health_interval = 5s
backends = http://localhost:9001, http://localhost:9002
