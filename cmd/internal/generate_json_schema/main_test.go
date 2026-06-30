package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateIncludesOptionVariants(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	generator := newGenerator(root)
	if err := generator.load(); err != nil {
		t.Fatal(err)
	}
	content, err := generator.generate()
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	defs, isMap := document["$defs"].(map[string]any)
	if !isMap {
		t.Fatal("missing $defs")
	}
	for _, name := range []string{"Inbound", "Outbound", "DNSServerOptions", "Rule", "HTTPClientOptions"} {
		if _, loaded := defs[name]; !loaded {
			t.Fatalf("missing definition %s", name)
		}
	}
	output := string(content)
	for _, marker := range []string{
		`"const": "direct"`,
		`"const": "hysteria2"`,
		`"const": "route-options"`,
		`"const": "udp"`,
	} {
		if !strings.Contains(output, marker) {
			t.Fatalf("missing schema marker %s", marker)
		}
	}
}
