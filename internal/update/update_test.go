package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseAndCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.0.8", "0.0.9", -1},
		{"v0.0.9", "0.0.9", 0},
		{"0.1.0", "0.0.99", 1},
		{"1.0.0", "0.9.9", 1},
	} {
		x, ok1 := parseVersion(tc.a)
		y, ok2 := parseVersion(tc.b)
		if !ok1 || !ok2 {
			t.Fatalf("parse %q/%q failed", tc.a, tc.b)
		}
		if got := compare(x, y); got != tc.want {
			t.Fatalf("compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	for _, bad := range []string{"", "1.2", "1.2.3.4", "a.b.c", "0.0.1-dev"} {
		if _, ok := parseVersion(bad); ok {
			t.Fatalf("%q parsed as a version", bad)
		}
	}
	if IsReleaseVersion("0.0.1") || IsReleaseVersion("0.0.0") || !IsReleaseVersion("0.0.8") {
		t.Fatal("release-version classification wrong")
	}
}

func TestCheckUsesSiteThenFallsBack(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"tag":"v0.0.9","name":"FFSWallet 0.0.9","url":"https://example.test/r/v0.0.9"}`)) //nolint:errcheck
	}))
	defer site.Close()
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.0.7","html_url":"https://example.test/gh/v0.0.7"}`)) //nolint:errcheck
	}))
	defer github.Close()
	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()

	c := Checker{LatestURL: site.URL, FallbackURL: github.URL}
	res, err := c.Check(context.Background(), "0.0.8")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !res.Outdated || res.Latest.Version != "0.0.9" || res.Latest.URL != "https://example.test/r/v0.0.9" {
		t.Fatalf("site result wrong: %+v", res)
	}

	fallback := Checker{LatestURL: missing.URL, FallbackURL: github.URL}
	res, err = fallback.Check(context.Background(), "0.0.8")
	if err != nil {
		t.Fatalf("fallback check: %v", err)
	}
	if res.Outdated || res.Latest.Tag != "v0.0.7" || res.Latest.Version != "0.0.7" {
		t.Fatalf("fallback result wrong: %+v", res)
	}

	dead := Checker{LatestURL: missing.URL, FallbackURL: missing.URL}
	if _, err := dead.Check(context.Background(), "0.0.8"); err == nil {
		t.Fatal("expected an error when neither source answers")
	}
	if _, err := c.Check(context.Background(), "0.0.1"); err == nil {
		t.Fatal("an unpackaged build has no version to compare")
	}
}
