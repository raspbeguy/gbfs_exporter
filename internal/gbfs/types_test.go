package gbfs

import (
	"encoding/json"
	"testing"
	"time"
)

func TestTimestampAcceptsBothVersions(t *testing.T) {
	cases := map[string]string{
		`1609459200`:                  "2021-01-01T00:00:00Z",
		`"1609459200"`:                "2021-01-01T00:00:00Z",
		`"2021-01-01T00:00:00Z"`:      "2021-01-01T00:00:00Z",
		`"2021-01-01T01:00:00+01:00"`: "2021-01-01T00:00:00Z",
	}
	for input, want := range cases {
		var got Timestamp
		if err := json.Unmarshal([]byte(input), &got); err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if got.Format(time.RFC3339) != want {
			t.Errorf("%s: got %s, want %s", input, got.Format(time.RFC3339), want)
		}
	}
	var bad Timestamp
	if err := json.Unmarshal([]byte(`"not a time"`), &bad); err == nil {
		t.Error("a bad timestamp must return an error")
	}
}

func TestTextAcceptsBothVersions(t *testing.T) {
	var plain Text
	if err := json.Unmarshal([]byte(`"Gare du Nord"`), &plain); err != nil {
		t.Fatal(err)
	}
	if plain != "Gare du Nord" {
		t.Errorf("got %q", plain)
	}

	var localized Text
	input := `[{"text":"Gare du Nord","language":"fr"},{"text":"North Station","language":"en"}]`
	if err := json.Unmarshal([]byte(input), &localized); err != nil {
		t.Fatal(err)
	}
	if localized != "Gare du Nord" {
		t.Errorf("got %q", localized)
	}
}

func TestBoolAcceptsNumbers(t *testing.T) {
	cases := map[string]bool{`true`: true, `false`: false, `1`: true, `0`: false}
	for input, want := range cases {
		var got Bool
		if err := json.Unmarshal([]byte(input), &got); err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if bool(got) != want {
			t.Errorf("%s: got %v, want %v", input, got, want)
		}
	}
}

func TestFeedListAcceptsBothLayouts(t *testing.T) {
	var version3 FeedList
	input3 := `{"feeds":[{"name":"station_status","url":"https://example.test/s.json"}]}`
	if err := json.Unmarshal([]byte(input3), &version3); err != nil {
		t.Fatal(err)
	}
	if len(version3.Feeds) != 1 || version3.Feeds[0].Name != "station_status" {
		t.Errorf("got %+v", version3.Feeds)
	}

	var version2 FeedList
	input2 := `{"fr":{"feeds":[{"name":"a","url":"u"}]},"en":{"feeds":[{"name":"station_status","url":"u"}]}}`
	if err := json.Unmarshal([]byte(input2), &version2); err != nil {
		t.Fatal(err)
	}
	if len(version2.Feeds) != 1 || version2.Feeds[0].Name != "station_status" {
		t.Errorf("English must win, got %+v", version2.Feeds)
	}
}

func TestStationStatusPicksTheVersionField(t *testing.T) {
	var version2 StationStatus
	if err := json.Unmarshal([]byte(`{"num_bikes_available":4}`), &version2); err != nil {
		t.Fatal(err)
	}
	if value, ok := version2.VehiclesAvailable(); !ok || value != 4 {
		t.Errorf("got %v %v", value, ok)
	}

	var version3 StationStatus
	if err := json.Unmarshal([]byte(`{"num_vehicles_available":7}`), &version3); err != nil {
		t.Fatal(err)
	}
	if value, ok := version3.VehiclesAvailable(); !ok || value != 7 {
		t.Errorf("got %v %v", value, ok)
	}

	var empty StationStatus
	if _, ok := empty.VehiclesAvailable(); ok {
		t.Error("a missing counter must report that it is not set")
	}
}

func TestFeedIndexNormalizesNamesAndURLs(t *testing.T) {
	feeds := []Feed{
		{Name: "station_status.json", URL: "station_status.json"},
		{Name: " vehicle_types ", URL: "https://other.test/v.json"},
	}
	index, err := feedIndex("https://example.test/gbfs/gbfs.json", feeds)
	if err != nil {
		t.Fatal(err)
	}
	if index["station_status"] != "https://example.test/gbfs/station_status.json" {
		t.Errorf("got %q", index["station_status"])
	}
	if index["vehicle_types"] != "https://other.test/v.json" {
		t.Errorf("got %q", index["vehicle_types"])
	}
}
