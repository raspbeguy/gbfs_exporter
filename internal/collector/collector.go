// Package collector turns GBFS feeds into Prometheus metrics.
package collector

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/raspbeguy/gbfs_exporter/internal/gbfs"
)

// System is one GBFS system that the exporter reads.
type System struct {
	Name           string `yaml:"name"`
	URL            string `yaml:"url"`
	PerVehicleType bool   `yaml:"per_vehicle_type"`
	// Headers go with every request to this system. Use them for an API key
	// or for a client name that the operator asks for.
	Headers map[string]string `yaml:"headers"`
	// MaxConcurrency limits how many feeds of this system the exporter reads
	// at the same time. Set it to 1 for an operator that answers HTTP 429 to
	// parallel requests. A value of 0 means no limit.
	MaxConcurrency int `yaml:"max_concurrency"`
}

const namespace = "gbfs"

var (
	upDesc = prometheus.NewDesc(
		namespace+"_up",
		"1 if the exporter read every feed of the system, 0 if one feed failed.",
		[]string{"system"}, nil)

	systemInfoDesc = prometheus.NewDesc(
		namespace+"_system_info",
		"System metadata. The value is always 1.",
		[]string{"system", "system_id", "name", "version", "timezone"}, nil)

	stationInfoDesc = prometheus.NewDesc(
		namespace+"_station_info",
		"Station metadata. The value is always 1.",
		[]string{"system", "station_id", "name", "lat", "lon"}, nil)

	stationCapacityDesc = prometheus.NewDesc(
		namespace+"_station_capacity",
		"Number of docks that the station has.",
		[]string{"system", "station_id"}, nil)

	stationVehiclesAvailableDesc = prometheus.NewDesc(
		namespace+"_station_vehicles_available",
		"Number of vehicles at the station that a rider can take.",
		[]string{"system", "station_id"}, nil)

	stationVehiclesDisabledDesc = prometheus.NewDesc(
		namespace+"_station_vehicles_disabled",
		"Number of vehicles at the station that a rider cannot take.",
		[]string{"system", "station_id"}, nil)

	stationDocksAvailableDesc = prometheus.NewDesc(
		namespace+"_station_docks_available",
		"Number of docks at the station that accept a vehicle.",
		[]string{"system", "station_id"}, nil)

	stationDocksDisabledDesc = prometheus.NewDesc(
		namespace+"_station_docks_disabled",
		"Number of docks at the station that do not accept a vehicle.",
		[]string{"system", "station_id"}, nil)

	stationInstalledDesc = prometheus.NewDesc(
		namespace+"_station_installed",
		"1 if the station is on the street, 0 if it is not.",
		[]string{"system", "station_id"}, nil)

	stationRentingDesc = prometheus.NewDesc(
		namespace+"_station_renting",
		"1 if the station gives out vehicles, 0 if it does not.",
		[]string{"system", "station_id"}, nil)

	stationReturningDesc = prometheus.NewDesc(
		namespace+"_station_returning",
		"1 if the station takes back vehicles, 0 if it does not.",
		[]string{"system", "station_id"}, nil)

	stationTypeDesc = prometheus.NewDesc(
		namespace+"_station_vehicles_available_by_type",
		"Number of vehicles of one type at the station that a rider can take.",
		[]string{"system", "station_id", "vehicle_type_id", "form_factor"}, nil)

	freeVehiclesDesc = prometheus.NewDesc(
		namespace+"_free_vehicles",
		"Number of vehicles in the vehicle feed, grouped by type and state.",
		[]string{"system", "vehicle_type_id", "form_factor", "state"}, nil)

	systemStationsDesc = prometheus.NewDesc(
		namespace+"_system_stations",
		"Number of stations in the station feed.",
		[]string{"system"}, nil)

	systemVehiclesAvailableDesc = prometheus.NewDesc(
		namespace+"_system_vehicles_available",
		"Number of vehicles at all stations that a rider can take.",
		[]string{"system"}, nil)

	systemVehiclesDisabledDesc = prometheus.NewDesc(
		namespace+"_system_vehicles_disabled",
		"Number of vehicles at all stations that a rider cannot take.",
		[]string{"system"}, nil)

	systemDocksAvailableDesc = prometheus.NewDesc(
		namespace+"_system_docks_available",
		"Number of docks at all stations that accept a vehicle.",
		[]string{"system"}, nil)

	systemFreeVehiclesDesc = prometheus.NewDesc(
		namespace+"_system_free_vehicles",
		"Number of vehicles in the vehicle feed.",
		[]string{"system"}, nil)

	allDescs = []*prometheus.Desc{
		upDesc, systemInfoDesc, stationInfoDesc, stationCapacityDesc,
		stationVehiclesAvailableDesc, stationVehiclesDisabledDesc,
		stationDocksAvailableDesc, stationDocksDisabledDesc,
		stationInstalledDesc, stationRentingDesc, stationReturningDesc,
		stationTypeDesc, freeVehiclesDesc, systemStationsDesc,
		systemVehiclesAvailableDesc, systemVehiclesDisabledDesc,
		systemDocksAvailableDesc, systemFreeVehiclesDesc,
	}
)

// Collector reads every configured system when Prometheus scrapes it.
type Collector struct {
	client  *gbfs.Client
	systems []System
	timeout time.Duration
	log     *slog.Logger
	parent  context.Context
}

// New returns a collector for the given systems.
func New(client *gbfs.Client, systems []System, timeout time.Duration, log *slog.Logger) *Collector {
	return &Collector{client: client, systems: systems, timeout: timeout, log: log}
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

// Collect reads all systems at the same time and writes their metrics.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	parent := c.parent
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()

	var group sync.WaitGroup
	for _, system := range c.systems {
		group.Add(1)
		go func(system System) {
			defer group.Done()
			c.collectSystem(ctx, ch, system)
		}(system)
	}
	group.Wait()
}

func (c *Collector) collectSystem(ctx context.Context, ch chan<- prometheus.Metric, system System) {
	snapshot, err := c.client.Fetch(ctx, system.URL, gbfs.FetchOptions{
		Headers:        system.Headers,
		MaxConcurrency: system.MaxConcurrency,
	})
	if err != nil {
		c.log.Warn("cannot read the system", "system", system.Name, "error", err)
		gauge(ch, upDesc, 0, system.Name)
	} else {
		gauge(ch, upDesc, 1, system.Name)
	}
	// The client returns the feeds that it did read, even after a failure.
	// One broken feed must not hide the feeds that answered.
	if snapshot == nil {
		return
	}

	if snapshot.SystemID != "" || snapshot.SystemName != "" {
		gauge(ch, systemInfoDesc, 1, system.Name, snapshot.SystemID,
			snapshot.SystemName, snapshot.Version, snapshot.Timezone)
	}

	c.collectStations(ch, system, snapshot)
	c.collectVehicles(ch, system, snapshot)
}

func (c *Collector) collectStations(ch chan<- prometheus.Metric, system System, snapshot *gbfs.Snapshot) {
	seen := make(map[string]bool, len(snapshot.Stations))
	for _, station := range snapshot.Stations {
		if station.StationID == "" || seen[station.StationID] {
			continue
		}
		seen[station.StationID] = true
		gauge(ch, stationInfoDesc, 1, system.Name, station.StationID,
			string(station.Name), formatCoordinate(station.Lat), formatCoordinate(station.Lon))
		if station.Capacity != nil {
			gauge(ch, stationCapacityDesc, float64(*station.Capacity), system.Name, station.StationID)
		}
	}

	var (
		totalAvailable float64
		totalDisabled  float64
		totalDocks     float64
		stationCount   float64
	)
	reported := make(map[string]bool, len(snapshot.Status))
	for _, status := range snapshot.Status {
		if status.StationID == "" || reported[status.StationID] {
			continue
		}
		reported[status.StationID] = true
		stationCount++

		if value, ok := status.VehiclesAvailable(); ok {
			gauge(ch, stationVehiclesAvailableDesc, value, system.Name, status.StationID)
			totalAvailable += value
		}
		if value, ok := status.VehiclesDisabled(); ok {
			gauge(ch, stationVehiclesDisabledDesc, value, system.Name, status.StationID)
			totalDisabled += value
		}
		if status.NumDocksAvailable != nil {
			value := float64(*status.NumDocksAvailable)
			gauge(ch, stationDocksAvailableDesc, value, system.Name, status.StationID)
			totalDocks += value
		}
		if status.NumDocksDisabled != nil {
			gauge(ch, stationDocksDisabledDesc, float64(*status.NumDocksDisabled), system.Name, status.StationID)
		}
		flag(ch, stationInstalledDesc, status.IsInstalled, system.Name, status.StationID)
		flag(ch, stationRentingDesc, status.IsRenting, system.Name, status.StationID)
		flag(ch, stationReturningDesc, status.IsReturning, system.Name, status.StationID)

		if !system.PerVehicleType {
			continue
		}
		perType := make(map[string]bool, len(status.VehicleTypes))
		for _, entry := range status.VehicleTypes {
			if entry.VehicleTypeID == "" || perType[entry.VehicleTypeID] {
				continue
			}
			perType[entry.VehicleTypeID] = true
			gauge(ch, stationTypeDesc, float64(entry.Count), system.Name,
				status.StationID, entry.VehicleTypeID, formFactor(snapshot, entry.VehicleTypeID))
		}
	}

	// A system that publishes station_information without station_status still
	// has stations. Count the identifiers of both feeds.
	for identifier := range reported {
		seen[identifier] = true
	}
	if len(seen) > 0 {
		gauge(ch, systemStationsDesc, float64(len(seen)), system.Name)
	}
	// The totals come from the status feed. Without that feed the totals are
	// unknown, and a 0 would read as an empty system.
	if stationCount > 0 {
		gauge(ch, systemVehiclesAvailableDesc, totalAvailable, system.Name)
		gauge(ch, systemVehiclesDisabledDesc, totalDisabled, system.Name)
		gauge(ch, systemDocksAvailableDesc, totalDocks, system.Name)
	}
}

type vehicleKey struct {
	typeID     string
	formFactor string
	state      string
}

func (c *Collector) collectVehicles(ch chan<- prometheus.Metric, system System, snapshot *gbfs.Snapshot) {
	if snapshot.Vehicles == nil {
		return
	}
	counts := map[vehicleKey]float64{}
	for _, vehicle := range snapshot.Vehicles {
		key := vehicleKey{
			typeID:     vehicle.VehicleTypeID,
			formFactor: formFactor(snapshot, vehicle.VehicleTypeID),
			state:      vehicleState(vehicle),
		}
		counts[key]++
	}
	for key, count := range counts {
		gauge(ch, freeVehiclesDesc, count, system.Name, key.typeID, key.formFactor, key.state)
	}
	gauge(ch, systemFreeVehiclesDesc, float64(len(snapshot.Vehicles)), system.Name)
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

func formFactor(snapshot *gbfs.Snapshot, typeID string) string {
	if vehicleType, ok := snapshot.VehicleTypes[typeID]; ok && vehicleType.FormFactor != "" {
		return vehicleType.FormFactor
	}
	return "unknown"
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
