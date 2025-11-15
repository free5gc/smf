package util_test

import (
	"testing"

	"github.com/free5gc/smf/internal/util"
)

func TestIsValidSupi(t *testing.T) {
	tests := []struct {
		name     string
		supi     string
		expected bool
	}{
		// Valid IMSI cases
		{
			name:     "Valid IMSI with minimum length (5 digits)",
			supi:     "imsi-12345",
			expected: true,
		},
		{
			name:     "Valid IMSI with medium length (10 digits)",
			supi:     "imsi-1234567890",
			expected: true,
		},
		{
			name:     "Valid IMSI with maximum length (15 digits)",
			supi:     "imsi-123456789012345",
			expected: true,
		},
		{
			name:     "Valid IMSI with typical length",
			supi:     "imsi-208930000000001",
			expected: true,
		},

		// Invalid IMSI cases
		{
			name:     "Invalid IMSI - too short (4 digits)",
			supi:     "imsi-1234",
			expected: false,
		},
		{
			name:     "Invalid IMSI - too long (16 digits)",
			supi:     "imsi-1234567890123456",
			expected: false,
		},
		{
			name:     "Invalid IMSI - contains letters",
			supi:     "imsi-12345abc",
			expected: false,
		},
		{
			name:     "Invalid IMSI - contains special characters",
			supi:     "imsi-12345-67890",
			expected: false,
		},
		{
			name:     "Invalid IMSI - contains spaces",
			supi:     "imsi-12345 67890",
			expected: false,
		},
		{
			name:     "Invalid IMSI - empty after prefix",
			supi:     "imsi-",
			expected: false,
		},

		// Valid NAI cases
		{
			name:     "Valid NAI with simple content",
			supi:     "nai-user@example.com",
			expected: true,
		},
		{
			name:     "Valid NAI with complex content",
			supi:     "nai-user.name@domain.example.com",
			expected: true,
		},
		{
			name:     "Valid NAI with minimum content",
			supi:     "nai-a",
			expected: true,
		},
		{
			name:     "Valid NAI with numbers",
			supi:     "nai-user123@domain.com",
			expected: true,
		},
		{
			name:     "Valid NAI with special characters",
			supi:     "nai-user+tag@example.com",
			expected: true,
		},

		// Invalid NAI cases
		{
			name:     "Invalid NAI - empty after prefix",
			supi:     "nai-",
			expected: false,
		},

		// Invalid prefix cases
		{
			name:     "Invalid prefix - wrong prefix",
			supi:     "msisdn-123456789",
			expected: false,
		},
		{
			name:     "Invalid - no prefix",
			supi:     "123456789",
			expected: false,
		},
		{
			name:     "Invalid - empty string",
			supi:     "",
			expected: false,
		},
		{
			name:     "Invalid - only prefix part",
			supi:     "imsi",
			expected: false,
		},
		{
			name:     "Invalid - case sensitive prefix (uppercase)",
			supi:     "IMSI-123456789",
			expected: false,
		},
		{
			name:     "Invalid - case sensitive prefix (mixed)",
			supi:     "Imsi-123456789",
			expected: false,
		},

		// Edge cases
		{
			name:     "Edge case - IMSI exact minimum boundary (10 chars total)",
			supi:     "imsi-12345",
			expected: true,
		},
		{
			name:     "Edge case - IMSI exact maximum boundary (20 chars total)",
			supi:     "imsi-123456789012345",
			expected: true,
		},
		{
			name:     "Edge case - IMSI one below minimum (9 chars total)",
			supi:     "imsi-1234",
			expected: false,
		},
		{
			name:     "Edge case - IMSI one above maximum (21 chars total)",
			supi:     "imsi-1234567890123456",
			expected: false,
		},
		{
			name:     "Edge case - NAI minimum valid length (5 chars total)",
			supi:     "nai-a",
			expected: true,
		},
		{
			name:     "Edge case - NAI at boundary (4 chars total)",
			supi:     "nai-",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := util.IsValidSupi(tt.supi)
			if result != tt.expected {
				t.Errorf("IsValidSupi(%q) = %v, expected %v", tt.supi, result, tt.expected)
			}
		})
	}
}
