package update

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveReleaseSite talks to the real release sources. Opt in with
// FFSWALLET_LIVE=1; it is skipped otherwise so the suite stays offline.
func TestLiveReleaseSite(t *testing.T) {
	if os.Getenv("FFSWALLET_LIVE") == "" {
		t.Skip("set FFSWALLET_LIVE=1 to query the real release site")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	latest, err := Checker{}.Latest(ctx)
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	t.Logf("latest: %+v", latest)
	res, err := Checker{}.Check(ctx, "0.0.1+release")
	if err == nil {
		t.Fatalf("a non-release version must not compare: %+v", res)
	}
	res, err = Checker{}.Check(ctx, "0.0.7")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !res.Outdated {
		t.Fatalf("0.0.7 should be behind %s", latest.Tag)
	}
	t.Logf("check(0.0.7): outdated=%v latest=%s url=%s", res.Outdated, res.Latest.Tag, res.Latest.URL)
}
