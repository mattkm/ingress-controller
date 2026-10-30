package main

import (
	"bytes"
	"html/template"
	"os"
	"strings"
	"testing"
)

func TestNeutralTemplate(t *testing.T) {
	page, err := os.ReadFile("error.gohtml")
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`<title>{{.Title}}</title><script>window.data={{.Data}}</script>`)
	wrapped, err := wrapTemplate(original, page)
	if err != nil {
		t.Fatal(err)
	}
	tpl := template.Must(template.New("test").Parse(string(wrapped)))
	for _, name := range []string{"Error", "UserInfo", "SignOutConfirm"} {
		var out bytes.Buffer
		if err := tpl.Execute(&out, map[string]any{"Title": "Original title", "Data": map[string]any{"page": name, "statusText": "PRIVATE_DETAIL"}}); err != nil {
			t.Fatal(err)
		}
		if name == "Error" {
			if strings.Contains(out.String(), "PRIVATE_DETAIL") || strings.Contains(out.String(), "Original title") || strings.Contains(out.String(), "<script") {
				t.Fatal("error leaked original template")
			}
		} else if !strings.Contains(out.String(), "Original title") || !strings.Contains(out.String(), "PRIVATE_DETAIL") {
			t.Fatal("non-error page changed")
		}
	}
	if _, err := wrapTemplate(wrapped, page); err == nil {
		t.Fatal("double wrapping accepted")
	}
	for _, invalid := range []string{"100%", "{{.Data}}"} {
		if _, err := wrapTemplate(original, []byte(invalid)); err == nil {
			t.Fatal("dynamic neutral page accepted")
		}
	}
}

func TestNeutralOverlayRefusesUnreviewedSource(t *testing.T) {
	if _, err := neutralReply([]byte("different upstream source")); err == nil {
		t.Fatal("unreviewed dependency accepted")
	}
}
