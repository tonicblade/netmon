package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"netmon/pkg/types"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		if err := node.Decode(&s); err != nil {
			return err
		}
		v, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		*d = Duration(v)
		return nil
	}
	var n int64
	if err := node.Decode(&n); err != nil {
		return err
	}
	*d = Duration(time.Duration(n) * time.Second)
	return nil
}

func (d Duration) D() time.Duration { return time.Duration(d) }

type Refresh struct {
	Interface   Duration `yaml:"interface"`
	Ping        Duration `yaml:"ping"`
	Connections Duration `yaml:"connections"`
	Routes      Duration `yaml:"routes"`
}

type Graph struct {
	History Duration `yaml:"history"`
}

type Alerts struct {
	Latency       float64 `yaml:"latency_ms"`
	PacketLoss    float64 `yaml:"packet_loss_pct"`
	Jitter        float64 `yaml:"jitter_ms"`
	BandwidthDrop float64 `yaml:"bandwidth_drop_pct"`
	Webhook       string  `yaml:"webhook"`
	LogFile       string  `yaml:"log_file"`
}

type DNS struct {
	Resolvers []string `yaml:"resolvers"`
}

type Ping struct {
	Method  string   `yaml:"method"`
	Port    int      `yaml:"port"`
	Timeout Duration `yaml:"timeout"`
}

type Config struct {
	Refresh Refresh            `yaml:"refresh"`
	Targets []types.PingTarget `yaml:"targets"`
	Graph   Graph              `yaml:"graph"`
	Alerts  Alerts             `yaml:"alerts"`
	DNS     DNS                `yaml:"dns"`
	Ping    Ping               `yaml:"ping"`
}

func Default() *Config {
	return &Config{
		Refresh: Refresh{
			Interface:   Duration(250 * time.Millisecond),
			Ping:        Duration(time.Second),
			Connections: Duration(2 * time.Second),
			Routes:      Duration(10 * time.Second),
		},
		Targets: []types.PingTarget{
			{Name: "Cloudflare", Address: "1.1.1.1"},
			{Name: "Google", Address: "8.8.8.8"},
		},
		Graph: Graph{History: Duration(5 * time.Minute)},
		Alerts: Alerts{
			Latency:       150,
			PacketLoss:    5,
			Jitter:        30,
			BandwidthDrop: 50,
		},
		DNS: DNS{Resolvers: []string{"1.1.1.1", "8.8.8.8", "9.9.9.9"}},
		Ping: Ping{
			Method:  "auto",
			Port:    443,
			Timeout: Duration(time.Second),
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		path = findDefaultPath()
		if path == "" {
			return cfg, nil
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if path == "" {
			return cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(b, cfg); err != nil {
		return nil, errors.New("config " + path + ": " + err.Error())
	}
	cfg.applyDefaults()
	return cfg, nil
}

func (c *Config) applyDefaults() {
	d := Default()
	if c.Refresh.Interface <= 0 {
		c.Refresh.Interface = d.Refresh.Interface
	}
	if c.Refresh.Ping <= 0 {
		c.Refresh.Ping = d.Refresh.Ping
	}
	if c.Refresh.Connections <= 0 {
		c.Refresh.Connections = d.Refresh.Connections
	}
	if c.Refresh.Routes <= 0 {
		c.Refresh.Routes = d.Refresh.Routes
	}
	if c.Graph.History <= 0 {
		c.Graph.History = d.Graph.History
	}
	if c.Ping.Port == 0 {
		c.Ping.Port = d.Ping.Port
	}
	if c.Ping.Timeout <= 0 {
		c.Ping.Timeout = d.Ping.Timeout
	}
	if c.Ping.Method == "" {
		c.Ping.Method = "auto"
	}
	if c.Alerts.Latency == 0 {
		c.Alerts.Latency = d.Alerts.Latency
	}
	if c.Alerts.PacketLoss == 0 {
		c.Alerts.PacketLoss = d.Alerts.PacketLoss
	}
	if c.Alerts.Jitter == 0 {
		c.Alerts.Jitter = d.Alerts.Jitter
	}
	if len(c.DNS.Resolvers) == 0 {
		c.DNS.Resolvers = d.DNS.Resolvers
	}
}

func findDefaultPath() string {
	candidates := []string{"netmon.yaml", "netmon.yml"}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".config", "netmon", "config.yaml"),
			filepath.Join(home, ".netmon.yaml"),
		)
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func (c *Config) Rule() types.AlertRule {
	return types.AlertRule{
		LatencyMS:        c.Alerts.Latency,
		LossPct:          c.Alerts.PacketLoss,
		JitterMS:         c.Alerts.Jitter,
		BandwidthDropPct: c.Alerts.BandwidthDrop,
	}
}
