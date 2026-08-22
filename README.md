# gbfs_exporter

[![CI](https://github.com/raspbeguy/gbfs_exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/raspbeguy/gbfs_exporter/actions/workflows/ci.yml)

A Prometheus exporter for GBFS feeds.

GBFS is the General Bikeshare Feed Specification. Bike share and scooter share
operators publish it. The exporter turns the feeds into Prometheus metrics.

The exporter reads GBFS 2.x and GBFS 3.0. It finds the version from the
auto-discovery file and adapts to the field names of that version.

The exporter follows the multi-target pattern of `blackbox_exporter`. One
scrape of `/probe` reads one system, and the query string names it. Prometheus
holds the list of systems, so a new system needs no restart of the exporter.
`/metrics` reports on the exporter itself.

## Warning

Do not set a scrape interval below the ttl of the feeds. The exporter reads the
operator API on every scrape. A short interval can hit a rate limit, and the
operator can block your address.

## What the exporter reads

The exporter reads these feeds of each system:

- `station_information.json` for the name, the position, and the capacity
- `station_status.json` for the vehicles and the docks of each station
- `vehicle_status.json`, or `free_bike_status.json` in GBFS 2.x, for the
  free-floating vehicles
- `vehicle_types.json` for the form factor of each vehicle type
- `system_information.json` for the name and the time zone of the system

A missing feed is not an error. A system with docks has no vehicle feed. A
free-floating system has no station feed.

## Install

### Container image

Each release pushes an image to the GitHub container registry. The tag points
to a manifest list, so Docker pulls the `linux/amd64` image or the
`linux/arm64` image to match your machine.

```
docker pull ghcr.io/raspbeguy/gbfs_exporter:latest
```

The image holds a default configuration that lets it start, and that
configuration sets no `allowed_hosts`. Mount your own file for a real
deployment:

```
docker run -p 9718:9718 \
  -v "$PWD/config.yml:/etc/gbfs_exporter/config.yml:ro" \
  ghcr.io/raspbeguy/gbfs_exporter:latest
```

The container runs as `nonroot`, uid 65532, so the file must be readable by
that user.

### Binary

Each release carries an archive for linux, macOS, and FreeBSD, on amd64 and on
arm64. Download the archive of your platform from the releases page, then
verify it against `checksums.txt`:

```
sha256sum -c checksums.txt --ignore-missing
```

The archive holds the binary, `README.md`, `LICENSE`, the example
configuration, and the `grafana/` folder.

## Build

Go 1.25 or higher is necessary.

```
make build
```

To build a container image, run this command:

```
docker build -t gbfs_exporter .
```

## Configure

The exporter follows the multi-target pattern of `blackbox_exporter`. The
configuration file holds no target. Prometheus gives the target in the query
string of each scrape.

1. Copy the example file.

   ```
   cp config.example.yml config.yml
   ```

2. Set `allowed_hosts` to the hosts of your operators. Read the warning below.

3. Add a module for each operator that needs a header, a lower concurrency, or
   the per-type metric.

The exporter refuses a configuration file that holds an unknown key. This
catches a typing mistake at start.

### Warning

The exporter fetches the URL that the caller gives. A caller who reaches the
exporter port can therefore reach any address that the exporter can reach, and
can read back whether that address answers. An empty `allowed_hosts` accepts
every host. Set the list, or keep the port closed to untrusted callers.

### Settings

| Setting | Default | Meaning |
| --- | --- | --- |
| `listen_address` | `:9718` | Address that the exporter listens on. |
| `timeout` | `30s` | Budget for one scrape, across every feed of the system. |
| `request_timeout` | `10s` | Budget for one feed. It must not exceed `timeout`. |
| `user_agent` | the version | User agent that the exporter sends. |
| `allowed_hosts` | empty | Hosts that the exporter accepts as a target. Empty accepts every host. |
| `max_in_flight` | `4` | Scrapes that run at the same time. A scrape above the limit gets HTTP 503. Keep it above the number of targets. |
| `modules` | empty | Named settings for an operator. |

### Modules

A module holds the settings that a query string must not carry, such as an API
key. A scrape names one with the `module` parameter. A scrape without the
parameter uses the module called `default`, and the values below if the file
holds no such module.

| Setting | Default | Meaning |
| --- | --- | --- |
| `per_vehicle_type` | `false` | Add one series per station and per vehicle type. |
| `max_concurrency` | `0` | Feeds to read at the same time. 0 means no limit. |
| `request_timeout` | the file value | Budget for one feed of this operator. It must not exceed `timeout`. |
| `reuse_connections` | `true` | Keep one connection alive across the feeds of this operator. |
| `headers` | empty | Headers to add to every request. |

Raise `request_timeout` in a module for an operator that is slow. A module keeps
the longer budget to that operator, so a hung feed elsewhere still gives up
quickly.

### Do not limit concurrency without measuring

`max_concurrency` makes the exporter read the feeds of a system one after
another, so the scrape takes as long as their total rather than as long as their
slowest. Set it only for an operator that answers HTTP 429 to parallel
requests, and measure first. Entur serves all four Trondheim feeds together in
0.28 seconds and one after another in 0.80 seconds.

Keep `max_in_flight` above the number of targets you scrape. A scrape above the
limit gets HTTP 503, and Prometheus reads that as a target that is down, so a
run of targets landing together would report a healthy system as broken.

### An operator that holds a reused connection

Some operators serve the first request of a connection at once and then hold
every later one. Strasbourg Citiz answers the first request in 170 milliseconds
and each reuse in 5 seconds, whichever HTTP version is in use. The exporter
reads the feeds of one system together, so all but the first would wait, and a
scrape that should take half a second takes five.

Set `reuse_connections: false` for such an operator. Each feed then opens its
own connection, which costs one handshake and removes the wait. Citiz drops from
about five seconds a scrape to under one.

The exporter also drops an idle connection after 30 seconds, so at the usual
scrape interval of a minute the pool is empty when the next scrape starts, and
no connection carries the penalty forward.

That is a floor and not a guarantee. A feed with a `ttl` of 15 seconds invites a
shorter interval, and at that interval the pool still holds a connection between
scrapes. `reuse_connections: false` is the reliable answer for an operator that
behaves this way.

```yaml
modules:
  default: {}
  entur:
    max_concurrency: 1
    per_vehicle_type: true
    headers:
      ET-Client-Name: my-company-gbfs-exporter
```


## Run

```
./gbfs_exporter -config config.yml
```

The exporter listens on port 9718. These flags are available:

| Flag | Meaning |
| --- | --- |
| `-config` | Path of the configuration file. The default is `config.yml`. |
| `-web.listen-address` | Address to listen on. This flag overrides the file. |
| `-log.level` | `debug`, `info`, `warn`, or `error`. |
| `-version` | Print the version and exit. |

These endpoints are available:

| Path | Content |
| --- | --- |
| `/probe` | The metrics of the one system that `target` names. |
| `/metrics` | The metrics of the exporter itself. |
| `/healthz` | The text `ok`. |
| `/` | A short landing page. |

`/probe` accepts these query parameters:

| Parameter | Meaning |
| --- | --- |
| `target` | The URL of the auto-discovery file. This parameter is necessary. |
| `module` | The module of the operator. The default is the module called `default`. |

Example:

```
curl 'http://localhost:9718/probe?target=https://gbfs.urbansharing.com/oslobysykkel.no/gbfs.json'
```

`/probe` and `/metrics` are separate, the layout that `blackbox_exporter` and
`snmp_exporter` use. A scrape of one system must not carry the runtime metrics
of the exporter, because they would repeat under the `instance` label of every
target. `/metrics` holds these:

| Metric | Meaning |
| --- | --- |
| `gbfs_exporter_build_info` | The version, the revision, and the Go version of the build. |
| `gbfs_exporter_requests_rejected_total` | Requests that the exporter refused, by `reason`. |
| `go_*`, `process_*` | Goroutines, memory, and file descriptors. |

The `reason` label is `bad_target`, `forbidden_host`, `unknown_module`, or
`too_many_in_flight`. A refused request carries no metrics body, so Prometheus
sees only that the scrape failed. This counter says why.

### The system label

The exporter sets no `system` label. The person who runs Prometheus names each
target, which is where a target label belongs. Two systems of one operator
often share a host, so a name taken from the URL would merge them: four
nextbike cities all live on `gbfs.nextbike.net`.


## Metrics

Every metric is a gauge. The exporter sets no `system` label. Prometheus sets
that label on each target, as the scrape configuration below shows.

One scrape returns the metrics of one system. Prometheus adds the `instance`
label, which holds the URL of the feed.

| Metric | Labels | Meaning |
| --- | --- | --- |
| `gbfs_up` | | 1 if the exporter read every feed that the system publishes, 0 if any feed or the discovery file failed. |
| `gbfs_feed_up` | `feed` | 1 if the exporter read this feed, 0 if it failed. |
| `gbfs_feed_last_updated_timestamp_seconds` | `feed` | Unix time of the `last_updated` header of the feed. |
| `gbfs_feed_ttl_seconds` | `feed` | Seconds before the publisher changes the feed. 0 means always fresh. |
| `gbfs_feed_duration_seconds` | `feed` | Seconds that the exporter took to read the feed. |
| `gbfs_system_info` | `system_id`, `system_name`, `gbfs_version`, `timezone` | System metadata. `gbfs_version` is the version of the feed, not of the exporter. |
| `gbfs_station_info` | `station_id`, `station_name`, `lat`, `lon` | Station metadata. The value is always 1. |
| `gbfs_vehicle_type_info` | `vehicle_type_id`, `vehicle_type_name`, `form_factor`, `propulsion_type` | Vehicle type metadata. The value is always 1. |
| `gbfs_station_capacity` | `station_id` | Total parking positions: docking points for a physical station, parkable vehicles for a virtual one. |
| `gbfs_station_vehicles_available` | `station_id` | Functional vehicles physically at the station. A rider can take one only where `gbfs_station_renting` is 1. |
| `gbfs_station_vehicles_disabled` | `station_id` | Vehicles that a rider cannot take. |
| `gbfs_station_docks_available` | `station_id` | Docks that accept a vehicle. |
| `gbfs_station_docks_disabled` | `station_id` | Docks that do not accept a vehicle. |
| `gbfs_station_installed` | `station_id` | 1 if the station is on the street. |
| `gbfs_station_renting` | `station_id` | 1 if the station gives out vehicles. |
| `gbfs_station_returning` | `station_id` | 1 if the station takes back vehicles. |
| `gbfs_station_type_vehicles_available` | `station_id`, `vehicle_type_id`, `form_factor`, `propulsion_type` | Vehicles of one type at the station. A breakdown of `gbfs_station_vehicles_available`; never add the two. Set `per_vehicle_type` to get this metric. |
| `gbfs_vehicles` | `vehicle_type_id`, `form_factor`, `propulsion_type`, `state`, `docked` | Vehicles in the vehicle feed, docked or not. The state is `available`, `reserved`, or `disabled`. |
| `gbfs_system_stations` | | Distinct stations across `station_information` and `station_status`. |
| `gbfs_system_vehicles_available` | | Vehicles at all stations that a rider can take. |
| `gbfs_system_vehicles_disabled` | | Vehicles at all stations that a rider cannot take. |
| `gbfs_system_docks_available` | | Docks at all stations that accept a vehicle. |

The station name and the position stay in `gbfs_station_info`. The other
station metrics carry only the station identifier. This keeps the number of
label values low. To get the name in a query, join the two metrics.

A vehicle that is both disabled and reserved counts as disabled.

A feed that fails does not remove the feeds that answered. The exporter sets
`gbfs_up` to 0 and publishes the data that it did read. Alert on `gbfs_up`, and
not on a metric that disappears.

A failure of the auto-discovery file also sets `gbfs_up` to 0. In that case the
exporter read no feed at all, so it publishes only the discovery feed:
`gbfs_feed_up{feed="gbfs"} 0` and its duration.

`gbfs_up` is one bit for the whole system. To find the feed that failed, use
`gbfs_feed_up`. The `feed` label holds one of these names:

| Name | File |
| --- | --- |
| `gbfs` | The auto-discovery file. |
| `system_information` | `system_information.json` |
| `station_information` | `station_information.json` |
| `station_status` | `station_status.json` |
| `vehicle_status` | `vehicle_status.json`, or `free_bike_status.json` in GBFS 2.x |
| `vehicle_types` | `vehicle_types.json` |

A feed that the system does not publish gets no `gbfs_feed_up` series. The
absence means "this system does not publish that feed", and not "this feed is
down".

### Stale data

An operator can serve HTTP 200 with data that stopped changing hours ago. Such
a feed reads as healthy, because every fetch succeeds. Watch the age of the
data instead:

```promql
time() - gbfs_feed_last_updated_timestamp_seconds > 300
```

`gbfs_feed_ttl_seconds` holds the budget that the publisher states, so an alert
can follow the operator instead of a fixed number:

```promql
time() - gbfs_feed_last_updated_timestamp_seconds{feed!="gbfs"}
  > 10 * clamp_min(gbfs_feed_ttl_seconds, 60)
```

Leave the auto-discovery file out of the alert. It lists the other feeds and
changes rarely, so a stale timestamp on it is normal. Entur serves a
Trondheim discovery file that states `ttl: 15` and carries a `last_updated`
six hours old, while every feed that it lists is under a minute old. An alert
that covers `feed="gbfs"` fires forever on that operator.

A feed that omits `last_updated` or `ttl` gets no series for it.

The exporter writes a metric only for a field that the feed holds. A system
without docks gets no `gbfs_station_docks_available`. A feed that omits
`is_renting` gets no `gbfs_station_renting`, because a 0 reads as a closed
station.

The same rule covers the `gbfs_system_*` totals. Each one appears only when at
least one station reported the field that it sums. GBFS lets an operator hide
the disabled counters, and Strasbourg Vel'hop hides them at all 40 stations, so
that system gets no `gbfs_system_vehicles_disabled`. An absent total means "not
published", and a 0 would read as "nothing is broken".

A total can still cover only part of the system, because one station can omit a
field that the others report. To measure the coverage, compare the count of
station series with the station count:

```promql
count by (system) (gbfs_station_docks_available)
  / sum by (system) (gbfs_system_stations)
```

GBFS 3.0 gives a name in several languages. The exporter keeps the English
name. Without an English name, it keeps the first name of the list.

A system that publishes no `vehicle_types.json` carries non-motorized bicycles
only, so its vehicles report `form_factor="bicycle"` and
`propulsion_type="human"`. A system that publishes the feed but did not answer
reports `unknown` for both, because the exporter must not claim a bicycle.

### Docked vehicles

Many operators list every docked vehicle in the vehicle feed. Strasbourg
Vel'hop is one: each of its bikes carries a `station_id`, and none is free
floating. The `docked` label separates them:

```promql
sum by (system) (gbfs_vehicles{docked="false"})
```

Read `docked` as "the feed gave the vehicle a `station_id`", and not as "the
vehicle is at a station". GBFS requires the field only when the system also
publishes `vehicle_types.json`, and the field did not exist before GBFS 2.1.

`gbfs_vehicles{docked="true"}` still overlaps the station metrics, because the
same bike appears in both feeds. The two do not match exactly, because a
docked disabled vehicle counts under `gbfs_vehicles{state="disabled"}` but
under `num_bikes_disabled` at the station, not under the available count. Do
not add `gbfs_vehicles` to `gbfs_station_vehicles_available`.

## Prometheus configuration

Prometheus holds the list of systems. A relabel rule moves each target into the
`target` parameter, and a label on each target carries the system name.

```yaml
scrape_configs:
  - job_name: gbfs
    scrape_interval: 60s
    scrape_timeout: 45s
    metrics_path: /probe
    static_configs:
      - targets: [https://gbfs.urbansharing.com/oslobysykkel.no/gbfs.json]
        labels: {system: oslo}
      - targets: [https://gbfs.nextbike.net/maps/gbfs/v2/nextbike_ae/gbfs.json]
        labels: {system: velhop}
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: localhost:9718
```

Set the `system` label on every target. The exporter does not set it, so a
target without the label gets no `system` at all. Two systems of one operator
often share a host, so a name taken from the URL would merge them: four
nextbike cities all live on `gbfs.nextbike.net`.

For an operator that needs a module, add the `module` parameter to the job:

```yaml
  - job_name: gbfs_entur
    scrape_interval: 60s
    scrape_timeout: 45s
    metrics_path: /probe
    params:
      module: [entur]
    static_configs:
      - targets: [https://api.entur.io/mobility/v2/gbfs/v3/trondheimbysykkel/gbfs]
        labels: {system: trondheim}
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: localhost:9718
```

Swap `static_configs` for `file_sd_configs` to change the list without a
restart of Prometheus.

Scrape the exporter itself with a second, plain job:

```yaml
  - job_name: gbfs_exporter
    static_configs:
      - targets: ["localhost:9718"]
```

## Grafana dashboards

The folder `grafana/` holds three dashboards. Each one links to the others from a
dropdown in its top bar.

| File | Scope | Content |
| --- | --- | --- |
| `gbfs-health-dashboard.json` | Every system at once | Feed freshness against the published ttl, which feed failed, scrape cost, the plausibility of each system's numbers, and the health of the exporter itself. |
| `gbfs-dashboard.json` | One system | Totals, occupancy, the fleet by drive, station service, a map, and a table of every station. |
| `gbfs-stations-dashboard.json` | Chosen stations | The selection combined, then one row per station: occupancy against capacity, the service flags, and the vehicle mix. |

To import one by hand, follow these steps:

1. Open Grafana.
2. Select **Dashboards**, then **New**, then **Import**.
3. Upload the file.
4. Select your Prometheus data source.

To install it with provisioning, follow these steps:

1. Copy every dashboard of `grafana/` to `/var/lib/grafana/dashboards/`.
2. Copy `grafana/provisioning-dashboard.yml` to
   `/etc/grafana/provisioning/dashboards/gbfs.yml`.
3. Restart Grafana.

Both dashboards have a **Data source** variable and a **System** variable. The
System dropdown shows the name that the operator publishes, and it takes one
value.

The station dashboard adds a **Station** variable that takes several values. It
shows the whole selection as one pool, then one row per station. The graph stacks three bands to the capacity
line: the vehicles that a rider can take, the docks that accept a vehicle, and
the space that does neither. That third band is derived by subtraction, because
no GBFS field states it. At a car sharing bay it is mostly cars out on rental,
and at a docked bike station it is broken docks or bikes.

The station list offers **All**. It draws a row for every station of the
system, so read it as a bulk view rather than a comparison: Oslo makes 268 rows
and Citiz 293.

Pick the stations you want. The dashboard draws one row for each, and **All**
draws a row for every station of the system.

The system dashboard holds six rows, ordered from the whole network down to
reference data:

| Row | Content |
| --- | --- |
| Overview | The state of the system, the station count, the vehicle and dock counts, and the occupancy. |
| Availability | The network against its capacity, the occupancy over time, and the count of empty and full stations. |
| Vehicle feed | Vehicles the operator lists outside the station counts, by state, by `docked`, and by drive. A docked system need not publish this feed, and the row says so when it does not. |
| Stations | A map of every station, and a sortable table of every station with its vehicles and free docks. |
| Station service | Stations with a service flag off, over time and in a table. |
| Vehicle types | The types that the operator publishes, and what stands at the stations by drive. |
| Feed health | The overall state and the state of each feed over time, and the metadata of the system. |

The Availability row measures against capacity rather than against free docks.
It stacks the vehicles a rider can take, the docks that accept a vehicle, and
the space that does neither, up to a dashed capacity line. That third band is
derived by subtraction, because no GBFS field states it.

Occupancy divides the vehicles at stations by the capacity of the stations that
report both. It does not divide by the free docks. An operator that reports no
free docks would otherwise read as permanently full, and Citiz Grand Est is one:
it reports zero free docks at all 293 of its stations.

The count of full stations is left out for such an operator, for the same
reason.

The map and the table join `gbfs_station_info`. The map takes the position from
it, and the table takes the name. The dashboard needs Grafana 10 or higher for
the map panel.

### The map opens too far out

The station map asks Grafana to fit the view to the stations. Grafana 12.4.4
sets the centre correctly and then loses the zoom, so the map opens far too far
out. Scroll to zoom in.

The cause sits in `GeomapPanel.initViewExtent`, which runs
`view.setResolution` after `view.fit` and computes the resolution from
`this.map?.getSize()`. Reading the value back with the map debug control shows
a correct centre next to a zoom near 1.

Do not try to repair this by adding a `zoom` to the map view. Grafana reads
that key as the upper bound of the fit, through
`const maxZoom = config.zoom ?? config.maxZoom`, so a `zoom` of 1 pins the map
to the whole world. Grafana writes that key itself when it saves a dashboard
from the user interface.

## Example queries

Stations that hold no vehicle:

```promql
count by (system) (gbfs_station_vehicles_available == 0)
```

Vehicles in the vehicle feed:

```promql
sum by (system) (gbfs_vehicles)
```

Occupancy of each system:

```promql
sum by (system) (
  gbfs_station_vehicles_available
    and on (system, station_id) gbfs_station_capacity
)
/
sum by (system) (
  gbfs_station_capacity
    and on (system, station_id) gbfs_station_vehicles_available
)
```

Divide by the capacity, and not by the free docks. An operator that reports no
free docks would otherwise read as permanently full. Citiz Grand Est is one: it
reports zero free docks at all 293 of its stations, so the ratio against free
docks answers 1.0 while the true occupancy is 0.60.

The `and on` keeps the two sums over the same stations. A station that the
status feed never described is left out, rather than counted as empty.

The ten emptiest stations, with their names:

```promql
bottomk(10,
  gbfs_station_vehicles_available
    * on (system, station_id) group_left (station_name) gbfs_station_info
)
```

Electric bikes that wait on the street:

```promql
sum by (system) (
  gbfs_vehicles{form_factor="bicycle", propulsion_type=~"electric.*", state="available"}
)
```

The `form_factor` of a GBFS vehicle type does not say whether the vehicle is
electric. A classic bike and an electric bike both report `bicycle`. The drive
sits in `propulsion_type`, which is `human`, `electric_assist`, `electric`, or
`combustion`.

A system that the exporter cannot read:

```promql
gbfs_up == 0
```

## Test

```
make test
```

The tests use a local HTTP server with fixed feeds. One fixture is GBFS 2.3 and
the other is GBFS 3.0. No test calls a real operator.

## License

MIT
