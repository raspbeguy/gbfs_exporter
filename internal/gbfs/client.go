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
}

// Fetch reads the auto-discovery file and then every feed that it lists.
//
// A missing optional feed is not an error. A system with docks has no vehicle
// feed, and a free-floating system has no station feed.
func (c *Client) Fetch(ctx context.Context, discoveryURL string, options FetchOptions) (*Snapshot, error) {
	var discovery Discovery
	if err := c.get(ctx, discoveryURL, options.Headers, &discovery); err != nil {
		return nil, err
	}

	feeds, err := feedIndex(discoveryURL, discovery.Data.Feeds)
	if err != nil {
		return nil, err
	}

	snapshot := &Snapshot{
		Version:      discovery.Version,
		VehicleTypes: map[string]VehicleType{},
	}
	if snapshot.Version == "" {
		snapshot.Version = "1.0"
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
	fetch := func(name string, decode func(context.Context, string) error) {
		feedURL, ok := feeds[name]
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
					failed = append(failed, fmt.Errorf("gbfs: gave up before reading %s: %w", name, ctx.Err()))
					mutex.Unlock()
					return
				}
			}
			if err := decode(ctx, feedURL); err != nil {
				mutex.Lock()
				failed = append(failed, err)
				mutex.Unlock()
			}
		}()
	}

	fetch("system_information", func(ctx context.Context, u string) error {
		var feed SystemInformation
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return err
		}
		mutex.Lock()
		defer mutex.Unlock()
		snapshot.SystemID = feed.Data.SystemID
		snapshot.SystemName = string(feed.Data.Name)
		snapshot.Timezone = feed.Data.Timezone
		return nil
	})

	fetch("station_information", func(ctx context.Context, u string) error {
		var feed StationInformation
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return err
		}
		mutex.Lock()
		defer mutex.Unlock()
		snapshot.Stations = feed.Data.Stations
		return nil
	})

	fetch("station_status", func(ctx context.Context, u string) error {
		var feed StationStatusFeed
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return err
		}
		mutex.Lock()
		defer mutex.Unlock()
		snapshot.Status = feed.Data.Stations
		return nil
	})

	fetch("vehicle_types", func(ctx context.Context, u string) error {
		var feed VehicleTypesFeed
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return err
		}
		mutex.Lock()
		defer mutex.Unlock()
		for _, vehicleType := range feed.Data.VehicleTypes {
			snapshot.VehicleTypes[vehicleType.VehicleTypeID] = vehicleType
		}
		return nil
	})

	vehicleFeed := "vehicle_status"
	if _, ok := feeds[vehicleFeed]; !ok {
		vehicleFeed = "free_bike_status"
	}
	fetch(vehicleFeed, func(ctx context.Context, u string) error {
		var feed VehicleStatusFeed
		if err := c.get(ctx, u, options.Headers, &feed); err != nil {
			return err
		}
		mutex.Lock()
		defer mutex.Unlock()
		snapshot.Vehicles = feed.All()
		return nil
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

func joinErrors(errs []error) error {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return fmt.Errorf("%s", strings.Join(messages, "; "))
}
