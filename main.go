// Command gbfs_exporter serves GBFS feed data as Prometheus metrics.
//
// The exporter reads the feeds while Prometheus scrapes it. Set the scrape
// interval to the ttl of the feeds, or higher. A short interval puts load on
// the operator API and can hit a rate limit.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"

	"github.com/raspbeguy/gbfs_exporter/internal/collector"
	"github.com/raspbeguy/gbfs_exporter/internal/gbfs"
)

const version = "0.1.0"

// Config is the content of the configuration file.
type Config struct {
	ListenAddress string `yaml:"listen_address"`
	// Timeout is the budget for one scrape. It covers every feed of every
	// system.
	Timeout time.Duration `yaml:"timeout"`
	// RequestTimeout is the budget for one feed. Keep it well below Timeout,
	// because a system with max_concurrency set reads its feeds one after the
	// other inside the scrape budget.
	RequestTimeout time.Duration      `yaml:"request_timeout"`
	UserAgent      string             `yaml:"user_agent"`
	Probe          ProbeConfig        `yaml:"probe"`
	Systems        []collector.System `yaml:"systems"`
}

// ProbeConfig controls the /probe endpoint.
type ProbeConfig struct {
	// Enabled turns the endpoint on. The default is true.
	Enabled *bool `yaml:"enabled"`
	// AllowedHosts lists the hosts that the endpoint accepts. An empty list
	// accepts every host.
	AllowedHosts []string `yaml:"allowed_hosts"`
	// MaxInFlight limits the number of probes that run at the same time.
	MaxInFlight int `yaml:"max_in_flight"`
}

func main() {
	configPath := flag.String("config", "config.yml", "Path of the configuration file.")
	listenAddress := flag.String("web.listen-address", "", "Address to listen on. This flag overrides the configuration file.")
	logLevel := flag.String("log.level", "info", "Log level: debug, info, warn, or error.")
	showVersion := flag.Bool("version", false, "Print the version and exit.")
	flag.Parse()

	if *showVersion {
		fmt.Println("gbfs_exporter", version)
		return
	}

	log := newLogger(*logLevel)

	config, err := loadConfig(*configPath)
	if err != nil {
		log.Error("cannot load the configuration", "path", *configPath, "error", err)
		os.Exit(1)
	}
	if *listenAddress != "" {
		config.ListenAddress = *listenAddress
	}

	client := gbfs.NewClient(config.RequestTimeout, config.UserAgent)

	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registry.MustRegister(collector.New(client, config.Systems, config.Timeout, log))

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
		Registry:      registry,
	}))
	if config.Probe.Enabled == nil || *config.Probe.Enabled {
		mux.HandleFunc("/probe", probeHandler(client, config, log))
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", indexHandler)

	server := &http.Server{
		Addr:              config.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Info("listening", "address", config.ListenAddress, "systems", len(config.Systems))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("the server stopped", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

// probeHandler reads one system that the query string names.
//
// Use it to scrape a system that the configuration file does not list. The
// query parameters are target, name, per_vehicle_type, and max_concurrency.
//
// The endpoint fetches the URL that the caller gives. Set allowed_hosts, or
// keep the port closed to untrusted callers.
func probeHandler(client *gbfs.Client, config *Config, log *slog.Logger) http.HandlerFunc {
	allowed := make(map[string]bool, len(config.Probe.AllowedHosts))
	for _, host := range config.Probe.AllowedHosts {
		allowed[strings.ToLower(host)] = true
	}
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("target")
		if target == "" {
			http.Error(w, "the target parameter is missing", http.StatusBadRequest)
			return
		}
		parsed, err := url.Parse(target)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			http.Error(w, "the target parameter must be an http or https URL", http.StatusBadRequest)
			return
		}
		if len(allowed) > 0 && !allowed[strings.ToLower(parsed.Hostname())] {
			log.Warn("the probe target is not in allowed_hosts", "host", parsed.Hostname())
			http.Error(w, "the host of the target is not in allowed_hosts", http.StatusForbidden)
			return
		}

		name := r.URL.Query().Get("name")
		if name == "" {
			name = parsed.Host
		}
		perType, _ := strconv.ParseBool(r.URL.Query().Get("per_vehicle_type"))
		maxConcurrency, _ := strconv.Atoi(r.URL.Query().Get("max_concurrency"))

		system := collector.System{
			Name:           name,
			URL:            target,
			PerVehicleType: perType,
			MaxConcurrency: maxConcurrency,
		}
		registry := prometheus.NewRegistry()
		single := collector.New(client, []collector.System{system}, config.Timeout, log)
		// The request context stops the outbound requests when Prometheus
		// gives up on the scrape.
		registry.MustRegister(single.WithContext(r.Context()))
		promhttp.HandlerFor(registry, promhttp.HandlerOpts{
			ErrorHandling:       promhttp.ContinueOnError,
			MaxRequestsInFlight: config.Probe.MaxInFlight,
		}).ServeHTTP(w, r)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, `<html>
<head><title>GBFS exporter</title></head>
<body>
<h1>GBFS exporter %s</h1>
<p><a href="/metrics">Metrics of the configured systems</a></p>
<p>To read one other system, open /probe?target=&lt;URL of gbfs.json&gt;</p>
</body>
</html>
`, version)
}

func loadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	config := &Config{}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(config); err != nil {
		return nil, err
	}

	if config.ListenAddress == "" {
		config.ListenAddress = ":9718"
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 10 * time.Second
	}
	if config.RequestTimeout > config.Timeout {
		return nil, errors.New("request_timeout is higher than timeout")
	}
	if config.Probe.MaxInFlight <= 0 {
		config.Probe.MaxInFlight = 4
	}
	if config.UserAgent == "" {
		config.UserAgent = "gbfs_exporter/" + version
	}
	if len(config.Systems) == 0 {
		return nil, errors.New("the configuration lists no system")
	}

	names := map[string]bool{}
	for index, system := range config.Systems {
		if system.URL == "" {
			return nil, fmt.Errorf("system %d has no url", index+1)
		}
		parsed, err := url.Parse(system.URL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("system %d has a url that is not http or https", index+1)
		}
		if system.Name == "" {
			config.Systems[index].Name = parsed.Host
		}
		name := config.Systems[index].Name
		if names[name] {
			return nil, fmt.Errorf("two systems use the name %q", name)
		}
		names[name] = true
	}
	return config, nil
}

func newLogger(level string) *slog.Logger {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		parsed = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parsed}))
}
