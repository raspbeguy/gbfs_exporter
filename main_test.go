package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/raspbeguy/gbfs_exporter/internal/gbfs"
)

// feeds is a small GBFS 3.0 system with one station and one free vehicle.
var feeds = map[string]string{
	"/gbfs.json": `{"last_updated":"2021-01-01T00:00:00Z","ttl":60,"version":"3.0","data":{"feeds":[
		{"name":"system_information","url":"system_information.json"},
		{"name":"station_information","url":"station_information.json"},
		{"name":"station_status","url":"station_status.json"},
		{"name":"vehicle_types","url":"vehicle_types.json"}]}}`,
	"/system_information.json": `{"data":{"system_id":"demo","name":[{"text":"Demo","language":"en"}],"timezone":"Europe/Paris"}}`,
	"/station_information.json": `{"data":{"stations":[
		{"station_id":"1","name":[{"text":"Gare","language":"en"}],"lat":48.8,"lon":2.3,"capacity":20}]}}`,
	"/station_status.json": `{"data":{"stations":[
		{"station_id":"1","num_vehicles_available":3,"num_docks_available":16,
		 "vehicle_types_available":[{"vehicle_type_id":"ebike","count":3}]}]}}`,
	"/vehicle_types.json": `{"data":{"vehicle_types":[
		{"vehicle_type_id":"ebike","form_factor":"bicycle","propulsion_type":"electric_assist"}]}}`,
}

// fakeSystem serves the feeds and records the headers of every request.
func fakeSystem(t *testing.T, delay time.Duration) (*httptest.Server, func() []http.Header) {
	t.Helper()
	var mutex sync.Mutex
	var seen []http.Header

	mux := http.NewServeMux()
	for path, body := range feeds {
		body := body
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			mutex.Lock()
			seen = append(seen, r.Header.Clone())
			mutex.Unlock()
			if delay > 0 {
				time.Sleep(delay)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, func() []http.Header {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]http.Header(nil), seen...)
	}
}

func newHandler(t *testing.T, config *Config) http.HandlerFunc {
	t.Helper()
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}
	if config.MaxInFlight == 0 {
		config.MaxInFlight = 4
	}
	client := gbfs.NewClient(5*time.Second, "gbfs_exporter/test")
	return probeHandler(client, config, slog.New(slog.DiscardHandler))
}

func get(t *testing.T, handler http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func TestMetricsHandlerRejects(t *testing.T) {
	server, _ := fakeSystem(t, 0)
	handler := newHandler(t, &Config{AllowedHosts: []string{"127.0.0.1"}})

	cases := []struct {
		name   string
		target string
		status int
		body   string
	}{
		{"no target", "/probe", http.StatusBadRequest, "target parameter is missing"},
		{"not http", "/probe?target=ftp://example.org/gbfs.json", http.StatusBadRequest, "must be an http or https URL"},
		{"host not allowed", "/probe?target=https://example.org/gbfs.json", http.StatusForbidden, "not in allowed_hosts"},
		{"unknown module", "/probe?target=" + server.URL + "/gbfs.json&module=absent", http.StatusBadRequest, "no module"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := get(t, handler, testCase.target)
			if recorder.Code != testCase.status {
				t.Fatalf("got status %d, want %d", recorder.Code, testCase.status)
			}
			if !strings.Contains(recorder.Body.String(), testCase.body) {
				t.Fatalf("got body %q, want it to hold %q", recorder.Body.String(), testCase.body)
			}
		})
	}
}

// An empty allowed_hosts list accepts every host. This is the default, and it
// is the reason that the README carries a warning.
func TestMetricsHandlerEmptyAllowedHostsAcceptsEveryHost(t *testing.T) {
	server, _ := fakeSystem(t, 0)
	handler := newHandler(t, &Config{})
	recorder := get(t, handler, "/probe?target="+server.URL+"/gbfs.json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", recorder.Code)
	}
}

func TestMetricsHandlerReadsTheTarget(t *testing.T) {
	server, _ := fakeSystem(t, 0)
	handler := newHandler(t, &Config{})

	recorder := get(t, handler, "/probe?target="+server.URL+"/gbfs.json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{
		"gbfs_up 1",
		`gbfs_station_vehicles_available{station_id="1"} 3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the body does not hold %q", want)
		}
	}
	// The exporter serves one endpoint, so it publishes no self metrics.
	for _, absent := range []string{"go_goroutines", "process_cpu_seconds_total"} {
		if strings.Contains(body, absent) {
			t.Errorf("the body holds the self metric %q", absent)
		}
	}
}

// The exporter sets no system label. Prometheus owns that label, and a
// host-derived fallback merged four nextbike cities into one value, because
// they all live on gbfs.nextbike.net.
func TestProbeSetsNoSystemLabel(t *testing.T) {
	server, _ := fakeSystem(t, 0)
	handler := newHandler(t, &Config{})
	body := get(t, handler, "/probe?target="+server.URL+"/gbfs.json").Body.String()
	if strings.Contains(body, "system=") {
		t.Fatalf("the body carries a system label:\n%s", body)
	}
}

// A module carries the settings that a query string must not carry. The
// header holds an API key, so it can only come from the configuration file.
func TestModuleSendsItsHeaders(t *testing.T) {
	server, headers := fakeSystem(t, 0)
	handler := newHandler(t, &Config{Modules: map[string]Module{
		"entur": {Headers: map[string]string{"ET-Client-Name": "test-client"}},
	}})

	if recorder := get(t, handler, "/probe?target="+server.URL+"/gbfs.json&module=entur"); recorder.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", recorder.Code)
	}
	seen := headers()
	if len(seen) == 0 {
		t.Fatal("the target got no request")
	}
	for index, header := range seen {
		if header.Get("ET-Client-Name") != "test-client" {
			t.Errorf("request %d carries ET-Client-Name %q, want \"test-client\"", index, header.Get("ET-Client-Name"))
		}
	}
}

func TestModuleControlsPerVehicleType(t *testing.T) {
	server, _ := fakeSystem(t, 0)
	handler := newHandler(t, &Config{Modules: map[string]Module{
		"default": {},
		"pertype": {PerVehicleType: true},
	}})

	withType := get(t, handler, "/probe?target="+server.URL+"/gbfs.json&module=pertype").Body.String()
	if !strings.Contains(withType, "gbfs_station_type_vehicles_available") {
		t.Error("the pertype module gives no per-type metric")
	}
	plain := get(t, handler, "/probe?target="+server.URL+"/gbfs.json").Body.String()
	if strings.Contains(plain, "gbfs_station_type_vehicles_available") {
		t.Error("the default module gives a per-type metric")
	}
}

// The cap must live outside the request. A counter built inside the request
// starts again on every scrape and never fills.
func TestMaxInFlightRejectsTheExtraScrape(t *testing.T) {
	server, _ := fakeSystem(t, 300*time.Millisecond)
	handler := newHandler(t, &Config{MaxInFlight: 1})
	url := "/probe?target=" + server.URL + "/gbfs.json"

	var wait sync.WaitGroup
	codes := make([]int, 4)
	for index := range codes {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			codes[index] = get(t, handler, url).Code
		}(index)
	}
	wait.Wait()

	var ok, rejected int
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusServiceUnavailable:
			rejected++
		default:
			t.Fatalf("got an unexpected status %d", code)
		}
	}
	if ok != 1 || rejected != 3 {
		t.Fatalf("got %d served and %d rejected, want 1 and 3", ok, rejected)
	}
}

func TestSelectModule(t *testing.T) {
	modules := map[string]Module{
		"default": {MaxConcurrency: 2},
		"entur":   {MaxConcurrency: 1},
	}
	if module, err := selectModule(modules, ""); err != nil || module.MaxConcurrency != 2 {
		t.Errorf("an empty name gives %+v and %v, want the default module", module, err)
	}
	if module, err := selectModule(modules, "entur"); err != nil || module.MaxConcurrency != 1 {
		t.Errorf("the name entur gives %+v and %v", module, err)
	}
	if _, err := selectModule(modules, "absent"); err == nil {
		t.Error("an unknown name gives no error")
	}
	// Without a default module, an empty name gives the zero value.
	if module, err := selectModule(map[string]Module{}, ""); err != nil || module.MaxConcurrency != 0 {
		t.Errorf("an empty configuration gives %+v and %v", module, err)
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigDefaults(t *testing.T) {
	config, err := loadConfig(writeConfig(t, ""))
	if err != nil {
		t.Fatalf("an empty file gives the error %v", err)
	}
	if config.ListenAddress != ":9718" {
		t.Errorf("listen_address is %q", config.ListenAddress)
	}
	if config.Timeout != 30*time.Second || config.RequestTimeout != 10*time.Second {
		t.Errorf("the timeouts are %v and %v", config.Timeout, config.RequestTimeout)
	}
	if config.MaxInFlight != 4 {
		t.Errorf("max_in_flight is %d", config.MaxInFlight)
	}
	if config.UserAgent != "gbfs_exporter/"+version {
		t.Errorf("user_agent is %q", config.UserAgent)
	}
}

func TestLoadConfigRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"an unknown key", "listen_adress: \":9718\"\n"},
		{"a systems list", "systems:\n  - name: oslo\n    url: https://example.org/gbfs.json\n"},
		{"a request timeout above the timeout", "timeout: 5s\nrequest_timeout: 10s\n"},
		{"a negative max_concurrency", "modules:\n  bad:\n    max_concurrency: -1\n"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := loadConfig(writeConfig(t, testCase.body)); err == nil {
				t.Fatal("the configuration loads without an error")
			}
		})
	}
}

func TestLoadConfigReadsModules(t *testing.T) {
	config, err := loadConfig(writeConfig(t, `
allowed_hosts: [gbfs.example.org]
max_in_flight: 2
modules:
  entur:
    max_concurrency: 1
    per_vehicle_type: true
    headers:
      ET-Client-Name: demo
`))
	if err != nil {
		t.Fatal(err)
	}
	module, ok := config.Modules["entur"]
	if !ok {
		t.Fatal("the module entur is missing")
	}
	if module.MaxConcurrency != 1 || !module.PerVehicleType || module.Headers["ET-Client-Name"] != "demo" {
		t.Fatalf("the module holds %+v", module)
	}
	if len(config.AllowedHosts) != 1 || config.MaxInFlight != 2 {
		t.Fatalf("the top level settings are %+v", config)
	}
}

// TestEndpointsAreSeparate checks the split that every established
// multi-target exporter uses. A scrape of one target must not carry the
// runtime metrics of the exporter, because they would repeat under the
// instance label of every target.
func TestEndpointsAreSeparate(t *testing.T) {
	server, _ := fakeSystem(t, 0)
	config := &Config{}
	probe := newHandler(t, config)

	self := prometheus.NewRegistry()
	self.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		versioncollector.NewCollector("gbfs_exporter"),
		rejected,
	)
	selfHandler := promhttp.HandlerFor(self, promhttp.HandlerOpts{})

	t.Run("the target endpoint carries no process metric", func(t *testing.T) {
		body := get(t, probe, "/probe?target="+server.URL+"/gbfs.json").Body.String()
		if !strings.Contains(body, "gbfs_up 1") {
			t.Fatalf("the target endpoint gives no GBFS data:\n%s", body)
		}
		for _, absent := range []string{"go_goroutines", "process_cpu_seconds_total", "gbfs_exporter_build_info"} {
			if strings.Contains(body, absent) {
				t.Errorf("the target endpoint carries %q", absent)
			}
		}
	})

	t.Run("the self endpoint carries no GBFS data", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		selfHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		body := recorder.Body.String()
		for _, want := range []string{"go_goroutines", "gbfs_exporter_build_info"} {
			if !strings.Contains(body, want) {
				t.Errorf("the self endpoint does not carry %q", want)
			}
		}
		for _, absent := range []string{"gbfs_station_", "gbfs_up ", "gbfs_feed_"} {
			if strings.Contains(body, absent) {
				t.Errorf("the self endpoint carries %q", absent)
			}
		}
	})
}

// TestRejectedRequestsAreCounted checks that a refused request leaves a trace.
// Such a request carries no metrics body, so Prometheus sees only that the
// scrape failed and never why.
func TestRejectedRequestsAreCounted(t *testing.T) {
	rejected.Reset()
	server, _ := fakeSystem(t, 0)
	handler := newHandler(t, &Config{
		AllowedHosts: []string{"127.0.0.1"},
		Modules:      map[string]Module{"known": {}},
	})

	get(t, handler, "/probe")
	get(t, handler, "/probe?target=https://elsewhere.example.org/gbfs.json")
	get(t, handler, "/probe?target="+server.URL+"/gbfs.json&module=absent")

	for reason, want := range map[string]float64{
		"bad_target": 1, "forbidden_host": 1, "unknown_module": 1,
	} {
		if got := testutil.ToFloat64(rejected.WithLabelValues(reason)); got != want {
			t.Errorf("reason %q counted %v, want %v", reason, got, want)
		}
	}
}
