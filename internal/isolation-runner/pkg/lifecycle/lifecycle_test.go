package lifecycle

import (
	"testing"
)

func TestGenerateChainName(t *testing.T) {
	tests := []struct {
		name        string
		containerID string
		want        string
	}{
		{
			"normal container ID",
			"abc123def456789012345678",
			"ISO-abc123def4567890",
		},
		{
			"with special chars",
			"abc-123-def-456",
			"ISO-abc123def456",
		},
		{
			"short ID",
			"abc123",
			"ISO-abc123",
		},
		{
			"uppercase hex",
			"ABC123DEF456",
			"ISO-ABC123DEF456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateChainName(tt.containerID)
			if got != tt.want {
				t.Errorf("GenerateChainName() = %v, want %v", got, tt.want)
			}
			if len(got) > 20 {
				t.Errorf("GenerateChainName() length = %d, max 20", len(got))
			}
		})
	}
}
