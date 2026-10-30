//go:build embed_pomerium

package cmd

import (
	"strings"
	"testing"

	_ "github.com/pomerium/ingress-controller/internal"
	"github.com/pomerium/pomerium/config"
	"github.com/pomerium/pomerium/config/envoyconfig"
	"github.com/pomerium/pomerium/ui"
)

// Run with the production embed_pomerium tag after build-only customization.
func TestNeutralErrorPages(t *testing.T) {
	data := map[string]any{
		"status": 503, "statusText": "PRIVATE_CONNECTION_DETAIL",
		"requestId": "PRIVATE_REQUEST_ID", "responseFlags": "PRIVATE_RESPONSE_FLAGS",
	}
	body, err := ui.RenderPage("Error", "Error", data)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)
	for _, forbidden := range []string{"PRIVATE_", "pomerium", "upstream", "<script", "%", "http://", "https://"} {
		if strings.Contains(strings.ToLower(html), strings.ToLower(forbidden)) {
			t.Fatalf("neutral page contains %q", forbidden)
		}
	}
	if !strings.Contains(html, "We couldn't complete this request") {
		t.Fatal("neutral page missing")
	}
	// Other page templates continue to use the original application.
	other, err := ui.RenderPage("UserInfo", "User Info", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(other), "We couldn't complete this request") {
		t.Fatal("non-error page replaced")
	}
}

func TestNeutralLocalReplies(t *testing.T) {
	builder := new(envoyconfig.Builder)
	reply, err := builder.BuildLocalReplyConfig(&config.Options{Services: "proxy"})
	if err != nil {
		t.Fatal(err)
	}
	var htmlCount, jsonCount, grpcCount int
	for _, mapper := range reply.GetMappers() {
		if mapper.GetStatusCode() != nil {
			t.Fatal("HTTP error status override added")
		}
		format := mapper.GetBodyFormatOverride()
		body := string(format.GetTextFormatSource().GetInlineBytes())
		if strings.Contains(body, "%RESPONSE_CODE_DETAILS%") {
			t.Fatal("connection details remain in response body")
		}
		cache := false
		for _, header := range mapper.GetHeadersToAdd() {
			if header.GetHeader().GetKey() == "cache-control" && header.GetHeader().GetValue() == "no-store" {
				cache = true
			}
			if header.GetHeader().GetKey() == "grpc-status" {
				grpcCount++
			}
		}
		if !cache {
			t.Fatal("local error response may be cached")
		}
		switch {
		case strings.HasPrefix(format.GetContentType(), "text/html"):
			htmlCount++
			if strings.Contains(strings.ToLower(body), "pomerium") || strings.Contains(body, "%RESPONSE_FLAGS%") {
				t.Fatal("HTML infrastructure details remain")
			}
		case strings.HasPrefix(format.GetContentType(), "application/json"), strings.HasPrefix(format.GetContentType(), "text/plain"):
			jsonCount++
			if !strings.Contains(body, "%RESPONSE_CODE%") || !strings.Contains(body, "Request could not be completed.") {
				t.Fatal("structured error changed status or kept details")
			}
		}
	}
	if htmlCount != 1 || jsonCount != 2 || grpcCount == 0 {
		t.Fatal("expected HTML, structured and gRPC mappings")
	}
}
