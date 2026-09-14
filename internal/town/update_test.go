package town

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type responseTransport struct {
	status int
	body   string
}

func (r responseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: r.status, Status: http.StatusText(r.status), Body: io.NopCloser(strings.NewReader(r.body)), Header: make(http.Header), Request: req}, nil
}

func TestNewerVersion(t *testing.T) {
	for _, test := range []struct {
		current, latest string
		newer           bool
	}{{"v1.2.3", "1.2.4", true}, {"1.9.9", "2.0.0", true}, {"1.2.3", "1.2.3", false}, {"2.0.0", "1.99.0", false}, {"dev", "9.0.0", false}, {"1.0.0-rc.1", "1.0.0", false}} {
		if got := NewerVersion(test.current, test.latest); got != test.newer {
			t.Fatalf("NewerVersion(%q, %q) = %v, want %v", test.current, test.latest, got, test.newer)
		}
	}
}

func TestCheckUpdateOffersPinnedCommand(t *testing.T) {
	client := &http.Client{Transport: responseTransport{status: http.StatusOK, body: `{"version":"1.4.0"}`}}
	notice, err := CheckUpdate(context.Background(), client, "v1.3.2")
	if err != nil {
		t.Fatal(err)
	}
	if notice == nil || notice.Latest != "1.4.0" || notice.Command != "npm install -g @brokkai/brokk-town@1.4.0" {
		t.Fatalf("unexpected update notice: %+v", notice)
	}
}

func TestCheckUpdateSkipsDevelopmentBuildWithoutNetwork(t *testing.T) {
	notice, err := CheckUpdate(context.Background(), &http.Client{Transport: responseTransport{status: http.StatusOK, body: `{}`}}, "dev")
	if err != nil || notice != nil {
		t.Fatalf("development build returned notice=%+v err=%v", notice, err)
	}
}
