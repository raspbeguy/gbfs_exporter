# gbfs_exporter

[![CI](https://github.com/raspbeguy/gbfs_exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/raspbeguy/gbfs_exporter/actions/workflows/ci.yml)

A Prometheus exporter for GBFS feeds.

GBFS is the General Bikeshare Feed Specification. Bike share and scooter share
operators publish it. The exporter turns the feeds into Prometheus metrics.

The exporter reads GBFS 2.x and GBFS 3.0. It finds the version from the
auto-discovery file and adapts to the field names of that version.

The exporter follows the multi-target pattern of `blackbox_exporter`. One
scrape reads one system, and the query string names it. Prometheus holds the
list of systems, so a new system needs no restart of the exporter.

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

### Binary

Each release carries an archive for linux, macOS, and FreeBSD, on amd64 and on
arm64. Download the archive of your platform from the releases page, then
verify it against `checksums.txt`:

```
sha256sum -c checksums.txt --ignore-missing
```

The archive holds the binary, the example configuration, and the Grafana
dashboard.

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
| `max_in_flight` | `4` | Scrapes that run at the same time. A scrape above the limit gets HTTP 503. |
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
| `headers` | empty | Headers to add to every request. |

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
| `/metrics` | The metrics of the one system that `target` names. |
| `/healthz` | The text `ok`. |
| `/` | A short landing page. |

`/metrics` accepts these query parameters:

| Parameter | Meaning |
| --- | --- |
| `target` | The URL of the auto-discovery file. This parameter is necessary. |
| `module` | The module of the operator. The default is the module called `default`. |
| `name` | The value of the `system` label. The default is the host of the target. |

Example:

```
curl 'http://localhost:9718/metrics?target=https://gbfs.urbansharing.com/oslobysykkel.no/gbfs.json&name=oslo'
```

The exporter serves no metric about itself. `/metrics` returns GBFS data only,
because the exporter serves one endpoint. Watch the exporter with the `up`
metric and the `scrape_duration_seconds` metric of Prometheus.


## Metrics

Every metric is a gauge. Every metric carries the `system` label, which holds
the `name` parameter of the request, or the host of the target.

One scrape returns the metrics of one system. Prometheus adds the `instance`
label, which holds the URL of the feed.

| Metric | Extra labels | Meaning |
| --- | --- | --- |
| `gbfs_up` | | 1 if the exporter read every feed that the system publishes, 0 if any feed or the discovery file failed. |
| `gbfs_feed_up` | `feed` | 1 if the exporter read this feed, 0 if it failed. |
| `gbfs_feed_last_updated_timestamp_seconds` | `feed` | Unix time of the `last_updated` header of the feed. |
| `gbfs_feed_ttl_seconds` | `feed` | Seconds before the publisher changes the feed. 0 means always fresh. |
| `gbfs_feed_duration_seconds` | `feed` | Seconds that the exporter took to read the feed. |
| `gbfs_system_info` | `system_id`, `name`, `version`, `timezone` | System metadata. The value is always 1. |
| `gbfs_station_info` | `station_id`, `name`, `lat`, `lon` | Station metadata. The value is always 1. |
| `gbfs_vehicle_type_info` | `vehicle_type_id`, `name`, `form_factor`, `propulsion_type` | Vehicle type metadata. The value is always 1. |
| `gbfs_station_capacity` | `station_id` | Total parking positions: docking points for a physical station, parkable vehicles for a virtual one. |
| `gbfs_station_vehicles_available` | `station_id` | Functional vehicles physically at the station. A rider can take one only where `gbfs_station_renting` is 1. |
| `gbfs_station_vehicles_disabled` | `station_id` | Vehicles that a rider cannot take. |
| `gbfs_station_docks_available` | `station_id` | Docks that accept a vehicle. |
| `gbfs_station_docks_disabled` | `station_id` | Docks that do not accept a vehicle. |
| `gbfs_station_installed` | `station_id` | 1 if the station is on the street. |
| `gbfs_station_renting` | `station_id` | 1 if the station gives out vehicles. |
| `gbfs_station_returning` | `station_id` | 1 if the station takes back vehicles. |
| `gbfs_station_vehicles_available_by_type` | `station_id`, `vehicle_type_id`, `form_factor`, `propulsion_type` | Vehicles of one type at the station. Set `per_vehicle_type` to get this metric. |
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
exporter read no feed at all, so it publishes only
`gbfs_feed_up{feed="gbfs"} 0`.

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
time() - gbfs_feed_last_updated_timestamp_seconds
  > 10 * clamp_min(gbfs_feed_ttl_seconds, 60)
```

A feed that omits `last_updated` or `ttl` gets no series for it.

The exporter writes a metric only for a field that the feed holds. A system
without docks gets no `gbfs_station_docks_available`. A feed that omits
`is_renting` gets no `gbfs_station_renting`, because a 0 reads as a closed
station.

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

Prometheus holds the list of systems. A relabel rule moves each target into
the `target` parameter.

```yaml
scrape_configs:
  - job_name: gbfs
    scrape_interval: 60s
    scrape_timeout: 45s
    metrics_path: /metrics
    static_configs:
      - targets:
          - https://gbfs.urbansharing.com/oslobysykkel.no/gbfs.json
          - https://gbfs.lyft.com/gbfs/2.3/bkn/gbfs.json
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: localhost:9718
```

The `system` label falls back to the host of the target. Two systems of one
operator therefore share a label value. Give each target its own `name` with a
second job, or with a label in the target list:

```yaml
    static_configs:
      - targets: [https://gbfs.urbansharing.com/oslobysykkel.no/gbfs.json]
        labels: {name: oslo}
    relabel_configs:
      - source_labels: [name]
        target_label: __param_name
      - regex: name
        action: labeldrop
```

For an operator that needs a module, add the `module` parameter to the job:

```yaml
  - job_name: gbfs_entur
    metrics_path: /metrics
    params:
      module: [entur]
    static_configs:
      - targets: [https://api.entur.io/mobility/v2/gbfs/v3/trondheimbysykkel/gbfs]
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


## Grafana dashboard

The folder `grafana/` holds a dashboard.

To import it by hand, follow these steps:

1. Open Grafana.
2. Select **Dashboards**, then **New**, then **Import**.
3. Upload `grafana/gbfs-dashboard.json`.
4. Select your Prometheus data source.

To install it with provisioning, follow these steps:

1. Copy `grafana/gbfs-dashboard.json` to `/var/lib/grafana/dashboards/`.
2. Copy `grafana/provisioning-dashboard.yml` to
   `/etc/grafana/provisioning/dashboards/gbfs.yml`.
3. Restart Grafana.

The dashboard has two variables. **Data source** selects the Prometheus server.
**System** selects one or more systems, and it accepts several values at once.

The dashboard holds four rows:

| Row | Content |
| --- | --- |
| Overview | Systems down, station count, vehicle count, dock count, and fill rate. |
| Availability | Vehicles and docks over time, free-floating vehicles by state, and the count of empty and full stations. |
| Stations | A map of every station, and a table of the twenty emptiest stations. |
| Feed health | The state of each feed over time, and the metadata of each system. |

The map and the table join `gbfs_station_info` to get the station name and the
position. The dashboard needs Grafana 10 or higher for the map panel.

## Example queries

Stations that hold no vehicle:

```promql
count by (system) (gbfs_station_vehicles_available == 0)
```

Vehicles in the vehicle feed:

```promql
sum by (system) (gbfs_vehicles)
```

Fill rate of each system:

```promql
gbfs_system_vehicles_available
  / (gbfs_system_vehicles_available + gbfs_system_docks_available)
```

The ten emptiest stations, with their names:

```promql
bottomk(10,
  gbfs_station_vehicles_available
    * on (system, station_id) group_left (name) gbfs_station_info
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
