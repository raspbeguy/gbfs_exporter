// Package collector turns GBFS feeds into Prometheus metrics.
package collector

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/raspbeguy/gbfs_exporter/internal/gbfs"
)

// System is one GBFS system that the exporter reads.
// System is one system to read. The caller builds it from the query string
// of the request and from the module that the request names.
type System struct {
	Name           string
	URL            string
	PerVehicleType bool
	// Headers go with every request to this system. Use them for an API key
	// or for a client name that the operator asks for.
	Headers map[string]string
	// MaxConcurrency limits how many feeds of this system the exporter reads
	// at the same time. Set it to 1 for an operator that answers HTTP 429 to
	// parallel requests. A value of 0 means no limit.
	MaxConcurrency int
}

const namespace = "gbfs"

var (
	upDesc = prometheus.NewDesc(
		namespace+"_up",
		"1 if the exporter read every feed that the system publishes, 0 if any feed or the discovery document failed.",
		nil, nil)

	feedUpDesc = prometheus.NewDesc(
		namespace+"_feed_up",
		"1 if the exporter read this feed, 0 if it failed. A feed that the system does not publish gets no series.",
		[]string{"feed"}, nil)

	feedLastUpdatedDesc = prometheus.NewDesc(
		namespace+"_feed_last_updated_timestamp_seconds",
		"Unix time of the last_updated header of the feed. Staleness is time() minus this value.",
		[]string{"feed"}, nil)

	feedTTLDesc = prometheus.NewDesc(
		namespace+"_feed_ttl_seconds",
		"Seconds that the publisher says will pass before the feed changes. 0 means that the feed is always fresh.",
		[]string{"feed"}, nil)

	feedDurationDesc = prometheus.NewDesc(
		namespace+"_feed_duration_seconds",
		"Seconds that the exporter took to read the feed.",
		[]string{"feed"}, nil)

	systemInfoDesc = prometheus.NewDesc(
		namespace+"_system_info",
		"System metadata. The value is always 1.",
		[]string{"system_id", "system_name", "gbfs_version", "timezone"}, nil)

	vehicleTypeInfoDesc = prometheus.NewDesc(
		namespace+"_vehicle_type_info",
		"Vehicle type metadata. The value is always 1.",
		[]string{"vehicle_type_id", "vehicle_type_name", "form_factor", "propulsion_type"}, nil)

	stationInfoDesc = prometheus.NewDesc(
		namespace+"_station_info",
		"Station metadata. The value is always 1.",
		[]string{"station_id", "station_name", "lat", "lon"}, nil)

	stationCapacityDesc = prometheus.NewDesc(
		namespace+"_station_capacity",
		"Total parking positions of the station: docking points for a physical station, parkable vehicles for a virtual one.",
		[]string{"station_id"}, nil)

	stationVehiclesAvailableDesc = prometheus.NewDesc(
		namespace+"_station_vehicles_available",
		"Number of functional vehicles physically at the station. A rider can take one only where gbfs_station_renting is 1.",
		[]string{"station_id"}, nil)

	stationVehiclesDisabledDesc = prometheus.NewDesc(
		namespace+"_station_vehicles_disabled",
		"Number of vehicles at the station that a rider cannot take.",
		[]string{"station_id"}, nil)

	stationDocksAvailableDesc = prometheus.NewDesc(
		namespace+"_station_docks_available",
		"Number of docks at the station that accept a vehicle.",
		[]string{"station_id"}, nil)

	stationDocksDisabledDesc = prometheus.NewDesc(
		namespace+"_station_docks_disabled",
		"Number of docks at the station that do not accept a vehicle.",
		[]string{"station_id"}, nil)

	stationInstalledDesc = prometheus.NewDesc(
		namespace+"_station_installed",
		"1 if the station is on the street, 0 if it is not.",
		[]string{"station_id"}, nil)

	stationRentingDesc = prometheus.NewDesc(
		namespace+"_station_renting",
		"1 if the station gives out vehicles, 0 if it does not.",
		[]string{"station_id"}, nil)

	stationReturningDesc = prometheus.NewDesc(
		namespace+"_station_returning",
		"1 if the station takes back vehicles, 0 if it does not.",
		[]string{"station_id"}, nil)

	stationTypeDesc = prometheus.NewDesc(
		namespace+"_station_type_vehicles_available",
		"Number of vehicles of one type at the station. A breakdown of gbfs_station_vehicles_available; never add the two.",
		[]string{"station_id", "vehicle_type_id", "form_factor", "propulsion_type"}, nil)

	vehiclesDesc = prometheus.NewDesc(
		namespace+"_vehicles",
		"Number of vehicles in the vehicle feed, docked or not. docked is true when the feed gave the vehicle a station_id.",
		[]string{"vehicle_type_id", "form_factor", "propulsion_type", "state", "docked"}, nil)

	systemStationsDesc = prometheus.NewDesc(
		namespace+"_system_stations",
		"Number of distinct stations across station_information and station_status.",
		nil, nil)

	systemVehiclesAvailableDesc = prometheus.NewDesc(
		namespace+"_system_vehicles_available",
		"Sum of gbfs_station_vehicles_available over every station. Never add it to that metric.",
		nil, nil)

	systemVehiclesDisabledDesc = prometheus.NewDesc(
		namespace+"_system_vehicles_disabled",
		"Sum of gbfs_station_vehicles_disabled over every station. Never add it to that metric.",
		nil, nil)

	systemDocksAvailableDesc = prometheus.NewDesc(
		namespace+"_system_docks_available",
		"Sum of gbfs_station_docks_available over every station. Never add it to that metric.",
		nil, nil)

	allDescs = []*prometheus.Desc{
		upDesc, feedUpDesc, feedLastUpdatedDesc, feedTTLDesc, feedDurationDesc,
		systemInfoDesc, vehicleTypeInfoDesc, stationInfoDesc, stationCapacityDesc,
		stationVehiclesAvailableDesc, stationVehiclesDisabledDesc,
		stationDocksAvailableDesc, stationDocksDisabledDesc,
		stationInstalledDesc, stationRentingDesc, stationReturningDesc,
		stationTypeDesc, vehiclesDesc, systemStationsDesc,
		systemVehiclesAvailableDesc, systemVehiclesDisabledDesc,
		systemDocksAvailableDesc,
	}
)

// Collector reads every configured system when Prometheus scrapes it.
type Collector struct {
	client  *gbfs.Client
	system  System
	timeout time.Duration
	log     *slog.Logger
	parent  context.Context
}

// New returns a collector for one system.
//
// One scrape reads one system, so the metrics carry no system label. The
// person who runs Prometheus names the target, the same way that every other
// multi-target exporter works.
func New(client *gbfs.Client, system System, timeout time.Duration, log *slog.Logger) *Collector {
	return &Collector{client: client, system: system, timeout: timeout, log: log}
}

// Describe sends the descriptor of every metric that the collector can emit.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range allDescs {
		ch <- desc
	}
}

// WithContext returns a collector that stops its requests when the context
// ends. Use it to tie one scrape to the life of its HTTP request.
func (c *Collector) WithContext(ctx context.Context) prometheus.Collector {
	copied := *c
	copied.parent = ctx
	return &copied
}

// Collect reads the system and writes its metrics.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	parent := c.parent
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	c.collectSystem(ctx, ch, c.system)
}

func (c *Collector) collectSystem(ctx context.Context, ch chan<- prometheus.Metric, system System) {
	snapshot, err := c.client.Fetch(ctx, system.URL, gbfs.FetchOptions{
		Headers:        system.Headers,
		MaxConcurrency: system.MaxConcurrency,
	})
	if err != nil {
		c.log.Warn("cannot read the system", "system", system.Name, "url", system.URL, "error", err)
		gauge(ch, upDesc, 0)
	} else {
		gauge(ch, upDesc, 1)
	}
	// The client returns the feeds that it did read, even after a failure.
	// One broken feed must not hide the feeds that answered.
	if snapshot == nil {
		return
	}

	c.collectFeeds(ch, system, snapshot)

	if snapshot.SystemID != "" || snapshot.SystemName != "" {
		gauge(ch, systemInfoDesc, 1, snapshot.SystemID,
			snapshot.SystemName, snapshot.Version, snapshot.Timezone)
	}

	for typeID, vehicleType := range snapshot.VehicleTypes {
		if typeID == "" {
			continue
		}
		gauge(ch, vehicleTypeInfoDesc, 1, typeID, string(vehicleType.Name),
			orUnknown(vehicleType.FormFactor), orUnknown(vehicleType.PropulsionType))
	}

	c.collectStations(ch, system, snapshot)
	c.collectVehicles(ch, system, snapshot)
}

// collectFeeds reports the health and the freshness of each feed.
//
// A feed that the auto-discovery file does not list gets no series at all,
// because the system does not publish it.
func (c *Collector) collectFeeds(ch chan<- prometheus.Metric, system System, snapshot *gbfs.Snapshot) {
	for name, feed := range snapshot.Feeds {
		up := float64(0)
		if feed.OK {
			up = 1
		}
		gauge(ch, feedUpDesc, up, name)
		gauge(ch, feedDurationDesc, feed.Duration.Seconds(), name)
		// A zero time writes -62135596800, which reads as a feed that is
		// two thousand years stale. Write nothing instead.
		if !feed.LastUpdated.IsZero() {
			gauge(ch, feedLastUpdatedDesc, float64(feed.LastUpdated.Unix()), name)
		}
		if feed.TTL != nil {
			gauge(ch, feedTTLDesc, float64(*feed.TTL), name)
		}
	}
}

func (c *Collector) collectStations(ch chan<- prometheus.Metric, system System, snapshot *gbfs.Snapshot) {
	seen := make(map[string]bool, len(snapshot.Stations))
	for _, station := range snapshot.Stations {
		if station.StationID == "" || seen[station.StationID] {
			continue
		}
		seen[station.StationID] = true
		gauge(ch, stationInfoDesc, 1, station.StationID,
			string(station.Name), formatCoordinate(station.Lat), formatCoordinate(station.Lon))
		if station.Capacity != nil {
			gauge(ch, stationCapacityDesc, float64(*station.Capacity), station.StationID)
		}
	}

	// Each total needs its own presence flag. A station entry does not mean
	// that the station reported every field, and GBFS lets an operator omit
	// the disabled counters on purpose. A shared flag published a 0 that no
	// feed ever stated.
	var (
		totalAvailable float64
		totalDisabled  float64
		totalDocks     float64
		sawAvailable   bool
		sawDisabled    bool
		sawDocks       bool
	)
	reported := make(map[string]bool, len(snapshot.Status))
	for _, status := range snapshot.Status {
		if status.StationID == "" || reported[status.StationID] {
			continue
		}
		reported[status.StationID] = true

		if value, ok := status.VehiclesAvailable(); ok {
			gauge(ch, stationVehiclesAvailableDesc, value, status.StationID)
			totalAvailable += value
			sawAvailable = true
		}
		if value, ok := status.VehiclesDisabled(); ok {
			gauge(ch, stationVehiclesDisabledDesc, value, status.StationID)
			totalDisabled += value
			sawDisabled = true
		}
		if status.NumDocksAvailable != nil {
			value := float64(*status.NumDocksAvailable)
			gauge(ch, stationDocksAvailableDesc, value, status.StationID)
			totalDocks += value
			sawDocks = true
		}
		if status.NumDocksDisabled != nil {
			gauge(ch, stationDocksDisabledDesc, float64(*status.NumDocksDisabled), status.StationID)
		}
		flag(ch, stationInstalledDesc, status.IsInstalled, status.StationID)
		flag(ch, stationRentingDesc, status.IsRenting, status.StationID)
		flag(ch, stationReturningDesc, status.IsReturning, status.StationID)

		if !system.PerVehicleType {
			continue
		}
		perType := make(map[string]bool, len(status.VehicleTypes))
		for _, entry := range status.VehicleTypes {
			if entry.VehicleTypeID == "" || perType[entry.VehicleTypeID] {
				continue
			}
			perType[entry.VehicleTypeID] = true
			form, propulsion := vehicleTypeOf(snapshot, entry.VehicleTypeID)
			gauge(ch, stationTypeDesc, float64(entry.Count),
				status.StationID, entry.VehicleTypeID, form, propulsion)
		}
	}

	// A system that publishes station_information without station_status still
	// has stations. Count the identifiers of both feeds.
	for identifier := range reported {
		seen[identifier] = true
	}
	if len(seen) > 0 {
		gauge(ch, systemStationsDesc, float64(len(seen)))
	}
	// Each total appears only when at least one station reported that field.
	// Without it the total is unknown, and a 0 would read as an empty system.
	if sawAvailable {
		gauge(ch, systemVehiclesAvailableDesc, totalAvailable)
	}
	if sawDisabled {
		gauge(ch, systemVehiclesDisabledDesc, totalDisabled)
	}
	if sawDocks {
		gauge(ch, systemDocksAvailableDesc, totalDocks)
	}
}

type vehicleKey struct {
	typeID     string
	formFactor string
	propulsion string
	state      string
	docked     string
}

func (c *Collector) collectVehicles(ch chan<- prometheus.Metric, system System, snapshot *gbfs.Snapshot) {
	if snapshot.Vehicles == nil {
		return
	}
	counts := map[vehicleKey]float64{}
	for _, vehicle := range snapshot.Vehicles {
		form, propulsion := vehicleTypeOf(snapshot, vehicle.VehicleTypeID)
		key := vehicleKey{
			typeID:     vehicle.VehicleTypeID,
			formFactor: form,
			propulsion: propulsion,
			state:      vehicleState(vehicle),
			docked:     docked(vehicle),
		}
		counts[key]++
	}
	for key, count := range counts {
		gauge(ch, vehiclesDesc, count, key.typeID, key.formFactor,
			key.propulsion, key.state, key.docked)
	}
}

// docked reports whether the feed placed the vehicle at a station.
//
// Many operators list every docked vehicle in the vehicle feed. Strasbourg
// Vel'hop is one: each of its bikes carries a station_id, and none is free
// floating. A value of false is weaker than "free floating", because GBFS
// requires station_id only when the system also publishes vehicle_types.json,
// and the field did not exist before GBFS 2.1.
func docked(vehicle gbfs.Vehicle) string {
	if vehicle.StationID != "" {
		return "true"
	}
	return "false"
}

// vehicleState reports one state per vehicle. A vehicle that is both disabled
// and reserved counts as disabled.
func vehicleState(vehicle gbfs.Vehicle) string {
	switch {
	case bool(vehicle.IsDisabled):
		return "disabled"
	case bool(vehicle.IsReserved):
		return "reserved"
	default:
		return "available"
	}
}

// vehicleTypeOf returns the form factor and the drive of one vehicle type.
//
// GBFS says that a system without vehicle_types.json carries non-motorized
// bicycles only, so that system reports bicycle and human. A system that
// publishes the feed but did not answer is a different case: the type is
// unknown, and the exporter must not claim a bicycle.
func vehicleTypeOf(snapshot *gbfs.Snapshot, typeID string) (string, string) {
	if vehicleType, ok := snapshot.VehicleTypes[typeID]; ok {
		return orUnknown(vehicleType.FormFactor), orUnknown(vehicleType.PropulsionType)
	}
	if _, published := snapshot.Feeds[gbfs.FeedVehicleTypes]; !published {
		return "bicycle", "human"
	}
	return "unknown", "unknown"
}

func orUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func formatCoordinate(value gbfs.Float) string {
	return strconv.FormatFloat(float64(value), 'f', -1, 64)
}

// flag writes a 0 or 1 metric. A field that the feed does not set writes
// nothing, because 0 means "closed" and that reading is wrong.
func flag(ch chan<- prometheus.Metric, desc *prometheus.Desc, value *gbfs.Bool, labels ...string) {
	if value == nil {
		return
	}
	number := float64(0)
	if *value {
		number = 1
	}
	gauge(ch, desc, number, labels...)
}

func gauge(ch chan<- prometheus.Metric, desc *prometheus.Desc, value float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
}
