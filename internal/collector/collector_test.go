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
	// Bike "d" sits at station 1. Many operators list every docked vehicle in
	// this feed, so the exporter must not call it free floating.
	"/free_bike_status.json": `{"data":{"bikes":[
		{"bike_id":"a","vehicle_type_id":"ebike","is_reserved":false,"is_disabled":false},
		{"bike_id":"b","vehicle_type_id":"ebike","is_reserved":true,"is_disabled":false},
		{"bike_id":"c","vehicle_type_id":"bike","is_reserved":false,"is_disabled":true},
		{"bike_id":"d","vehicle_type_id":"bike","station_id":"1","is_reserved":false,"is_disabled":false}]}}`,
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
# HELP gbfs_vehicles Number of vehicles in the vehicle feed, docked or not. docked is true when the feed gave the vehicle a station_id.
# TYPE gbfs_vehicles gauge
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="electric_assist",state="available",system="demo",vehicle_type_id="ebike"} 1
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="electric_assist",state="reserved",system="demo",vehicle_type_id="ebike"} 1
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="human",state="disabled",system="demo",vehicle_type_id="bike"} 1
gbfs_vehicles{docked="true",form_factor="bicycle",propulsion_type="human",state="available",system="demo",vehicle_type_id="bike"} 1
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
# HELP gbfs_station_type_vehicles_available Number of vehicles of one type at the station. A breakdown of gbfs_station_vehicles_available; never add the two.
# TYPE gbfs_station_type_vehicles_available gauge
gbfs_station_type_vehicles_available{form_factor="bicycle",propulsion_type="human",station_id="1",system="demo",vehicle_type_id="bike"} 2
gbfs_station_type_vehicles_available{form_factor="bicycle",propulsion_type="electric_assist",station_id="1",system="demo",vehicle_type_id="ebike"} 1
# HELP gbfs_station_info Station metadata. The value is always 1.
# TYPE gbfs_station_info gauge
gbfs_station_info{lat="48.8",lon="2.3",station_id="1",station_name="Gare",system="demo"} 1
gbfs_station_info{lat="48.9",lon="2.4",station_id="2",station_name="Place",system="demo"} 1
# HELP gbfs_station_capacity Total parking positions of the station: docking points for a physical station, parkable vehicles for a virtual one.
# TYPE gbfs_station_capacity gauge
gbfs_station_capacity{station_id="1",system="demo"} 20
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
gbfs_system_info{gbfs_version="2.3",system="demo",system_id="demo",system_name="Demo Bikes",timezone="Europe/Paris"} 1
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
		"gbfs_vehicles", "gbfs_station_capacity",
		"gbfs_station_docks_available", "gbfs_station_info",
		"gbfs_station_installed", "gbfs_station_renting",
		"gbfs_station_returning", "gbfs_station_vehicles_available",
		"gbfs_station_type_vehicles_available", "gbfs_station_vehicles_disabled",
		"gbfs_system_docks_available",
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
gbfs_station_info{lat="48.8",lon="2.3",station_id="1",station_name="Gare",system="demo"} 1
# HELP gbfs_system_info System metadata. The value is always 1.
# TYPE gbfs_system_info gauge
gbfs_system_info{gbfs_version="3.0",system="demo",system_id="demo",system_name="Demo Bikes",timezone="Europe/Paris"} 1
# HELP gbfs_vehicles Number of vehicles in the vehicle feed, docked or not. docked is true when the feed gave the vehicle a station_id.
# TYPE gbfs_vehicles gauge
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="electric_assist",state="available",system="demo",vehicle_type_id="ebike"} 1
`
	names := []string{
		"gbfs_station_vehicles_available", "gbfs_station_info",
		"gbfs_system_info", "gbfs_vehicles",
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

// TestCollectVehicleTypeInfo checks the vehicle type metadata metric. The
// form factor of both fixture types is bicycle, so only propulsion_type tells
// an electric bike from a classic one.
func TestCollectVehicleTypeInfo(t *testing.T) {
	server := fakeSystem(t, version2Feeds)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_vehicle_type_info Vehicle type metadata. The value is always 1.
# TYPE gbfs_vehicle_type_info gauge
gbfs_vehicle_type_info{form_factor="bicycle",propulsion_type="human",system="demo",vehicle_type_id="bike",vehicle_type_name=""} 1
gbfs_vehicle_type_info{form_factor="bicycle",propulsion_type="electric_assist",system="demo",vehicle_type_id="ebike",vehicle_type_name=""} 1
`
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), "gbfs_vehicle_type_info"); err != nil {
		t.Error(err)
	}
}

// TestVehicleTypeFallbacks checks the two cases where a vehicle type does not
// resolve. GBFS says a system without vehicle_types.json carries
// non-motorized bicycles. A system that publishes the feed but did not answer
// is unknown, and the exporter must not claim a bicycle.
func TestVehicleTypeFallbacks(t *testing.T) {
	t.Run("the system does not publish the feed", func(t *testing.T) {
		files := map[string]string{}
		for path, body := range version2Feeds {
			if path == "/vehicle_types.json" {
				continue
			}
			files[path] = body
		}
		// Drop the feed from the discovery file as well.
		files["/gbfs.json"] = strings.Replace(version2Feeds["/gbfs.json"],
			`,
		{"name":"vehicle_types","url":"vehicle_types.json"}`, "", 1)
		server := fakeSystem(t, files)
		subject := newCollector(t, server.URL+"/gbfs.json", false)

		expected := `
# HELP gbfs_vehicles Number of vehicles in the vehicle feed, docked or not. docked is true when the feed gave the vehicle a station_id.
# TYPE gbfs_vehicles gauge
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="human",state="available",system="demo",vehicle_type_id="ebike"} 1
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="human",state="reserved",system="demo",vehicle_type_id="ebike"} 1
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="human",state="disabled",system="demo",vehicle_type_id="bike"} 1
gbfs_vehicles{docked="true",form_factor="bicycle",propulsion_type="human",state="available",system="demo",vehicle_type_id="bike"} 1
`
		if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), "gbfs_vehicles"); err != nil {
			t.Error(err)
		}
	})

	t.Run("the feed is published but failed", func(t *testing.T) {
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
# HELP gbfs_vehicles Number of vehicles in the vehicle feed, docked or not. docked is true when the feed gave the vehicle a station_id.
# TYPE gbfs_vehicles gauge
gbfs_vehicles{docked="false",form_factor="unknown",propulsion_type="unknown",state="available",system="demo",vehicle_type_id="ebike"} 1
gbfs_vehicles{docked="false",form_factor="unknown",propulsion_type="unknown",state="reserved",system="demo",vehicle_type_id="ebike"} 1
gbfs_vehicles{docked="false",form_factor="unknown",propulsion_type="unknown",state="disabled",system="demo",vehicle_type_id="bike"} 1
gbfs_vehicles{docked="true",form_factor="unknown",propulsion_type="unknown",state="available",system="demo",vehicle_type_id="bike"} 1
`
		if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), "gbfs_vehicles"); err != nil {
			t.Error(err)
		}
	})
}

// TestCollectSplitsDockedVehicles checks the docked label.
//
// Many operators list every docked vehicle in the vehicle feed. Without the
// label, those vehicles counted as free floating and were counted a second
// time by the station metrics.
func TestCollectSplitsDockedVehicles(t *testing.T) {
	server := fakeSystem(t, version2Feeds)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_vehicles Number of vehicles in the vehicle feed, docked or not. docked is true when the feed gave the vehicle a station_id.
# TYPE gbfs_vehicles gauge
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="electric_assist",state="available",system="demo",vehicle_type_id="ebike"} 1
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="electric_assist",state="reserved",system="demo",vehicle_type_id="ebike"} 1
gbfs_vehicles{docked="false",form_factor="bicycle",propulsion_type="human",state="disabled",system="demo",vehicle_type_id="bike"} 1
gbfs_vehicles{docked="true",form_factor="bicycle",propulsion_type="human",state="available",system="demo",vehicle_type_id="bike"} 1
`
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), "gbfs_vehicles"); err != nil {
		t.Error(err)
	}
}

// TestSystemTotalsAbsentForAnUnreportedField checks that a total appears only
// when at least one station reported the field it sums.
//
// GBFS lets an operator omit the disabled counters on purpose. Strasbourg
// Vel'hop omits them at all 40 stations, and the exporter used to answer 0,
// which reads as "no broken bikes" instead of "not published".
func TestSystemTotalsAbsentForAnUnreportedField(t *testing.T) {
	files := map[string]string{}
	for path, body := range version2Feeds {
		files[path] = body
	}
	// No station reports a disabled count or a dock count.
	files["/station_status.json"] = `{"data":{"stations":[
		{"station_id":"1","num_bikes_available":3},
		{"station_id":"2","num_bikes_available":5}]}}`
	server := fakeSystem(t, files)
	subject := newCollector(t, server.URL+"/gbfs.json", false)

	expected := `
# HELP gbfs_system_vehicles_available Sum of gbfs_station_vehicles_available over every station. Never add it to that metric.
# TYPE gbfs_system_vehicles_available gauge
gbfs_system_vehicles_available{system="demo"} 8
`
	names := []string{
		"gbfs_system_vehicles_available", "gbfs_system_vehicles_disabled",
		"gbfs_system_docks_available",
	}
	if err := testutil.CollectAndCompare(subject, strings.NewReader(expected), names...); err != nil {
		t.Error(err)
	}
	for _, absent := range []string{"gbfs_system_vehicles_disabled", "gbfs_system_docks_available"} {
		if got := testutil.CollectAndCount(subject, absent); got != 0 {
			t.Errorf("%s has %d series, want none", absent, got)
		}
	}
}
