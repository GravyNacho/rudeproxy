package proxy

import (
	"strings"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"valid", Config{Listen: ":8080", Upstream: "localhost:9000"}, false},
		{"missing listen", Config{Upstream: "localhost:9000"}, true},
		{"missing upstream", Config{Listen: ":8080"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseUpstreamHidesCredentials(t *testing.T) {
	_, err := parseUpstream("https://user:secret@host:abc")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error leaks credentials: %v", err)
	}
}
