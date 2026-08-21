package gbfs

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// maxBodyBytes limits one feed response. A large system sends a few megabytes.
const maxBodyBytes = 64 << 20

// Client reads GBFS feeds over HTTP.
type Client struct {
	http      *http.Client
	userAgent string
}

// NewClient returns a client that gives up on one feed after the timeout.
//
// The transport reads the proxy settings from the environment, so the
// variables HTTP_PROXY, HTTPS_PROXY, and NO_PROXY work.
func NewClient(timeout time.Duration, userAgent string) *Client {
	return &Client{
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				ForceAttemptHTTP2:     true,
				MaxIdleConnsPerHost:   4,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		userAgent: userAgent,
	}
}

// FetchOptions changes how the client reads one system.
type FetchOptions struct {
	// Headers are added to every request. Use them for an API key or for a
	// client name that the operator asks for.
	Headers map[string]string
	// MaxConcurrency limits the number of feeds that the client reads at the
	// same time. A value of 0 or less means no limit. Set it to 1 for an
	// operator that answers HTTP 429 to parallel requests.
	MaxConcurrency int
}

func (c *Client) get(ctx context.Context, feedURL string, headers map[string]string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return fmt.Errorf("gbfs: bad URL %q: %w", feedURL, err)
	}
	request.Header.Set("User-Agent", c.userAgent)
	request.Header.Set("Accept", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("gbfs: cannot fetch %s: %w", feedURL, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
	}()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("gbfs: %s returned HTTP %d", feedURL, response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxBodyBytes))
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("gbfs: cannot read %s: %w", feedURL, err)
	}
	return nil
}

// Canonical feed names. The exporter reports a feed under one name across
// every GBFS version. GBFS 2.x calls the vehicle feed free_bike_status, and
// GBFS 3.0 calls it vehicle_status. Both report as FeedVehicleStatus, so one
// alert matches both versions.
const (
	FeedDiscovery          = "gbfs"
	FeedSystemInformation  = "system_information"
	FeedStationInformation = "station_information"
	FeedStationStatus      = "station_status"
	FeedVehicleStatus      = "vehicle_status"
	FeedVehicleTypes       = "vehicle_types"
)

// FeedResult is the outcome of one feed read.
type FeedResult struct {
	// OK is true if the client read and decoded the feed.
	OK bool
	// LastUpdated is the last_updated header of the feed. The zero value
	// means that the feed gave no value.
	LastUpdated time.Time
	// TTL is the ttl header of the feed, in seconds. A nil value means that
	// the feed gave no value.
	TTL *int
	// Duration is the time that the read took.
	Duration time.Duration
}

// Snapshot holds one reading of every feed that the exporter needs.
type Snapshot struct {
	Version      string
	SystemID     string
	SystemName   string
	Timezone     string
	Stations     []Station
	Status       []StationStatus
	Vehicles     []Vehicle
	VehicleTypes map[string]VehicleType
	// Feeds holds one entry per feed that the system publishes, under a
	// canonical name. A feed that the auto-discovery file does not list gets
	// no entry, because the system does not publish it.
	Feeds map[string]FeedResult
}

// Fetch reads the auto-discovery file and then every feed that it lists.
//
// A missing optional feed is not an error. A system with docks has no vehicle
// feed, and a free-floating system has no station feed.
func (c *Client) Fetch(ctx context.Context, discoveryURL string, options FetchOptions) (*Snapshot, error) {
	snapshot := &Snapshot{
		Version:      "1.0",
		VehicleTypes: map[string]VehicleType{},
		Feeds:        map[string]FeedResult{},
	}

	// The snapshot is never nil, even when the auto-discovery file fails. The
	// caller still needs to report that the discovery feed is down.
	var discovery Discovery
	start := time.Now()
	err := c.get(ctx, discoveryURL, options.Headers, &discovery)
	snapshot.Feeds[FeedDiscovery] = result(discovery.FeedHeader, time.Since(start), err)
	if err != nil {
		return snapshot, err
	}

	feeds, err := feedIndex(discoveryURL, discovery.Data.Feeds)
	if err != nil {
		return snapshot, err
	}

	if discovery.Version != "" {
		snapshot.Version = discovery.Version
	}

	var (
		mutex  sync.Mutex
		group  sync.WaitGroup
		failed []error
	)
	var slots chan struct{}
	if options.MaxConcurrency > 0 {
		slots = make(chan struct{}, options.MaxConcurrency)
	}
	// fetch reads one feed. listedName is the name in the auto-discovery
	// file, and canonical is the name that the snapshot reports it under.
	fetch := func(listedName, canonical string, decode func(context.Context, string) (FeedHeader, error)) {
		feedURL, ok := feeds[listedName]
		if !ok {
			return
		}
		group.Add(1)
		go func() {
			defer group.Done()
			if slots != nil {
				select {
				case slots <- struct{}{}:
					defer func() { <-slots }()
				case <-ctx.Done():
					mutex.Lock()
					failed = append(failed, fmt.Errorf("gbfs: gave up before reading %s: %w", canonical, ctx.Err()))
					snapshot.Feeds[canonical] = FeedResult{}
					mutex.Unlock()
					return
				}
			}
			start := time.Now()
			header, err := decode(ctx, feedURL)
			taken := time.Since(start)
			mutex.Lock()
			snapshot.Feeds[canonical] = result(header, taken, err)
			if err != nil {
				failed = append(failed, err)
			}
			mutex.Unlock()
		}()
	}

	fetch(FeedSystemInformation, FeedSystemInformation, func(ctx context.Context, u string) (FeedHeader, error) {
		var feed SystemInformation
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return feed.FeedHeader, err
		}
		mutex.Lock()
		defer mutex.Unlock()
		snapshot.SystemID = feed.Data.SystemID
		snapshot.SystemName = string(feed.Data.Name)
		snapshot.Timezone = feed.Data.Timezone
		return feed.FeedHeader, nil
	})

	fetch(FeedStationInformation, FeedStationInformation, func(ctx context.Context, u string) (FeedHeader, error) {
		var feed StationInformation
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return feed.FeedHeader, err
		}
		mutex.Lock()
		defer mutex.Unlock()
		snapshot.Stations = feed.Data.Stations
		return feed.FeedHeader, nil
	})

	fetch(FeedStationStatus, FeedStationStatus, func(ctx context.Context, u string) (FeedHeader, error) {
		var feed StationStatusFeed
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return feed.FeedHeader, err
		}
		mutex.Lock()
		defer mutex.Unlock()
		snapshot.Status = feed.Data.Stations
		return feed.FeedHeader, nil
	})

	fetch(FeedVehicleTypes, FeedVehicleTypes, func(ctx context.Context, u string) (FeedHeader, error) {
		var feed VehicleTypesFeed
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return feed.FeedHeader, err
		}
		mutex.Lock()
		defer mutex.Unlock()
		for _, vehicleType := range feed.Data.VehicleTypes {
			snapshot.VehicleTypes[vehicleType.VehicleTypeID] = vehicleType
		}
		return feed.FeedHeader, nil
	})

	// GBFS 2.x names the vehicle feed free_bike_status. Both names report
	// under the canonical FeedVehicleStatus.
	vehicleFeed := FeedVehicleStatus
	if _, ok := feeds[vehicleFeed]; !ok {
		vehicleFeed = "free_bike_status"
	}
	fetch(vehicleFeed, FeedVehicleStatus, func(ctx context.Context, u string) (FeedHeader, error) {
		var feed VehicleStatusFeed
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return feed.FeedHeader, err
		}
		mutex.Lock()
		defer mutex.Unlock()
		snapshot.Vehicles = feed.All()
		return feed.FeedHeader, nil
	})

	group.Wait()
	if len(failed) > 0 {
		return snapshot, joinErrors(failed)
	}
	return snapshot, nil
}

// feedIndex maps a feed name to an absolute URL.
//
// Feed names are normalized, because some feeds write "station_status" and
// others write "station_status.json".
func feedIndex(discoveryURL string, feeds []Feed) (map[string]string, error) {
	base, err := url.Parse(discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("gbfs: bad discovery URL %q: %w", discoveryURL, err)
	}
	index := make(map[string]string, len(feeds))
	for _, feed := range feeds {
		name := strings.TrimSuffix(strings.TrimSpace(feed.Name), ".json")
		if name == "" || feed.URL == "" {
			continue
		}
		reference, err := url.Parse(feed.URL)
		if err != nil {
			continue
		}
		index[name] = base.ResolveReference(reference).String()
	}
	return index, nil
}

// result records what happened when the client read one feed.
func result(header FeedHeader, taken time.Duration, err error) FeedResult {
	return FeedResult{
		OK:          err == nil,
		LastUpdated: header.LastUpdated.Time,
		TTL:         header.TTL,
		Duration:    taken,
	}
}

func joinErrors(errs []error) error {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return fmt.Errorf("%s", strings.Join(messages, "; "))
}
