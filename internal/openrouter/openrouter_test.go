package openrouter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchCreditsAndKeyUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/credits":
			_, _ = w.Write([]byte(`{"data":{"total_credits":40.0,"total_usage":27.19}}`))
		case "/v1/key":
			_, _ = w.Write([]byte(`{"data":{"label":"k","usage_daily":0.5,"usage_weekly":7.94,"usage_monthly":27.19}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c, err := FetchCredits(srv.URL, "good")
	if err != nil {
		t.Fatal(err)
	}
	if c.TotalCredits != 40 || c.TotalUsage != 27.19 {
		t.Errorf("credits = %+v", c)
	}
	if got := c.Remaining(); got < 12.80 || got > 12.82 {
		t.Errorf("remaining = %f", got)
	}

	u, err := FetchKeyUsage(srv.URL, "good")
	if err != nil {
		t.Fatal(err)
	}
	if u.Daily != 0.5 || u.Weekly != 7.94 || u.Monthly != 27.19 {
		t.Errorf("usage = %+v", u)
	}

	if _, err := FetchCredits(srv.URL, "bad"); err == nil {
		t.Error("non-200 credits fetch should error")
	}
	if err := ValidateKey(srv.URL, "bad"); err == nil {
		t.Error("ValidateKey should reject a non-200")
	}
	if err := ValidateKey(srv.URL, "good"); err != nil {
		t.Errorf("ValidateKey(good) = %v", err)
	}
}
