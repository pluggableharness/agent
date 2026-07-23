package telemetry

import (
	"errors"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"default config", DefaultConfig, false},
		{"sampling ratio negative", withSamplingRatio(DefaultConfig, -0.1), true},
		{"sampling ratio above one", withSamplingRatio(DefaultConfig, 1.1), true},
		{"sampling ratio zero is valid", withSamplingRatio(DefaultConfig, 0), false},
		{"sampling ratio one is valid", withSamplingRatio(DefaultConfig, 1), false},
		{"negative export interval", withExportInterval(DefaultConfig, -time.Second), true},
		{"zero export interval is valid", withExportInterval(DefaultConfig, 0), false},
		{"enabled with empty service name", withServiceName(DefaultConfig, ""), true},
		{"disabled with empty service name is valid", disabledWithServiceName(DefaultConfig, ""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("Validate() error = %v, want errors.Is ErrInvalidConfig", err)
			}
		})
	}
}

func withSamplingRatio(cfg Config, ratio float64) Config {
	cfg.SamplingRatio = ratio
	return cfg
}

func withExportInterval(cfg Config, d time.Duration) Config {
	cfg.ExportInterval = d
	return cfg
}

func withServiceName(cfg Config, name string) Config {
	cfg.ServiceName = name
	return cfg
}

func disabledWithServiceName(cfg Config, name string) Config {
	cfg.Enabled = false
	cfg.ServiceName = name
	return cfg
}
