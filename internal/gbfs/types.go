// Package gbfs reads General Bikeshare Feed Specification feeds.
//
// The types in this file decode GBFS 2.x and GBFS 3.0. The two versions
// changed the shape of several fields, so the decoders accept both shapes.
package gbfs

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// Timestamp reads a GBFS time value.
//
// GBFS 2.x writes a POSIX integer. GBFS 3.0 writes an RFC3339 string.
type Timestamp struct {
	time.Time
}

// UnmarshalJSON accepts a number, a quoted number, or an RFC3339 string.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(b, &num); err == nil {
		seconds, err := num.Int64()
		if err != nil {
			return fmt.Errorf("gbfs: timestamp %s is not a whole number", b)
		}
		t.Time = time.Unix(seconds, 0).UTC()
		return nil
	}
	var text string
	if err := json.Unmarshal(b, &text); err != nil {
		return fmt.Errorf("gbfs: timestamp %s is neither a number nor a string", b)
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return fmt.Errorf("gbfs: cannot read the timestamp %q", text)
	}
	t.Time = parsed.UTC()
	return nil
}

// Text reads a GBFS name or description.
//
// GBFS 2.x writes a plain string. GBFS 3.0 writes a list of translations.
// The decoder keeps the first translation.
type Text string

// UnmarshalJSON accepts a string or a list of {text, language} objects.
func (s *Text) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var plain string
	if err := json.Unmarshal(b, &plain); err == nil {
		*s = Text(plain)
		return nil
	}
	var translations []struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(b, &translations); err != nil {
		return fmt.Errorf("gbfs: cannot read the localized string %s", b)
	}
	if len(translations) > 0 {
		*s = Text(translations[0].Text)
	}
	return nil
}

// Bool reads a GBFS boolean.
//
// The specification requires true or false. Some feeds send 0 or 1, so the
// decoder accepts both forms.
type Bool bool

// UnmarshalJSON accepts a boolean or a number.
func (v *Bool) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var flag bool
	if err := json.Unmarshal(b, &flag); err == nil {
		*v = Bool(flag)
		return nil
	}
	var number float64
	if err := json.Unmarshal(b, &number); err != nil {
		return fmt.Errorf("gbfs: cannot read the boolean %s", b)
	}
	*v = Bool(number != 0)
	return nil
}

// Float reads a GBFS number that some feeds quote as a string.
type Float float64

// UnmarshalJSON accepts a number or a quoted number.
func (f *Float) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var num json.Number
	if err := json.Unmarshal(b, &num); err != nil {
		return fmt.Errorf("gbfs: cannot read the number %s", b)
	}
	value, err := num.Float64()
	if err != nil {
		return fmt.Errorf("gbfs: cannot read the number %s", b)
	}
	*f = Float(value)
	return nil
}

// Feed is one entry of the auto-discovery file.
type Feed struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// FeedList is the data block of the auto-discovery file.
type FeedList struct {
	Feeds []Feed
}

type feedBlock struct {
	Feeds []Feed `json:"feeds"`
}

// UnmarshalJSON accepts both discovery layouts.
//
// GBFS 3.0 writes {"feeds": [...]}. GBFS 2.x writes one block per language,
// for example {"en": {"feeds": [...]}}. English wins when it exists.
func (l *FeedList) UnmarshalJSON(b []byte) error {
	var direct feedBlock
	if err := json.Unmarshal(b, &direct); err == nil && direct.Feeds != nil {
		l.Feeds = direct.Feeds
		return nil
	}
	var byLanguage map[string]feedBlock
	if err := json.Unmarshal(b, &byLanguage); err != nil {
		return fmt.Errorf("gbfs: cannot read the feed list")
	}
	if block, ok := byLanguage["en"]; ok {
		l.Feeds = block.Feeds
		return nil
	}
	languages := make([]string, 0, len(byLanguage))
	for language := range byLanguage {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	if len(languages) > 0 {
		l.Feeds = byLanguage[languages[0]].Feeds
	}
	return nil
}

// Discovery is the content of gbfs.json.
type Discovery struct {
	LastUpdated Timestamp `json:"last_updated"`
	TTL         int       `json:"ttl"`
	Version     string    `json:"version"`
	Data        FeedList  `json:"data"`
}

// SystemInformation is the content of system_information.json.
type SystemInformation struct {
	Data struct {
		SystemID string `json:"system_id"`
		Name     Text   `json:"name"`
		Timezone string `json:"timezone"`
	} `json:"data"`
}

// Station is one entry of station_information.json.
type Station struct {
	StationID string `json:"station_id"`
	Name      Text   `json:"name"`
	Lat       Float  `json:"lat"`
	Lon       Float  `json:"lon"`
	Capacity  *Float `json:"capacity"`
}

// StationInformation is the content of station_information.json.
type StationInformation struct {
	Data struct {
		Stations []Station `json:"stations"`
	} `json:"data"`
}

// TypeCount is one entry of vehicle_types_available.
type TypeCount struct {
	VehicleTypeID string `json:"vehicle_type_id"`
	Count         Float  `json:"count"`
}

// StationStatus is one entry of station_status.json.
//
// GBFS 2.x names the vehicle counters "bikes". GBFS 3.0 names them
// "vehicles". Both names are read, and the first one that the feed sets wins.
type StationStatus struct {
	StationID            string      `json:"station_id"`
	NumBikesAvailable    *Float      `json:"num_bikes_available"`
	NumVehiclesAvailable *Float      `json:"num_vehicles_available"`
	NumBikesDisabled     *Float      `json:"num_bikes_disabled"`
	NumVehiclesDisabled  *Float      `json:"num_vehicles_disabled"`
	NumDocksAvailable    *Float      `json:"num_docks_available"`
	NumDocksDisabled     *Float      `json:"num_docks_disabled"`
	IsInstalled          Bool        `json:"is_installed"`
	IsRenting            Bool        `json:"is_renting"`
	IsReturning          Bool        `json:"is_returning"`
	VehicleTypes         []TypeCount `json:"vehicle_types_available"`
}

// VehiclesAvailable returns the number of vehicles that riders can take.
func (s StationStatus) VehiclesAvailable() (float64, bool) {
	return firstSet(s.NumVehiclesAvailable, s.NumBikesAvailable)
}

// VehiclesDisabled returns the number of vehicles that riders cannot take.
func (s StationStatus) VehiclesDisabled() (float64, bool) {
	return firstSet(s.NumVehiclesDisabled, s.NumBikesDisabled)
}

func firstSet(values ...*Float) (float64, bool) {
	for _, value := range values {
		if value != nil {
			return float64(*value), true
		}
	}
	return 0, false
}

// StationStatusFeed is the content of station_status.json.
type StationStatusFeed struct {
	Data struct {
		Stations []StationStatus `json:"stations"`
	} `json:"data"`
}

// Vehicle is one free-floating vehicle.
type Vehicle struct {
	BikeID        string `json:"bike_id"`
	VehicleID     string `json:"vehicle_id"`
	VehicleTypeID string `json:"vehicle_type_id"`
	StationID     string `json:"station_id"`
	IsReserved    Bool   `json:"is_reserved"`
	IsDisabled    Bool   `json:"is_disabled"`
}

// VehicleStatusFeed is the content of vehicle_status.json in GBFS 3.0 and of
// free_bike_status.json in GBFS 2.x.
type VehicleStatusFeed struct {
	Data struct {
		Bikes    []Vehicle `json:"bikes"`
		Vehicles []Vehicle `json:"vehicles"`
	} `json:"data"`
}

// All returns the vehicles of the feed under either field name.
func (f VehicleStatusFeed) All() []Vehicle {
	if len(f.Data.Vehicles) > 0 {
		return f.Data.Vehicles
	}
	return f.Data.Bikes
}

// VehicleType is one entry of vehicle_types.json.
type VehicleType struct {
	VehicleTypeID  string `json:"vehicle_type_id"`
	FormFactor     string `json:"form_factor"`
	PropulsionType string `json:"propulsion_type"`
	Name           Text   `json:"name"`
}

// VehicleTypesFeed is the content of vehicle_types.json.
type VehicleTypesFeed struct {
	Data struct {
		VehicleTypes []VehicleType `json:"vehicle_types"`
	} `json:"data"`
}
