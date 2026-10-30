// Build-only customization: preserve the shared module cache and fail
// closed when the reviewed upstream reply implementation changes.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const reviewedReplySHA256 = "816ef12c47f89cf30c49eae7acf1add8b791bb7f16689cdbaf440c2e1f3be6d9"
const coreDirectory = ".build/pomerium-core"
const reviewedHTTPErrorSHA256 = "46ca3f7b08e01cdfa62cdaeb0002d1105fc7a0e6b832494462fd3155fe659f0e"

func neutralReply(source []byte) ([]byte, error) {
	hash := sha256.Sum256(source)
	if hex.EncodeToString(hash[:]) != reviewedReplySHA256 {
		return nil, fmt.Errorf("upstream local-reply source changed; compatibility review required")
	}
	s := string(source)
	if strings.Count(s, `"%RESPONSE_CODE_DETAILS%"`) != 3 {
		return nil, fmt.Errorf("unexpected local-reply detail format")
	}
	s = strings.ReplaceAll(s, `"%RESPONSE_CODE_DETAILS%"`, `"Request could not be completed."`)
	needle := "\n\tjsonBody, err := json.MarshalIndent(map[string]any{"
	if strings.Count(s, needle) != 1 {
		return nil, fmt.Errorf("unexpected local-reply header layout")
	}
	headers := `
	// Public gateway-generated errors must not be stored by browsers or CDNs.
	for _, key := range []string{"cache-control", "cdn-cache-control", "cloudflare-cdn-cache-control"} {
		headers = append(headers, &envoy_config_core_v3.HeaderValueOption{
			Header: &envoy_config_core_v3.HeaderValue{Key: key, Value: "no-store"},
			AppendAction: envoy_config_core_v3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		})
	}
`
	return []byte(strings.Replace(s, needle, headers+needle, 1)), nil
}

func neutralHTTPError(source []byte) ([]byte, error) {
	hash := sha256.Sum256(source)
	if hex.EncodeToString(hash[:]) != reviewedHTTPErrorSHA256 {
		return nil, fmt.Errorf("upstream HTTP error source changed; compatibility review required")
	}
	s := string(source)
	needle := "\n\tif r.Header.Get(\"Accept\") == \"application/json\" {"
	if strings.Count(s, needle) != 1 {
		return nil, fmt.Errorf("unexpected HTTP error renderer layout")
	}
	sanitized := `
	// Keep the original error in private logs above; redact only the response.
	response.Description = "Request could not be completed."
	response.DebugURL = nil
	response.PolicyEvaluationTraces = nil
	response.CanDebug = false
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "no-store")
`
	return []byte(strings.Replace(s, needle, sanitized+needle, 1)), nil
}

func wrapTemplate(original, page []byte) ([]byte, error) {
	if strings.Contains(string(original), `{{if eq .Data.page "Error"}}`) {
		return nil, fmt.Errorf("error template is already wrapped")
	}
	// Envoy interprets percent signs as substitution commands. Static page
	// content deliberately contains no percent signs or request-derived values.
	if strings.Contains(string(page), "%") || strings.Contains(string(page), "{{") {
		return nil, fmt.Errorf("neutral page contains dynamic/template substitutions")
	}
	result := []byte(`{{if eq .Data.page "Error"}}` + string(page) + `{{else}}` + string(original) + `{{end}}`)
	if _, err := template.New("index").Parse(string(result)); err != nil {
		return nil, fmt.Errorf("generated error template is invalid: %w", err)
	}
	return result, nil
}

func run(mode string) error {
	switch mode {
	case "source":
		cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/pomerium/pomerium")
		raw, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("resolve build-only core source: %w", err)
		}
		corePath := strings.TrimSpace(string(raw))
		expected, err := filepath.Abs(coreDirectory)
		if err != nil {
			return err
		}
		if filepath.Clean(corePath) != expected {
			return fmt.Errorf("refusing to modify anything outside the isolated build-only core copy")
		}
		for _, change := range []struct {
			path    string
			rewrite func([]byte) ([]byte, error)
		}{
			{"config/envoyconfig/local_reply.go", neutralReply},
			{"internal/httputil/errors.go", neutralHTTPError},
		} {
			path := filepath.Join(corePath, change.path)
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			patched, err := change.rewrite(source)
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, patched, 0644); err != nil {
				return err
			}
		}
		test, err := os.ReadFile("tools/neutral-errors/httputil_test.go.txt")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(corePath, "internal/httputil/neutral_public_errors_test.go"), test, 0644)
	case "template":
		path := "internal/ui/dist/index.gohtml"
		original, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		page, err := os.ReadFile("tools/neutral-errors/error.gohtml")
		if err != nil {
			return err
		}
		wrapped, err := wrapTemplate(original, page)
		if err != nil {
			return err
		}
		return os.WriteFile(path, wrapped, 0644)
	default:
		return fmt.Errorf("expected source or template")
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "expected source or template")
		os.Exit(1)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
