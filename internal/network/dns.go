package network

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"netmon/pkg/types"
)

// DNSQuery describes one lookup.
type DNSQuery struct {
	Name    string // domain to resolve
	Type    string // A, AAAA, CNAME, MX, TXT, NS — empty means A
	Server  string // resolver IP; empty = operating system resolver
	Timeout time.Duration
}

// Query performs one DNS lookup and returns records plus timing.
// With Server == "" the OS resolver is used (real system configuration);
// with a Server set, a direct UDP query is sent to that resolver.
func Query(ctx context.Context, q DNSQuery) types.DNSResult {
	if q.Timeout <= 0 {
		q.Timeout = 3 * time.Second
	}
	if q.Type == "" {
		q.Type = "A"
	}
	q.Type = strings.ToUpper(q.Type)
	res := types.DNSResult{
		Query:  q.Name,
		Type:   q.Type,
		Server: q.Server,
		Time:   time.Now(),
	}
	if res.Server == "" {
		res.Server = "system"
	}

	start := time.Now()
	var records []types.DNSRecord
	var err error

	if q.Server == "" {
		records, err = querySystem(ctx, q)
	} else {
		records, err = queryDirect(ctx, q)
	}
	res.RTT = float64(time.Since(start).Microseconds()) / 1000
	res.Records = records
	if err != nil {
		res.Err = err.Error()
	}
	return res
}

func querySystem(ctx context.Context, q DNSQuery) ([]types.DNSRecord, error) {
	if q.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, q.Timeout)
		defer cancel()
	}
	r := net.DefaultResolver
	name := strings.TrimSuffix(q.Name, ".")
	switch q.Type {
	case "A":
		ips, err := r.LookupIP(ctx, "ip4", name)
		return ipRecords(name, "A", ips), err
	case "AAAA":
		ips, err := r.LookupIP(ctx, "ip6", name)
		return ipRecords(name, "AAAA", ips), err
	case "CNAME":
		cname, err := r.LookupCNAME(ctx, name)
		if err != nil {
			return nil, err
		}
		return []types.DNSRecord{{Name: name, Type: "CNAME", Value: cname}}, nil
	case "MX":
		mxs, err := r.LookupMX(ctx, name)
		if err != nil {
			return nil, err
		}
		out := make([]types.DNSRecord, 0, len(mxs))
		for _, mx := range mxs {
			out = append(out, types.DNSRecord{
				Name: name, Type: "MX", Value: fmt.Sprintf("%d %s", mx.Pref, mx.Host),
			})
		}
		return out, nil
	case "TXT":
		txts, err := r.LookupTXT(ctx, name)
		if err != nil {
			return nil, err
		}
		out := make([]types.DNSRecord, 0, len(txts))
		for _, t := range txts {
			out = append(out, types.DNSRecord{Name: name, Type: "TXT", Value: t})
		}
		return out, nil
	case "NS":
		nss, err := r.LookupNS(ctx, name)
		if err != nil {
			return nil, err
		}
		out := make([]types.DNSRecord, 0, len(nss))
		for _, ns := range nss {
			out = append(out, types.DNSRecord{Name: name, Type: "NS", Value: ns.Host})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported record type %s with system resolver", q.Type)
	}
}

func ipRecords(name, typ string, ips []net.IP) []types.DNSRecord {
	out := make([]types.DNSRecord, 0, len(ips))
	for _, ip := range ips {
		out = append(out, types.DNSRecord{Name: name, Type: typ, Value: ip.String()})
	}
	return out
}

func queryDirect(ctx context.Context, q DNSQuery) ([]types.DNSRecord, error) {
	qt, ok := dns.StringToType[q.Type]
	if !ok {
		return nil, fmt.Errorf("unknown record type %q", q.Type)
	}
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(q.Name), qt)
	m.RecursionDesired = true

	c := &dns.Client{Net: "udp", Timeout: q.Timeout}
	server := q.Server
	if !strings.Contains(server, ":") {
		server = net.JoinHostPort(server, "53")
	}
	in, _, err := c.ExchangeContext(ctx, m, server)
	if err != nil {
		return nil, err
	}
	if in.Rcode != dns.RcodeSuccess {
		return nil, fmt.Errorf("rcode %s", dns.RcodeToString[in.Rcode])
	}
	var out []types.DNSRecord
	for _, rr := range in.Answer {
		out = append(out, types.DNSRecord{
			Name:  rr.Header().Name,
			Type:  dns.TypeToString[rr.Header().Rrtype],
			TTL:   rr.Header().Ttl,
			Value: rrString(rr),
		})
	}
	return out, nil
}

func rrString(rr dns.RR) string {
	switch v := rr.(type) {
	case *dns.A:
		return v.A.String()
	case *dns.AAAA:
		return v.AAAA.String()
	case *dns.CNAME:
		return v.Target
	case *dns.NS:
		return v.Ns
	case *dns.PTR:
		return v.Ptr
	case *dns.MX:
		return fmt.Sprintf("%d %s", v.Preference, v.Mx)
	case *dns.TXT:
		return strings.Join(v.Txt, " ")
	case *dns.SOA:
		return fmt.Sprintf("%s %s %d %d %d %d %d", v.Ns, v.Mbox, v.Serial, v.Refresh, v.Retry, v.Expire, v.Minttl)
	default:
		return rr.String()
	}
}

// Benchmark measures query latency against several resolvers in parallel.
func Benchmark(ctx context.Context, name string, servers []string, timeout time.Duration) []types.DNSBenchmarkRow {
	if name == "" {
		name = "example.com"
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	out := make([]types.DNSBenchmarkRow, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func(i int, server string) {
			defer wg.Done()
			res := Query(ctx, DNSQuery{
				Name: name, Type: "A", Server: server, Timeout: timeout,
			})
			out[i] = types.DNSBenchmarkRow{
				Server: server,
				RTT:    res.RTT,
				OK:     res.Err == "" && len(res.Records) > 0,
				Error:  res.Err,
			}
		}(i, s)
	}
	wg.Wait()
	sortRows(out)
	return out
}

func sortRows(rows []types.DNSBenchmarkRow) {
	// fastest successes first, failures last
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			aOK, bOK := a.OK, b.OK
			if (aOK && bOK && a.RTT > b.RTT) || (!aOK && bOK) {
				rows[j-1], rows[j] = b, a
				continue
			}
			break
		}
	}
}
