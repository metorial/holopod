package container

import (
	"testing"
)

func TestParseMemoryLimit(t *testing.T) {
	tests := []struct {
		name    string
		limit   string
		want    int64
		wantErr bool
	}{
		{"megabytes", "256m", 256 * 1024 * 1024, false},
		{"gigabytes", "2g", 2 * 1024 * 1024 * 1024, false},
		{"uppercase M", "256M", 256 * 1024 * 1024, false},
		{"uppercase G", "2G", 2 * 1024 * 1024 * 1024, false},
		{"with spaces", " 128m ", 128 * 1024 * 1024, false},
		{"minimum valid", "4m", 4 * 1024 * 1024, false},
		{"below minimum", "2m", 0, true},
		{"too low bytes", "1024", 0, true},
		{"too low kilobytes", "512k", 0, true},
		{"above maximum", "200g", 0, true},
		{"invalid", "invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMemoryLimit(tt.limit)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseMemoryLimit() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseMemoryLimit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCPULimit(t *testing.T) {
	tests := []struct {
		name    string
		limit   string
		want    int64
		wantErr bool
	}{
		{"1 CPU", "1.0", 1e9, false},
		{"0.5 CPU", "0.5", 5e8, false},
		{"2 CPUs", "2.0", 2e9, false},
		{"0.25 CPU", "0.25", 25e7, false},
		{"minimum valid", "0.01", 1e7, false},
		{"below minimum", "0.005", 0, true},
		{"above maximum", "300", 0, true},
		{"invalid", "invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCPULimit(tt.limit)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCPULimit() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseCPULimit() = %v, want %v", got, tt.want)
			}
		})
	}
}
