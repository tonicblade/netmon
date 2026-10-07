# netmon

a network monitor for the terminal. it samples interfaces, latency, sockets,
routes and dns in the background and shows everything through a single
dashboard.

the tui is the main interface. a small cli gives you the same data one command
at a time. it uses raw icmp for latency when it can (this needs elevated
rights) and falls back to tcp timing otherwise, so it runs on linux, macos and
windows.

## what it does

- live dashboard with aggregate rx/tx, latency, jitter, loss and health
- interfaces with per-nic rates, packets, errors, addresses and graphs
- latency probes per target with avg, min, max, jitter and loss
- active connections with process names, filter and sort
- routing table with the default route marked
- dns lookup, record types, resolver switching and resolver benchmark
- mtr-style traceroute with live hop table
- event timeline plus alerts for latency, loss, jitter and throughput drops
- a "net quality index" score in the header, 0-100

## project structure

- `cmd/netmon` - cli + tui entrypoint
- `internal/config` - yaml config with defaults
- `internal/network` - ping, traceroute, dns query and benchmark
- `internal/platform` - build-tagged os adapters (interfaces, sockets, routes)
- `internal/collector` - samplers that publish lock-protected snapshots
- `tui` - the bubble tea / lip gloss dashboard
- `pkg/types` - shared structs and time-series buffers

## quick start

from the project root:

```bash
go run ./cmd/netmon
```

or build it:

```bash
go build -buildvcs=false -o netmon ./cmd/netmon
```

no arguments opens the tui:

```bash
netmon
```

## cli

```
netmon                           open the tui
netmon ping <host> [flags]       latency probe
netmon trace <host> [flags]      traceroute
netmon dns <name> [flags]        dns lookup
netmon interfaces [--json]       interface inventory
netmon connections [flags]       active sockets
netmon routes [--json]           routing table
netmon stats [--watch D]         live aggregate stats
netmon help | version
```

global flag:

```
--config/-c path   yaml config (default: netmon.yaml, ~/.config/netmon/config.yaml)
```

### ping

```bash
netmon ping 1.1.1.1                    # run until interrupted
netmon ping 1.1.1.1 --count 5
netmon ping 1.1.1.1 --interval 300ms --timeout 1s
netmon ping 8.8.8.8 --port 443         # tcp fallback on :443
netmon ping 1.1.1.1 --json
```

### trace

```bash
netmon trace 8.8.8.8
netmon trace 1.1.1.1 --max-hops 20 --probes 3
netmon trace example.com --json
```

hops that never replied are shown with `*` and the table is redrawn as
hostnames finish reverse-dns lookups.

### dns

```bash
netmon dns example.com
netmon dns example.com --type CNAME
netmon dns example.com --server 9.9.9.9
netmon dns example.com --bench        # compare configured resolvers
netmon dns example.com --json
```

### connections

```bash
netmon connections
netmon connections --proto TCP --state ESTABLISHED
netmon connections --process chrome
netmon connections --sort port --json
```

### stats

```bash
netmon stats                # one snapshot
netmon stats --watch 2s     # refresh every 2 seconds until interrupted
```

every command accepts `--json` where it can, so the output is easy to pipe
into scripts.

## tui

```
1-8            switch tab
tab / shift+tab  next / previous tab
j / k          move cursor
g / G          top / bottom of list
ctrl+d / ctrl+u  page down / page up
/              filter (interfaces, connections, events)
h / l          graph time range (-1m .. -1h)
x              graph mode: RX+TX / RX / TX
p              pause / resume collectors
enter          dns query / trace target
r              re-run dns query / traceroute
n              dns record type (A, AAAA, ...)
s              dns resolver / connection sort
b              benchmark dns resolvers
t              start traceroute
?              help
q / ctrl+c     quit
```

## configuration

all values are optional; the built-in defaults match `configs/example.yaml`.
load a file with:

```bash
netmon --config configs/example.yaml
```

```yaml
# how often each collector samples
refresh:
  interface: 250ms
  ping: 1s
  connections: 2s
  routes: 10s

# latency probe targets (the default gateway is always added first)
targets:
  - name: Cloudflare
    address: 1.1.1.1
  - name: Google
    address: 8.8.8.8

# in-memory graph history (the tui range selector goes up to 1h)
graph:
  history: 5m

# alert thresholds; 0 disables an alert
alerts:
  latency_ms: 150
  packet_loss_pct: 5
  jitter_ms: 30
  bandwidth_drop_pct: 50   # throughput drop vs its 60s average
  webhook: ""              # discord/slack-style json webhook url
  log_file: ""             # append alerts to a file

# public resolvers used by the benchmark
dns:
  resolvers:
    - 1.1.1.1
    - 8.8.8.8
    - 9.9.9.9

ping:
  method: auto   # auto (raw icmp -> udp -> tcp), icmp, or tcp
  port: 443
  timeout: 1s
```

## privileges

raw icmp and traceroute need elevated rights:

- linux: run with `sudo`
- windows: open the shell as administrator
- macos: nothing special for icmp, but socket collection is limited

everything else (interfaces, sockets, routes, dns) works unprivileged.
without raw icmp, latency falls back to tcp-connect timing and the tui tells
you when a feature needs elevation.

## net quality index

a 0-100 heuristic score, updated on every ping tick:

| component  | weight | score rule                        |
| ---------- | ------ | --------------------------------- |
| latency    | .35    | 100 at <=10ms, linear to 0 at 150ms |
| jitter     | .15    | 100 at <=2ms, linear to 0 at 30ms   |
| loss       | .30    | 100 - (mean loss / 10 x 100)       |
| bandwidth  | .10    | packet drop / error ratio          |
| dns        | .10    | 100 at <=10ms, linear to 0 at 150ms |

## install it globally

### windows

1. install go if you do not already have it, from https://go.dev/dl/ and make
   sure `go` is on your PATH.

2. build the binary:

```powershell
go build -buildvcs=false -o netmon.exe ./cmd/netmon
```

3. move it into a folder already on your PATH:

```powershell
mkdir "$env:USERPROFILE\bin" -Force
move .\netmon.exe "$env:USERPROFILE\bin\netmon.exe"
```

4. add that folder to your PATH if it is not there:

```powershell
[Environment]::SetEnvironmentVariable(
  "Path",
  "$env:USERPROFILE\bin;" + [Environment]::GetEnvironmentVariable("Path", "User"),
  "User"
)
```

5. restart the terminal and run `netmon`. for raw icmp, launch the shell as
   administrator.

### linux

```bash
go build -buildvcs=false -o netmon ./cmd/netmon
sudo install -m 0755 ./netmon /usr/local/bin/netmon
netmon
```

run with `sudo` if you want raw icmp and traceroute.

### macos

```bash
go build -buildvcs=false -o netmon ./cmd/netmon
sudo install -m 0755 ./netmon /usr/local/bin/netmon
netmon
```

## notes

- collectors run on independent tickers (interfaces 250ms, ping 1s, sockets
  2s, routes 10s) and publish snapshots, so the tui never blocks on io
- dns and traceroute run as separate goroutines; the ui stays responsive
- the benchmark compares your configured resolvers plus the ones you add
- copilot is used for fixing small issues, writing documentation, etc.
