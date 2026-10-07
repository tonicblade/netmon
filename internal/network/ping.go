package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type Method string

const (
	MethodICMP Method = "icmp"
	MethodTCP  Method = "tcp"
)

var ErrNoMethod = errors.New("no usable probe method (ICMP permission denied and TCP fallback unavailable)")

type Pinger struct {
	target string
	ip     net.IP
	method Method
	port   int

	id   uint16
	seq  uint16
	conn *icmp.PacketConn
}

func NewPinger(target string, tcpPort int) (*Pinger, error) {
	if tcpPort <= 0 {
		tcpPort = 443
	}
	ip, err := resolveIPv4(target)
	if err != nil {
		return nil, err
	}
	p := &Pinger{target: target, ip: ip, port: tcpPort, id: uint16(os.Getpid() & 0xffff)}

	if conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); err == nil {
		p.conn = conn
		p.method = MethodICMP
		return p, nil
	}
	if conn, err := icmp.ListenPacket("udp4", "0.0.0.0"); err == nil {
		p.conn = conn
		p.method = MethodICMP
		return p, nil
	}
	p.method = MethodTCP
	return p, nil
}

func (p *Pinger) Target() string { return p.target }
func (p *Pinger) Addr() string   { return p.ip.String() }
func (p *Pinger) Method() Method { return p.method }

func (p *Pinger) Close() error {
	if p.conn != nil {
		return p.conn.Close()
	}
	return nil
}

func (p *Pinger) Probe(timeout time.Duration) (time.Duration, error) {
	if p.method == MethodTCP {
		return p.probeTCP(timeout)
	}
	return p.probeICMP(timeout)
}

func (p *Pinger) probeTCP(timeout time.Duration) (time.Duration, error) {
	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.Dial("tcp4", net.JoinHostPort(p.ip.String(), fmt.Sprint(p.port)))
	elapsed := time.Since(start)
	if err != nil {
		if isRefused(err) {

			return elapsed, nil
		}
		return 0, err
	}
	conn.Close()
	return elapsed, nil
}

func isRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Err != nil {
		return errors.Is(oe.Err, syscall.ECONNREFUSED)
	}
	return false
}

func (p *Pinger) probeICMP(timeout time.Duration) (time.Duration, error) {
	p.seq++
	seq := int(p.seq & 0xffff)

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   int(p.id),
			Seq:  seq,
			Data: []byte("netmon"),
		},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	if _, err := p.conn.WriteTo(wb, &net.IPAddr{IP: p.ip}); err != nil {
		return 0, err
	}
	if err := p.conn.SetReadDeadline(start.Add(timeout)); err != nil {
		return 0, err
	}

	rb := make([]byte, 1500)
	for {
		n, _, err := p.conn.ReadFrom(rb)
		if err != nil {
			return 0, err
		}
		rm, err := icmp.ParseMessage(1, rb[:n])
		if err != nil {
			continue
		}
		echo, ok := rm.Body.(*icmp.Echo)
		if !ok || echo.Seq != seq {
			continue
		}
		switch rm.Type {
		case ipv4.ICMPTypeEchoReply:
			return time.Since(start), nil
		}
	}
}

func resolveIPv4(host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
		return nil, fmt.Errorf("%s: IPv6 not supported yet", host)
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
	}
	return nil, fmt.Errorf("no IPv4 address for %s", host)
}

func PingOnce(target string, port int, timeout time.Duration) (time.Duration, Method, error) {
	p, err := NewPinger(target, port)
	if err != nil {
		return 0, "", err
	}
	defer p.Close()
	d, err := p.Probe(timeout)
	return d, p.Method(), err
}

func PingStream(ctx context.Context, target string, port int, interval, timeout time.Duration, fn func(time.Duration, error)) error {
	p, err := NewPinger(target, port)
	if err != nil {
		return err
	}
	defer p.Close()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		d, err := p.Probe(timeout)
		fn(d, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}
