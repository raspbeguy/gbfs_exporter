# gbfs_exporter

A Prometheus exporter for GBFS feeds.

GBFS is the General Bikeshare Feed Specification. Bike share and scooter share
operators publish it. The exporter turns the feeds into Prometheus metrics.

The exporter reads GBFS 2.x and GBFS 3.0. It finds the version from the
auto-discovery file and adapts to the field names of that version.

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

1. Copy the example file.

   ```
   cp config.example.yml config.yml
   ```

2. Replace the systems with your own. The `url` is the auto-discovery file of
   the system, normally `gbfs.json`.

3. Set `max_concurrency` to 1 for an operator that answers HTTP 429 to parallel
   requests. Such a system reads its feeds one after the other. Give `timeout`
   enough room for all of them.

4. Add an API key or a client name under `headers` if the operator asks for one.

The exporter refuses a configuration file that holds an unknown key. This
catches a typing mistake at start.

These settings apply to the whole exporter:

| Setting | Default | Meaning |
| --- | --- | --- |
| `listen_address` | `:9718` | Address that the exporter listens on. |
| `timeout` | `30s` | Budget for one scrape, across every system. |
| `request_timeout` | `10s` | Budget for one feed. It must not exceed `timeout`. |
| `user_agent` | the version | User agent that the exporter sends. |
| `probe.enabled` | `true` | Turn the `/probe` endpoint on or off. |
| `probe.allowed_hosts` | empty | Hosts that `/probe` accepts. Empty accepts every host. |
| `probe.max_in_flight` | `4` | Probes that run at the same time. |

These settings apply to one system:

| Setting | Default | Meaning |
| --- | --- | --- |
| `name` | the host of the url | Value of the `system` label. |
| `url` | none | The auto-discovery file. This setting is necessary. |
| `per_vehicle_type` | `false` | Add one series per station and per vehicle type. |
| `max_concurrency` | `0` | Feeds to read at the same time. 0 means no limit. |
| `headers` | empty | Headers to add to every request. |

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
| `/metrics` | The metrics of every configured system. |
| `/probe` | The metrics of one system that the query string names. |
| `/healthz` | The text `ok`. |

## Metrics

Every metric is a gauge. Every metric carries the `system` label, which holds
the `name` from the configuration file.

| Metric | Extra labels | Meaning |
| --- | --- | --- |
| `gbfs_up` | | 1 if the exporter read every feed, 0 if one feed failed. |

| `gbfs_system_info` | `system_id`, `name`, `version`, `timezone` | System metadata. The value is always 1. |
| `gbfs_station_info` | `station_id`, `name`, `lat`, `lon` | Station metadata. The value is always 1. |
| `gbfs_station_capacity` | `station_id` | Number of docks that the station has. |
| `gbfs_station_vehicles_available` | `station_id` | Vehicles that a rider can take. |
| `gbfs_station_vehicles_disabled` | `station_id` | Vehicles that a rider cannot take. |
| `gbfs_station_docks_available` | `station_id` | Docks that accept a vehicle. |
| `gbfs_station_docks_disabled` | `station_id` | Docks that do not accept a vehicle. |
| `gbfs_station_installed` | `station_id` | 1 if the station is on the street. |
| `gbfs_station_renting` | `station_id` | 1 if the station gives out vehicles. |
| `gbfs_station_returning` | `station_id` | 1 if the station takes back vehicles. |
| `gbfs_station_vehicles_available_by_type` | `station_id`, `vehicle_type_id`, `form_factor` | Vehicles of one type at the station. Set `per_vehicle_type` to get this metric. |
| `gbfs_free_vehicles` | `vehicle_type_id`, `form_factor`, `state` | Vehicles in the vehicle feed. The state is `available`, `reserved`, or `disabled`. |
| `gbfs_system_stations` | | Number of stations in the station feed. |
| `gbfs_system_vehicles_available` | | Vehicles at all stations that a rider can take. |
| `gbfs_system_vehicles_disabled` | | Vehicles at all stations that a rider cannot take. |
| `gbfs_system_docks_available` | | Docks at all stations that accept a vehicle. |
| `gbfs_system_free_vehicles` | | Number of vehicles in the vehicle feed. |

The station name and the position stay in `gbfs_station_info`. The other
station metrics carry only the station identifier. This keeps the number of
label values low. To get the name in a query, join the two metrics.

A vehicle that is both disabled and reserved counts as disabled.

A feed that fails does not remove the feeds that answered. The exporter sets
`gbfs_up` to 0 and publishes the data that it did read. Alert on `gbfs_up`, and
not on a metric that disappears.

The exporter writes a metric only for a field that the feed holds. A system
without docks gets no `gbfs_station_docks_available`. A feed that omits
`is_renting` gets no `gbfs_station_renting`, because a 0 reads as a closed
station.

GBFS 3.0 gives a name in several languages. The exporter keeps the English
name. Without an English name, it keeps the first name of the list.

Some operators list docked vehicles in the vehicle feed. For those operators,
`gbfs_system_free_vehicles` and `gbfs_system_vehicles_available` count the same
vehicle twice. Check the feed of your operator before you add the two metrics.

## Scrape one system that the configuration does not list

### Warning

The `/probe` endpoint fetches the URL that the caller gives. A caller who
reaches the exporter port can therefore reach any address that the exporter can
reach, and can read back whether that address answers. Set `allowed_hosts`, or
set `enabled: false`, or keep the port closed to untrusted callers.

### Use

Use the `/probe` endpoint. It accepts these query parameters:

| Parameter | Meaning |
| --- | --- |
| `target` | The URL of the auto-discovery file. This parameter is necessary. |
| `name` | The value of the `system` label. The default is the host of the target. |
| `per_vehicle_type` | Set it to `true` to get the per-type metric. |
| `max_concurrency` | Number of feeds to read at the same time. |

Example:

```
curl 'http://localhost:9718/probe?target=https://gbfs.urbansharing.com/oslobysykkel.no/gbfs.json&name=oslo'
```

## Prometheus configuration

For the systems in the configuration file, use a static target:

```yaml
scrape_configs:
  - job_name: gbfs
    scrape_interval: 60s
    scrape_timeout: 45s
    static_configs:
      - targets: ["localhost:9718"]
```

For the `/probe` endpoint, use a relabel rule:

```yaml
scrape_configs:
  - job_name: gbfs_probe
    scrape_interval: 60s
    scrape_timeout: 45s
    metrics_path: /probe
    static_configs:
      - targets:
          - https://gbfs.urbansharing.com/oslobysykkel.no/gbfs.json
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: instance
      - target_label: __address__
        replacement: localhost:9718
```

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
sum by (system) (gbfs_free_vehicles{form_factor="bicycle", state="available"})
```

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
