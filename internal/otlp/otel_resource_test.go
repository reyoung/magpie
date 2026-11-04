package otlp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/settings"
)

// Check the collector's actual payloads for both signals, rather than only
// parsing the variable: a configured server identity must reach every export.
func TestOTelResourceAttributes(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		want      map[string]string
	}{
		{"default", "", map[string]string{"service.name": "magpie"}},
		{"whitespace", " \t ", map[string]string{"service.name": "magpie"}},
		{"server", "service.instance.id=magpie-prod-01,host.name=server-a,deployment.environment.name=production", map[string]string{
			"service.name": "magpie", "service.instance.id": "magpie-prod-01", "host.name": "server-a", "deployment.environment.name": "production",
		}},
		{"override and duplicate", "service.name=first,service.name=custom,host.name=old,host.name=new", map[string]string{"service.name": "custom", "host.name": "new"}},
		{"encoding and empty", " label%2Ckey = a%2Cb%3Dc%20d+e , empty=,host.name=%E6%9C%BA%E5%99%A8", map[string]string{
			"service.name": "magpie", "label,key": "a,b=c d+e", "empty": "", "host.name": "机器",
		}},
		{"explicit empty service", "service.name=", map[string]string{"service.name": ""}},
		{"missing equals", "host.name=server-a,broken", map[string]string{"service.name": "magpie"}},
		{"empty key", "host.name=server-a, =value", map[string]string{"service.name": "magpie"}},
		{"bad key escape", "host.name=server-a,bad%ZZ=value", map[string]string{"service.name": "magpie"}},
		{"bad value escape", "host.name=server-a,label=bad%ZZ", map[string]string{"service.name": "magpie"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			type received struct {
				path string
				body []byte
			}
			requests := make(chan received, 2)
			collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				requests <- received{r.URL.Path, body}
				io.WriteString(w, `{}`)
			}))
			defer collector.Close()
			otelConfig(t, settings.OTel{Endpoint: collector.URL, Metrics: true})
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", tc.env)
			config, err := settings.OTelExport()
			if err != nil {
				t.Fatal(err)
			}
			e := newOTelExporter()
			defer e.cancel()
			defer e.client.CloseIdleConnections()
			e.flush([]otelItem{{record: Record{Time: time.Now(), Status: 200, Input: 1}, config: config}})
			for _, signal := range []string{"Spans", "Metrics"} {
				var request received
				select {
				case request = <-requests:
				default:
					t.Fatalf("collector received no %s export", signal)
				}
				path := "/v1/traces"
				if signal == "Metrics" {
					path = "/v1/metrics"
				}
				if request.path != path {
					t.Fatalf("path = %q, want %q", request.path, path)
				}
				var payload map[string][]struct {
					Resource struct {
						Attributes []otelAttribute `json:"attributes"`
					} `json:"resource"`
				}
				if err := json.Unmarshal(request.body, &payload); err != nil {
					t.Fatal(err)
				}
				resources := payload["resource"+signal]
				if len(resources) != 1 {
					t.Fatalf("%s: got %d resources, want 1", signal, len(resources))
				}
				got := make(map[string]string)
				for _, attr := range resources[0].Resource.Attributes {
					value, ok := attr.Value["stringValue"].(string)
					if !ok {
						t.Fatalf("%s: attribute %q is not a string: %v", signal, attr.Key, attr.Value)
					}
					if _, exists := got[attr.Key]; exists {
						t.Fatalf("%s: duplicate attribute %q", signal, attr.Key)
					}
					got[attr.Key] = value
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("%s resource attributes = %v, want %v", signal, got, tc.want)
				}
			}
		})
	}
}
