package otlp

import (
	"log"
	"net/url"
	"os"
	"sort"
	"strings"
)

// otelResourceAttributes adds only identities the user explicitly configured.
// Both signals use it; a malformed variable leaves the default resource in
// use, as the OTel resource SDK specification recommends.
func otelResourceAttributes() []otelAttribute {
	values := map[string]string{"service.name": "magpie"}
	if raw := strings.TrimSpace(os.Getenv("OTEL_RESOURCE_ATTRIBUTES")); raw != "" {
		for _, pair := range strings.Split(raw, ",") {
			key, value, found := strings.Cut(pair, "=")
			key, keyErr := url.PathUnescape(strings.TrimSpace(key))
			value, valueErr := url.PathUnescape(strings.TrimSpace(value))
			if !found || key == "" || keyErr != nil || valueErr != nil {
				// Do not log the contents: attributes may contain private data.
				log.Print("otel: invalid OTEL_RESOURCE_ATTRIBUTES; using the default resource")
				return []otelAttribute{otelString("service.name", "magpie")}
			}
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	attrs := make([]otelAttribute, 0, len(keys))
	for _, key := range keys {
		attrs = append(attrs, otelString(key, values[key]))
	}
	return attrs
}
