package collector_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/raspbeguy/gbfs_exporter/internal/collector"
	"github.com/raspbeguy/gbfs_exporter/internal/gbfs"
)

// version2Feeds is a small GBFS 2.3 system. The station names are plain
// strings, the counters use the word "bikes", and one station reports the
// boolean fields as numbers.
var version2Feeds = map[string]string{
	"/gbfs.json": `{"last_updated":1609459200,"ttl":60,"version":"2.3","data":{"en":{"feeds":[
		{"name":"system_information","url":"system_information.json"},
		{"name":"station_information","url":"station_information.json"},
		{"name":"station_status","url":"station_status.json"},
		{"name":"free_bike_status","url":"free_bike_status.json"},
		{"name":"vehicle_types","url":"vehicle_types.json"}]}}}`,
	"/system_information.json": `{"data":{"system_id":"demo","name":"Demo Bikes","timezone":"Europe/Paris"}}`,
	"/station_information.json": `{"data":{"stations":[
		{"station_id":"1","name":"Gare","lat":48.8,"lon":2.3,"capacity":20},
		{"station_id":"2","name":"Place","lat":48.9,"lon":2.4}]}}`,
	"/station_status.json": `{"data":{"stations":[
		{"station_id":"1","num_bikes_available":3,"num_bikes_disabled":1,"num_docks_available":16,
		 "is_installed":true,"is_renting":true,"is_returning":true,
		 "vehicle_types_available":[{"vehicle_type_id":"bike","count":2},{"vehicle_type_id":"ebike","count":1}]},
		{"station_id":"2","num_bikes_available":5,"num_docks_available":0,
		 "is_installed":1,"is_renting":0,"is_returning":1}]}}`,
	"/free_bike_status.json": `{"data":{"bikes":[
		{"bike_id":"a","vehicle_type_id":"ebike","is_reserved":false,"is_disabled":false},
		{"bike_id":"b","vehicle_type_id":"ebike","is_reserved":true,"is_disabled":false},
		{"bike_id":"c","vehicle_type_id":"bike","is_reserved":false,"is_disabled":true}]}}`,
	"/vehicle_types.json": `{"data":{"vehicle_types":[
		{"vehicle_type_id":"bike","form_factor":"bicycle","propulsion_type":"human"},
		{"vehicle_type_id":"ebike","form_factor":"bicycle","propulsion_type":"electric_assist"}]}}`,
}

// version3Feeds is the same system in GBFS 3.0. The discovery file has no
// language block, the timestamp is RFC3339, the names are translated lists,
// and the counters use the word "vehicles".
var version3Feeds = map[string]string{
	"/gbfs.json": `{"last_updated":"2021-01-01T00:00:00Z","ttl":60,"version":"3.0","data":{"feeds":[
		{"name":"system_information","url":"system_information.json"},
		{"name":"station_information","url":"station_information.json"},
		{"name":"station_status","url":"station_status.json"},
		{"name":"vehicle_status","url":"vehicle_status.json"},
		{"name":"vehicle_types","url":"vehicle_types.json"}]}}`,
	"/system_information.json": `{"data":{"system_id":"demo","name":[{"text":"Demo Bikes","language":"en"}],"timezone":"Europe/Paris"}}`,
	"/station_information.json": `{"data":{"stations":[
		{"station_id":"1","name":[{"text":"Gare","language":"fr"}],"lat":48.8,"lon":2.3,"capacity":20}]}}`,
	"/station_status.json": `{"data":{"stations":[
		{"station_id":"1","num_vehicles_available":3,"num_vehicles_disabled":1,"num_docks_available":16,
		 "is_installed":true,"is_renting":true,"is_returning":true}]}}`,
	"/vehicle_status.json": `{"data":{"vehicles":[
		{"vehicle_id":"a","vehicle_type_id":"ebike","is_reserved":false,"is_disabled":false}]}}`,
	"/vehicle_types.json": `{"data":{"vehicle_types":[
		{"vehicle_type_id":"ebike","form_factor":"bicycle","propulsion_type":"electric_assist"}]}}`,
}

func fakeSystem(t *testing.T, files map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range files {
		body := body
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		})
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func newCollector(t *testing.T, url string, perType bool) *collector.Collector {
	t.Helper()
	client := gbfs.NewClient(5*time.Second, "gbfs_exporter/test")
	system := collector.System{Name: "demo", URL: url, PerVehicleType: perType}
	return collector.New(client, []collector.System{system}, 5*time.Second, slog.New(slog.DiscardHandler))
}

func TestCollectVersion2(t *testing.T) {
	server := fakeSystem(t, version2Feeds)
	subject := newCollector(t, server.URL+"/gbfs.json", true)

	expected := `
# HELP gbfs_free_vehicles Number of vehicles in the vehicle feed, grouped by type and state.
# TYPE gbfs_free_vehicles gauge
gbfs_free_vehicles{form_factor="bicycle",state="available",system="demo",vehicle_type_id="ebike"} 1
gbfs_free_vehicles{form_factor="bicycle",state="reserved",system="demo",vehicle_type_id="ebike"} 1
gbfs_free_vehicles{form_factor="bicycle",state="disabled",system="demo",vehicle_type_id="bike"} 1
# HELP gbfs_station_installed 1 if the station is on the street, 0 if it is not.
# TYPE gbfs_station_installed gauge
gbfs_station_installed{station_id="1",system="demo"} 1
gbfs_station_installed{station_id="2",system="demo"} 1
# HELP gbfs_station_renting 1 if the station gives out vehicles, 0 if it does not.
# TYPE gbfs_station_renting gauge
gbfs_station_renting{station_id="1",system="demo"} 1
gbfs_station_renting{station_id="2",system="demo"} 0
# HELP gbfs_station_vehicles_available Number of functional vehicles physically at the station. A rider can take one only where gbfs_station_renting is 1.
# TYPE gbfs_station_vehicles_available gauge
gbfs_station_vehicles_available{station_id="1",system="demo"} 3
gbfs_station_vehicles_available{station_id="2",system="demo"} 5
# HELP gbfs_station_vehicles_available_by_type Number of vehicles of one type at the station that a rider can take.
# TYPE gbfs_station_vehicles_available_by_type gauge
gbfs_station_vehicles_available_by_type{form_factor="bicycle",station_id="1",system="demo",vehicle_type_id="bike"} 2
gbfs_station_vehicles_available_by_type{form_factor="bicycle",station_id="1",system="demo",vehicle_type_id="ebike"} 1
# HELP gbfs_station_info Station metadata. The value is always 1.
# TYPE gbfs_station_info gauge
gbfs_station_info{lat="48.8",lon="2.3",name="Gare",station_id="1",system="demo"} 1
gbfs_station_info{lat="48.9",lon="2.4",name="Place",station_id="2",system="demo"} 1
# HELP gbfs_station_capacity Total parking positions of the station: docking points for a physical station, parkable vehicles for a virtual one.
# TYPE gbfs_station_capacity gauge
gbfs_station_capacity{station_id="1",system="demo"} 20
# HELP gbfs_system_free_vehicles Number of vehicles in the vehicle feed.
# TYPE gbfs_system_free_vehicles gauge
gbfs_system_free_vehicles{system="demo"} 3
# HELP gbfs_system_stations Number of distinct stations across station_information and station_status.
# TYPE gbfs_system_stations gauge
gbfs_system_stations{system="demo"} 2
# HELP gbfs_system_vehicles_available Sum of gbfs_station_vehicles_available over every station. Never add it to that metric.
# TYPE gbfs_system_vehicles_available gauge
gbfs_system_vehicles_available{system="demo"} 8
# HELP gbfs_system_vehicles_disabled Sum of gbfs_station_vehicles_disabled over every station. Never add it to that metric.
# TYPE gbfs_system_vehicles_disabled gauge
gbfs_system_vehicles_disabled{system="demo"} 1
# HELP gbfs_system_docks_available Sum of gbfs_station_docks_available over every station. Never add it to that metric.
# TYPE gbfs_system_docks_available gauge
gbfs_system_docks_available{system="demo"} 16
# HELP gbfs_system_info System metadata. The value is always 1.
# TYPE gbfs_system_info gauge
gbfs_system_info{name="Demo Bikes",system="demo",system_id="demo",timezone="Europe/Paris",version="2.3"} 1
# HELP gbfs_up 1 if the exporter read every feed that the system publishes, 0 if any feed or the discovery document failed.
# TYPE gbfs_up gauge
# HELP gbfs_station_docks_available Number of docks at the station that accept a vehicle.
# TYPE gbfs_station_docks_available gauge
gbfs_station_docks_available{station_id="1",system="demo"} 16
gbfs_station_docks_available{station_id="2",system="demo"} 0
# HELP gbfs_station_returning 1 if the station takes back vehicles, 0 if it does not.
# TYPE gbfs_station_returning gauge
gbfs_station_returning{station_id="1",system="demo"} 1
gbfs_station_returning{station_id="2",system="demo"} 1
# HELP gbfs_station_vehicles_disabled Number of vehicles at the station that a rider cannot take.
# TYPE gbfs_station_vehicles_disabled gauge
gbfs_station_vehicles_disabled{station_id="1",system="demo"} 1
gbfs_up{system="demo"} 1
`
	names := []string{
		"gbfs_free_vehicles", "gbfs_station_capacity",
		"gbfs_station_docks_available", "gbfs_station_info",
		"gbfs_station_installed", "gbfs_station_renting",
		"gbfs_station_returning", "gbfs_station_vehicles_available",
		"gbfs_station_vehicles_available_by_type", "gbfs_station_vehicles_disabled",
		"gbfs_system_docks_available", "gbfs_system_free_vehicles",
		"gbfs_system_info", "gbfs_system_stations",
		"gbfs_system_vehicles_available", "gbfs_system_vehicles_disabled",
		"gbfs_up",
	}
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), names...); err != nil {
		t.Error(err)
	}
}

func TestCollectVersion3(t *testing.T) {
	server := fakeSystem(t, version3Feeds)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_station_vehicles_available Number of functional vehicles physically at the station. A rider can take one only where gbfs_station_renting is 1.
# TYPE gbfs_station_vehicles_available gauge
gbfs_station_vehicles_available{station_id="1",system="demo"} 3
# HELP gbfs_station_info Station metadata. The value is always 1.
# TYPE gbfs_station_info gauge
gbfs_station_info{lat="48.8",lon="2.3",name="Gare",station_id="1",system="demo"} 1
# HELP gbfs_system_info System metadata. The value is always 1.
# TYPE gbfs_system_info gauge
gbfs_system_info{name="Demo Bikes",system="demo",system_id="demo",timezone="Europe/Paris",version="3.0"} 1
# HELP gbfs_free_vehicles Number of vehicles in the vehicle feed, grouped by type and state.
# TYPE gbfs_free_vehicles gauge
gbfs_free_vehicles{form_factor="bicycle",state="available",system="demo",vehicle_type_id="ebike"} 1
`
	names := []string{
		"gbfs_station_vehicles_available", "gbfs_station_info",
		"gbfs_system_info", "gbfs_free_vehicles",
	}
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), names...); err != nil {
		t.Error(err)
	}
}

func TestCollectReportsDownSystem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_up 1 if the exporter read every feed that the system publishes, 0 if any feed or the discovery document failed.
# TYPE gbfs_up gauge
gbfs_up{system="demo"} 0
# HELP gbfs_feed_up 1 if the exporter read this feed, 0 if it failed. A feed that the system does not publish gets no series.
# TYPE gbfs_feed_up gauge
gbfs_feed_up{feed="gbfs",system="demo"} 0
`
	// The discovery file failed, so the exporter read no other feed and
	// reports only the discovery feed as down.
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), "gbfs_up", "gbfs_feed_up"); err != nil {
		t.Error(err)
	}
}

// TestCollectKeepsFeedsThatAnswered checks that one broken feed does not hide
// the feeds that answered. The discovery file lists vehicle_types.json, but
// the server does not serve it.
func TestCollectKeepsFeedsThatAnswered(t *testing.T) {
	files := map[string]string{}
	for path, body := range version2Feeds {
		if path == "/vehicle_types.json" {
			continue
		}
		files[path] = body
	}
	server := fakeSystem(t, files)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_up 1 if the exporter read every feed that the system publishes, 0 if any feed or the discovery document failed.
# TYPE gbfs_up gauge
gbfs_up{system="demo"} 0
# HELP gbfs_station_vehicles_available Number of functional vehicles physically at the station. A rider can take one only where gbfs_station_renting is 1.
# TYPE gbfs_station_vehicles_available gauge
gbfs_station_vehicles_available{station_id="1",system="demo"} 3
gbfs_station_vehicles_available{station_id="2",system="demo"} 5
# HELP gbfs_system_stations Number of distinct stations across station_information and station_status.
# TYPE gbfs_system_stations gauge
gbfs_system_stations{system="demo"} 2
`
	names := []string{"gbfs_up", "gbfs_station_vehicles_available", "gbfs_system_stations"}
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), names...); err != nil {
		t.Error(err)
	}
}

// TestCollectWithoutStatusFeed checks a system that publishes its stations but
// no status. The station count must stay right, and the totals must stay away.
func TestCollectWithoutStatusFeed(t *testing.T) {
	server := fakeSystem(t, map[string]string{
		"/gbfs.json": `{"version":"2.3","data":{"en":{"feeds":[
			{"name":"station_information","url":"station_information.json"}]}}}`,
		"/station_information.json": `{"data":{"stations":[
			{"station_id":"1","name":"Gare","lat":48.8,"lon":2.3},
			{"station_id":"2","name":"Place","lat":48.9,"lon":2.4}]}}`,
	})
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_system_stations Number of distinct stations across station_information and station_status.
# TYPE gbfs_system_stations gauge
gbfs_system_stations{system="demo"} 2
`
	names := []string{
		"gbfs_system_stations", "gbfs_system_vehicles_available",
		"gbfs_system_docks_available", "gbfs_system_vehicles_disabled",
	}
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), names...); err != nil {
		t.Error(err)
	}
}

// TestCollectSkipsFlagsThatTheFeedOmits checks that a missing is_renting field
// writes no metric. A 0 would read as "the station is closed".
func TestCollectSkipsFlagsThatTheFeedOmits(t *testing.T) {
	server := fakeSystem(t, map[string]string{
		"/gbfs.json": `{"version":"2.3","data":{"en":{"feeds":[
			{"name":"station_status","url":"station_status.json"}]}}}`,
		"/station_status.json": `{"data":{"stations":[
			{"station_id":"1","num_bikes_available":3,"is_installed":true}]}}`,
	})
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_station_installed 1 if the station is on the street, 0 if it is not.
# TYPE gbfs_station_installed gauge
gbfs_station_installed{station_id="1",system="demo"} 1
`
	names := []string{"gbfs_station_installed", "gbfs_station_renting", "gbfs_station_returning"}
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), names...); err != nil {
		t.Error(err)
	}
}

// TestCollectStopsWithTheRequestContext checks that a cancelled scrape does not
// keep the outbound requests alive.
func TestCollectStopsWithTheRequestContext(t *testing.T) {
	server := fakeSystem(t, version2Feeds)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bound := subject.WithContext(ctx)

	expected := `
# HELP gbfs_up 1 if the exporter read every feed that the system publishes, 0 if any feed or the discovery document failed.
# TYPE gbfs_up gauge
gbfs_up{system="demo"} 0
`
	if err := testutil.CollectAndCompare(bound, strings.NewReader(expected), "gbfs_up"); err != nil {
		t.Error(err)
	}
}

// TestCollectFeedHealth checks the per-feed health and freshness metrics.
//
// The GBFS 2.x fixture names its vehicle feed free_bike_status. The exporter
// must report it under the canonical name vehicle_status, so that one alert
// matches both GBFS versions.
func TestCollectFeedHealth(t *testing.T) {
	server := fakeSystem(t, version2Feeds)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_feed_up 1 if the exporter read this feed, 0 if it failed. A feed that the system does not publish gets no series.
# TYPE gbfs_feed_up gauge
gbfs_feed_up{feed="gbfs",system="demo"} 1
gbfs_feed_up{feed="station_information",system="demo"} 1
gbfs_feed_up{feed="station_status",system="demo"} 1
gbfs_feed_up{feed="system_information",system="demo"} 1
gbfs_feed_up{feed="vehicle_status",system="demo"} 1
gbfs_feed_up{feed="vehicle_types",system="demo"} 1
# HELP gbfs_feed_last_updated_timestamp_seconds Unix time of the last_updated header of the feed. Staleness is time() minus this value.
# TYPE gbfs_feed_last_updated_timestamp_seconds gauge
gbfs_feed_last_updated_timestamp_seconds{feed="gbfs",system="demo"} 1.6094592e+09
# HELP gbfs_feed_ttl_seconds Seconds that the publisher says will pass before the feed changes. 0 means that the feed is always fresh.
# TYPE gbfs_feed_ttl_seconds gauge
gbfs_feed_ttl_seconds{feed="gbfs",system="demo"} 60
`
	names := []string{
		"gbfs_feed_up", "gbfs_feed_last_updated_timestamp_seconds",
		"gbfs_feed_ttl_seconds",
	}
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), names...); err != nil {
		t.Error(err)
	}
}

// TestCollectFeedHealthReportsOneBrokenFeed checks that a single failing feed
// is named. The discovery file lists vehicle_types.json, but the server does
// not serve it.
func TestCollectFeedHealthReportsOneBrokenFeed(t *testing.T) {
	files := map[string]string{}
	for path, body := range version2Feeds {
		if path == "/vehicle_types.json" {
			continue
		}
		files[path] = body
	}
	server := fakeSystem(t, files)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_feed_up 1 if the exporter read this feed, 0 if it failed. A feed that the system does not publish gets no series.
# TYPE gbfs_feed_up gauge
gbfs_feed_up{feed="gbfs",system="demo"} 1
gbfs_feed_up{feed="station_information",system="demo"} 1
gbfs_feed_up{feed="station_status",system="demo"} 1
gbfs_feed_up{feed="system_information",system="demo"} 1
gbfs_feed_up{feed="vehicle_status",system="demo"} 1
gbfs_feed_up{feed="vehicle_types",system="demo"} 0
# HELP gbfs_up 1 if the exporter read every feed that the system publishes, 0 if any feed or the discovery document failed.
# TYPE gbfs_up gauge
gbfs_up{system="demo"} 0
`
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), "gbfs_feed_up", "gbfs_up"); err != nil {
		t.Error(err)
	}
}

// TestCollectFeedDurationIsPresent checks that the duration metric exists for
// every feed. Its value is a measured time, so only presence is checked.
func TestCollectFeedDurationIsPresent(t *testing.T) {
	server := fakeSystem(t, version2Feeds)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	got := testutil.CollectAndCount(subject, "gbfs_feed_duration_seconds")
	if got != 6 {
		t.Fatalf("got %d duration series, want 6", got)
	}
}
