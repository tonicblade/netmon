// Package types defines the shared vocabulary used by collectors, the TUI
// and the CLI. Every layer above the platform layer speaks these structs.
package types

import (
	"math"
	"sync"
	"time"
)

// DataPoint is a single timestamped measurement.
type DataPoint struct {
	Time  time.Time
	Value float64
}

// TimeSeries is a bounded in-memory rolling buffer used for graphing.
// It is safe for concurrent use: collectors append, the TUI reads.
type TimeSeries struct {
	mu   sync.Mutex
	name string
	unit string
	pts  []DataPoint
	max  int // capacity in points
}

func NewTimeSeries(name, unit string, maxPoints int) *TimeSeries {
	if maxPoints <= 0 {
		maxPoints = 3600
	}
	return &TimeSeries{name: name, unit: unit, max: maxPoints}
}

func (ts *TimeSeries) Name() string { return ts.name }
func (ts *TimeSeries) Unit() string { return ts.unit }

func (ts *TimeSeries) Append(v float64) {
	ts.AppendAt(time.Now(), v)
}

func (ts *TimeSeries) AppendAt(t time.Time, v float64) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.pts) >= ts.max {
		// shift: O(n) but n is bounded (<= a few thousand)
		copy(ts.pts, ts.pts[1:])
		ts.pts = ts.pts[:len(ts.pts)-1]
	}
	ts.pts = append(ts.pts, DataPoint{Time: t, Value: v})
}

// Window returns points within the last d, always at least 2 points when
// available (so a graph has something to draw).
func (ts *TimeSeries) Window(d time.Duration) []DataPoint {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.pts) == 0 {
		return nil
	}
	cut := time.Now().Add(-d)
	i := 0
	for i < len(ts.pts) && ts.pts[i].Time.Before(cut) {
		i++
	}
	if len(ts.pts)-i < 2 && len(ts.pts) >= 2 {
		i = len(ts.pts) - 2
	}
	out := make([]DataPoint, len(ts.pts)-i)
	copy(out, ts.pts[i:])
	return out
}

func (ts *TimeSeries) Last() (DataPoint, bool) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.pts) == 0 {
		return DataPoint{}, false
	}
	return ts.pts[len(ts.pts)-1], true
}

func (ts *TimeSeries) Len() int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return len(ts.pts)
}

// Peak returns the maximum value within d.
func (ts *TimeSeries) Peak(d time.Duration) float64 {
	var max float64
	for _, p := range ts.Window(d) {
		if p.Value > max {
			max = p.Value
		}
	}
	return max
}

// Avg returns the arithmetic mean value within d.
func (ts *TimeSeries) Avg(d time.Duration) float64 {
	pts := ts.Window(d)
	if len(pts) == 0 {
		return 0
	}
	s := 0.0
	for _, p := range pts {
		s += p.Value
	}
	return s / float64(len(pts))
}

// ---- interfaces ----

// InterfaceInfo is static-ish description of one NIC.
type InterfaceInfo struct {
	Name   string   `json:"name"`
	Index  int      `json:"index"`
	MTU    int      `json:"mtu"`
	MAC    string   `json:"mac"`
	IsUp   bool     `json:"is_up"`
	IsLoop bool     `json:"is_loopback"`
	Kind   string   `json:"kind"`  // eth, wifi, virt, loop, tun, other
	Speed  uint64   `json:"speed"` // bits/sec, 0 unknown
	Addr4  []string `json:"addr4"`
	Addr6  []string `json:"addr6"`
}

// InterfaceStats is one raw sample of OS counters for an interface.
type InterfaceStats struct {
	Name      string    `json:"name"`
	RxBytes   uint64    `json:"rx_bytes"`
	TxBytes   uint64    `json:"tx_bytes"`
	RxPackets uint64    `json:"rx_packets"`
	TxPackets uint64    `json:"tx_packets"`
	RxDrops   uint64    `json:"rx_drops"`
	TxDrops   uint64    `json:"tx_drops"`
	RxErrors  uint64    `json:"rx_errors"`
	TxErrors  uint64    `json:"tx_errors"`
	Timestamp time.Time `json:"timestamp"`
}

// InterfaceRate is a derived rates view (bytes/packets per second).
type InterfaceRate struct {
	Stats           InterfaceStats `json:"stats"`
	RxBytesPerSec   float64        `json:"rx_bytes_per_sec"`
	TxBytesPerSec   float64        `json:"tx_bytes_per_sec"`
	RxPacketsPerSec float64        `json:"rx_packets_per_sec"`
	TxPacketsPerSec float64        `json:"tx_packets_per_sec"`
}

// InterfaceSnapshot pairs one interface's static info, latest counters and
// derived rates — exactly what a dashboard row needs.
type InterfaceSnapshot struct {
	Info     InterfaceInfo  `json:"info"`
	Counters InterfaceStats `json:"counters"`
	RxBps    float64        `json:"rx_bps"`
	TxBps    float64        `json:"tx_bps"`
	RxPps    float64        `json:"rx_pps"`
	TxPps    float64        `json:"tx_pps"`
}

// ---- connections ----

type Connection struct {
	Proto   string `json:"proto"` // TCP, TCP6, UDP, UDP6
	Local   string `json:"local"`
	Remote  string `json:"remote"`
	State   string `json:"state"`
	PID     int    `json:"pid"`
	Process string `json:"process"`
	RxBytes uint64 `json:"rx_bytes,omitempty"`
	TxBytes uint64 `json:"tx_bytes,omitempty"`
}

// ---- routes ----

type Route struct {
	Destination string `json:"destination"`
	Gateway     string `json:"gateway"`
	Genmask     string `json:"genmask"`
	Metric      int    `json:"metric"`
	Iface       string `json:"iface"`
	Family      string `json:"family"` // IPv4, IPv6
	Flags       string `json:"flags,omitempty"`
	Default     bool   `json:"default"`
}

// ---- ping / latency ----

// PingTarget is one configured latency probe destination.
type PingTarget struct {
	Name    string `yaml:"name" json:"name"`
	Address string `yaml:"address" json:"address"`
	Port    int    `yaml:"port,omitempty" json:"port,omitempty"` // TCP fallback port
}

// PingSample is one probe result.
type PingSample struct {
	Time time.Time
	RTT  float64 // ms, negative means lost
}

// PingStats is aggregate statistics for one target.
type PingStats struct {
	Name    string    `json:"name"`    // display name
	Address string    `json:"address"` // ip or hostname probed
	Method  string    `json:"method"`  // icmp, tcp
	Sent    int       `json:"sent"`
	Recv    int       `json:"received"`
	Lost    int       `json:"lost"`
	LossPct float64   `json:"loss_pct"`
	Last    float64   `json:"last_ms"`
	Min     float64   `json:"min_ms"`
	Max     float64   `json:"max_ms"`
	Avg     float64   `json:"avg_ms"`
	Jitter  float64   `json:"jitter_ms"`
	StdDev  float64   `json:"stddev_ms"`
	LastOK  bool      `json:"last_ok"`
	Updated time.Time `json:"updated"`
}

// ---- traceroute ----

type Hop struct {
	Num      int       `json:"num"`
	Addr     string    `json:"addr"`
	Host     string    `json:"host"`
	Sent     int       `json:"sent"`
	Timeouts int       `json:"timeouts"`
	Last     float64   `json:"last_ms"`
	Avg      float64   `json:"avg_ms"`
	Min      float64   `json:"min_ms"`
	Max      float64   `json:"max_ms"`
	LossPct  float64   `json:"loss_pct"`
	LastSeen time.Time `json:"last_seen"`
}

// Clone returns a deep copy of a hop (including no samples today).
func (h Hop) Clone() Hop { return h }

// ---- DNS ----

type DNSRecord struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	TTL   uint32 `json:"ttl"`
	Value string `json:"value"`
}

type DNSResult struct {
	Query   string      `json:"query"`
	Type    string      `json:"type"`
	Server  string      `json:"server"`
	Records []DNSRecord `json:"records"`
	RTT     float64     `json:"rtt_ms"`
	Err     string      `json:"err,omitempty"`
	Time    time.Time   `json:"time"`
}

type DNSBenchmarkRow struct {
	Server string  `json:"server"`
	RTT    float64 `json:"rtt_ms"`
	OK     bool    `json:"ok"`
	Error  string  `json:"error,omitempty"`
}

// ---- alerts / events ----

// Level classifies timeline events and alerts.
type Level uint8

const (
	LevelInfo Level = iota
	LevelOK
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelOK:
		return "ok"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
}

// Event is one entry in the network event timeline.
type Event struct {
	Time   time.Time `json:"time"`
	Level  Level     `json:"level"`
	Text   string    `json:"text"`
	Detail string    `json:"detail"`
}

type Alert struct {
	Time   time.Time `json:"time"`
	Level  string    `json:"level"` // warn, error, info, ok
	Title  string    `json:"title"`
	Detail string    `json:"detail"`
}

type AlertRule struct {
	LatencyMS        float64 // warn when avg latency above this
	LossPct          float64 // warn when packet loss above this
	JitterMS         float64 // warn when jitter above this
	BandwidthDropPct float64 // warn when bandwidth drops this much below baseline
}

// ---- health ----

// Health is the Net Quality Index breakdown (heuristic, see README).
type Health struct {
	Score     int            `json:"score"`
	Parts     map[string]int `json:"parts"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// HostInfo describes the running host for the dashboard header.
type HostInfo struct {
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Uptime   string `json:"uptime"`
}

// ---- statistics helpers ----

// Clone returns a deep copy of ping statistics.
func (p PingStats) Clone() PingStats {
	c := p
	// no reference fields today; kept for safety as the struct evolves
	return c
}

// Avg arithmetic mean of a sample set.
func Avg(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// Min smallest sample.
func Min(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

// Max largest sample.
func Max(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		if x > m {
			m = x
		}
	}
	return m
}

// StdDev population standard deviation.
func StdDev(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	m := Avg(v)
	ss := 0.0
	for _, x := range v {
		d := x - m
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(v)))
}

// Jitter RFC 3550-style smoothed mean absolute difference between samples.
func Jitter(v []float64) float64 {
	if len(v) < 2 {
		return 0
	}
	s := 0.0
	for i := 1; i < len(v); i++ {
		s += math.Abs(v[i] - v[i-1])
	}
	return s / float64(len(v)-1)
}
