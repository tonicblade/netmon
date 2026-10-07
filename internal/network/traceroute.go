package network

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"

	"netmon/pkg/types"
)

// TraceOptions tunes an MTR-style traceroute run.
type TraceOptions struct {
	Target       string
	MaxHops      int
	ProbesPerHop int
	Timeout      time.Duration // per probe
	Interval     time.Duration // between probes
}

func (o *TraceOptions) withDefaults() TraceOptions {
	if o.MaxHops <= 0 {
		o.MaxHops = 30
	}
	if o.ProbesPerHop <= 0 {
		o.ProbesPerHop = 3
	}
	if o.Timeout <= 0 {
		o.Timeout = time.Second
	}
	if o.Interval <= 0 {
		o.Interval = 200 * time.Millisecond
	}
	return *o
}

type traceProbe struct {
	hop      int
	sent     time.Time
	answered bool
	rtt      time.Duration
	from     net.IP
}

// Trace runs an interactive traceroute. onUpdate is invoked after every
// probe and at completion so a TUI can render progressively.
// Requires raw ICMP privileges (elevated/admin); the error explains this.
func Trace(ctx context.Context, opts TraceOptions, onUpdate func(hops []types.Hop, reached bool)) ([]types.Hop, error) {
	o := opts.withDefaults()
	target, err := resolveIPv4(o.Target)
	if err != nil {
		return nil, err
	}

	conn, rawMode, err := openICMP()
	if err != nil {
		return nil, fmt.Errorf("traceroute needs raw ICMP privileges (run elevated): %w", err)
	}
	_ = rawMode // raw sockets get real ICMP; dgram sockets match by seq only
	defer conn.Close()

	p4 := conn.IPv4PacketConn()
	if p4 == nil {
		return nil, fmt.Errorf("traceroute: no IPv4 packet conn")
	}
	if err := p4.SetTTL(1); err != nil {
		return nil, fmt.Errorf("traceroute cannot set TTL (run elevated): %w", err)
	}

	var (
		id   = uint16(time.Now().UnixNano() & 0xffff)
		seq  int
		mu   sync.Mutex
		seen = map[int]*traceProbe{}
	)

	hops := make([]types.Hop, o.MaxHops)
	for i := range hops {
		hops[i] = types.Hop{Num: i + 1}
	}
	reached := false

	readLoop := func(deadline time.Time) {
		for time.Now().Before(deadline) {
			if err := p4.SetReadDeadline(deadline); err != nil {
				return
			}
			rb := make([]byte, 1500)
			n, _, addr, err := p4.ReadFrom(rb)
			if err != nil {
				return
			}
			msg, err := icmp.ParseMessage(1, rb[:n])
			if err != nil {
				continue
			}
			var (
				ok       bool
				replySeq int
				rtt      time.Duration
			)
			switch msg.Type {
			case ipv4.ICMPTypeEchoReply:
				echo, good := msg.Body.(*icmp.Echo)
				// Windows rewrites the Identifier field of its ICMP echo
				// sockets, so match on sequence number only (same as ping).
				if !good {
					continue
				}
				replySeq, ok = int(echo.Seq), true
			case ipv4.ICMPTypeTimeExceeded, ipv4.ICMPTypeDestinationUnreachable:
				_, iSeq, good := innerEchoSeq(msg)
				if !good {
					continue
				}
				replySeq, ok = iSeq, true
			default:
				continue
			}
			if !ok {
				continue
			}
			mu.Lock()
			if p, exists := seen[replySeq]; exists && !p.answered {
				p.answered = true
				p.rtt = time.Since(p.sent)
				if ip, isIP := addr.(*net.IPAddr); isIP {
					p.from = ip.IP.To4()
				}
			}
			mu.Unlock()
			_ = rtt
		}
	}

	deadlineCtx := ctx.Done()

	for hop := 1; hop <= o.MaxHops && !reached; hop++ {
		var hopProbes []*traceProbe
		for i := 0; i < o.ProbesPerHop; i++ {
			select {
			case <-deadlineCtx:
				return finalize(hops, reached), ctx.Err()
			default:
			}

			seq++
			p := &traceProbe{hop: hop, sent: time.Now()}
			mu.Lock()
			seen[seq] = p
			mu.Unlock()
			hopProbes = append(hopProbes, p)

			if err := p4.SetTTL(hop); err != nil {
				return finalize(hops, reached), fmt.Errorf("traceroute cannot set TTL: %w", err)
			}
			msg := icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: &icmp.Echo{ID: int(id), Seq: seq, Data: []byte("netmon-tr")},
			}
			if wb, err := msg.Marshal(nil); err == nil {
				_, _ = p4.WriteTo(wb, nil, &net.IPAddr{IP: target})
			}

			readLoop(time.Now().Add(o.Timeout))

			// inter-probe pacing minus time already spent reading
			remaining := o.Interval - time.Since(p.sent)
			if remaining > 0 {
				select {
				case <-deadlineCtx:
					return finalize(hops, reached), ctx.Err()
				case <-time.After(remaining):
				}
			}
		}

		// fold this hop's probes into the hop record
		var rtts []float64
		lost := 0
		h := &hops[hop-1]
		h.Num = hop
		for _, p := range hopProbes {
			if p.answered && p.from != nil {
				ms := float64(p.rtt.Nanoseconds()) / 1e6
				rtts = append(rtts, ms)
				if h.Addr == "" {
					h.Addr = p.from.String()
				}
				h.LastSeen = p.sent.Add(p.rtt)
				if h.Last == 0 || ms < h.Min || h.Min == 0 {
					h.Min = ms
				}
				if ms > h.Max {
					h.Max = ms
				}
				h.Last = ms
			} else {
				lost++
			}
		}
		h.Sent = len(hopProbes)
		h.Timeouts = lost
		h.LossPct = float64(lost) / float64(len(hopProbes)) * 100
		if len(rtts) > 0 {
			h.Avg = mean(rtts)
		}
		h.Addr = bestAddr(hopProbes, h.Addr)

		if h.Addr == target.String() {
			reached = true
		}
		if onUpdate != nil {
			onUpdate(finalize(hops, reached), reached)
		}
	}

	// reverse DNS, best effort with a short overall budget
	resolveHostnames(hops, 800*time.Millisecond)
	if onUpdate != nil {
		onUpdate(finalize(hops, reached), reached)
	}
	return finalize(hops, reached), nil
}

// bestAddr returns the most common responder for the hop probes.
func bestAddr(probes []*traceProbe, fallback string) string {
	counts := map[string]int{}
	for _, p := range probes {
		if p.answered && p.from != nil {
			counts[p.from.String()]++
		}
	}
	best, bestN := fallback, -1
	for a, n := range counts {
		if n > bestN {
			best, bestN = a, n
		}
	}
	return best
}

func finalize(hops []types.Hop, _ bool) []types.Hop {
	out := make([]types.Hop, 0, len(hops))
	for _, h := range hops {
		if h.Num == 0 || h.Sent == 0 {
			continue
		}
		out = append(out, h.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Num < out[j].Num })
	return out
}

// innerEchoSeq extracts (id, seq) from the ICMP error payload, which holds
// the original IP header followed by 8 bytes of the offending datagram.
func innerEchoSeq(msg *icmp.Message) (uint16, int, bool) {
	var data []byte
	switch b := msg.Body.(type) {
	case *icmp.TimeExceeded:
		data = b.Data
	case *icmp.DstUnreach:
		data = b.Data
	default:
		return 0, 0, false
	}
	if len(data) < 8 {
		return 0, 0, false
	}
	ihl := int(data[0]&0x0f) * 4
	if ihl < 20 || len(data) < ihl+8 {
		return 0, 0, false
	}
	inner := data[ihl : ihl+8]
	// inner is the first 8 bytes of the offending ICMP message: echo header.
	// type(0) code(1) csum(2..3) id(4..5) seq(6..7)
	if inner[0] != 8 { // not an echo request
		return 0, 0, false
	}
	id := binary.BigEndian.Uint16(inner[4:6])
	seq := binary.BigEndian.Uint16(inner[6:8])
	return id, int(seq), true
}

// openICMP prefers a raw ICMP socket and falls back to the unprivileged
// datagram socket (Windows delivers ICMP errors on those; Linux usually
// does not, in which case every hop times out and the UI shows a hint).
func openICMP() (*icmp.PacketConn, bool, error) {
	if c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); err == nil {
		return c, true, nil
	}
	if c, err := icmp.ListenPacket("udp4", "0.0.0.0"); err == nil {
		return c, false, nil
	}
	return nil, false, fmt.Errorf("permission denied opening ICMP socket")
}

// resolveHostnames does reverse lookups with a bounded overall budget.
func resolveHostnames(hops []types.Hop, budget time.Duration) {
	type res struct {
		idx  int
		host string
	}
	ch := make(chan res, len(hops))
	var wg sync.WaitGroup
	for i := range hops {
		if hops[i].Addr == "" {
			continue
		}
		wg.Add(1)
		go func(idx int, addr string) {
			defer wg.Done()
			names, err := net.LookupAddr(addr)
			if err == nil && len(names) > 0 {
				ch <- res{idx: idx, host: trimDot(names[0])}
			}
		}(i, hops[i].Addr)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	deadline := time.After(budget)
	for {
		select {
		case r := <-ch:
			if hops[r.idx].Host == "" {
				hops[r.idx].Host = r.host
			}
		case <-done:
			// drain what's left
			for {
				select {
				case r := <-ch:
					if hops[r.idx].Host == "" {
						hops[r.idx].Host = r.host
					}
				default:
					return
				}
			}
		case <-deadline:
			return
		}
	}
}

func trimDot(s string) string {
	if len(s) > 0 && s[len(s)-1] == '.' {
		return s[:len(s)-1]
	}
	return s
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}
