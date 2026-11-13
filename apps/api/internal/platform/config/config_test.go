package config

import "testing"

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		addr    string
	}{
		{name: "defaults", env: map[string]string{"DATABASE_URL": "postgres://x"}, addr: ":8080"},
		{name: "cloud run port", env: map[string]string{"DATABASE_URL": "postgres://x", "PORT": "9090"}, addr: ":9090"},
		{name: "missing database url", env: map[string]string{}, wantErr: true},
		{name: "unknown env", env: map[string]string{"DATABASE_URL": "postgres://x", "APP_ENV": "dev"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"DATABASE_URL", "PORT", "APP_ENV", "HTTP_ADDR"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && cfg.HTTPAddr != tt.addr {
				t.Errorf("HTTPAddr = %q, want %q", cfg.HTTPAddr, tt.addr)
			}
		})
	}
}
