// Package update asks the release site which version is current, so a wallet
// that has fallen behind can say so at startup.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Where releases are published. The Pages site mirrors every release and
// writes latest.json alongside; the GitHub API is the fallback for a site
// built before latest.json existed.
const (
	SiteURL       = "https://brad1121.github.io/FFSWallet/"
	LatestURL     = SiteURL + "latest.json"
	FallbackURL   = "https://api.github.com/repos/brad1121/FFSWallet/releases/latest"
	ReleasesURL   = "https://github.com/brad1121/FFSWallet/releases"
	maxBodyBytes  = 1 << 20
	userAgentBase = "FFSWallet-update-check"
)

// Latest is what the release site says is current.
type Latest struct {
	Tag         string    `json:"tag"`
	Version     string    `json:"version"`
	Name        string    `json:"name"`
	PublishedAt time.Time `json:"published_at"`
	URL         string    `json:"url"`
}

// Result of a check against the running version.
type Result struct {
	Current  string
	Latest   Latest
	Outdated bool
}

// Checker fetches the latest release. Zero value uses the real site.
type Checker struct {
	Client      *http.Client
	LatestURL   string
	FallbackURL string
}

// Check reports whether current is behind the published release. current is
// the running version ("0.0.8" or "v0.0.8"); a version that does not parse
// is an error, since nothing can be said about it.
func (c Checker) Check(ctx context.Context, current string) (Result, error) {
	cur, ok := parseVersion(current)
	if !ok || !IsReleaseVersion(current) {
		return Result{}, fmt.Errorf("running version %q is not a release version", current)
	}
	latest, err := c.Latest(ctx)
	if err != nil {
		return Result{}, err
	}
	lat, ok := parseVersion(latest.Version)
	if !ok {
		return Result{}, fmt.Errorf("release site reports version %q, which does not parse", latest.Version)
	}
	return Result{Current: current, Latest: latest, Outdated: compare(cur, lat) < 0}, nil
}

// Latest fetches the current release from the site, falling back to the
// GitHub API when the site has no latest.json yet.
func (c Checker) Latest(ctx context.Context) (Latest, error) {
	latestURL, fallbackURL := c.LatestURL, c.FallbackURL
	if latestURL == "" {
		latestURL = LatestURL
	}
	if fallbackURL == "" {
		fallbackURL = FallbackURL
	}
	body, err := c.get(ctx, latestURL)
	if err == nil {
		var l Latest
		if jerr := json.Unmarshal(body, &l); jerr == nil && (l.Tag != "" || l.Version != "") {
			l.normalize()
			return l, nil
		}
		err = errors.New("latest.json did not name a release")
	}
	siteErr := err

	body, err = c.get(ctx, fallbackURL)
	if err != nil {
		return Latest{}, fmt.Errorf("release site: %v; github: %w", siteErr, err)
	}
	var rel struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		PublishedAt time.Time `json:"published_at"`
		HTMLURL     string    `json:"html_url"`
	}
	if err := json.Unmarshal(body, &rel); err != nil || rel.TagName == "" {
		return Latest{}, fmt.Errorf("release site: %v; github: no release in reply", siteErr)
	}
	l := Latest{Tag: rel.TagName, Name: rel.Name, PublishedAt: rel.PublishedAt, URL: rel.HTMLURL}
	l.normalize()
	return l, nil
}

func (l *Latest) normalize() {
	if l.Version == "" {
		l.Version = strings.TrimPrefix(l.Tag, "v")
	}
	if l.Tag == "" {
		l.Tag = "v" + l.Version
	}
	if l.URL == "" {
		l.URL = ReleasesURL + "/tag/" + l.Tag
	}
}

func (c Checker) get(ctx context.Context, url string) ([]byte, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgentBase)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
}

// parseVersion reads "1.2.3" or "v1.2.3". Anything else — an unpackaged
// build's "0.0.1", a pre-release suffix — is not a release version.
func parseVersion(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func compare(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// IsReleaseVersion says whether v looks like a tagged release ("1.2.3"), as
// opposed to the "0.0.1" Fyne stamps on a plain go build or the "0.0.0" the
// release workflow uses for untagged runs.
func IsReleaseVersion(v string) bool {
	p, ok := parseVersion(v)
	return ok && p != [3]int{0, 0, 0} && p != [3]int{0, 0, 1}
}
