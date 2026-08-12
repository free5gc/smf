package factory

import "testing"

func TestUPNodeNupfEeApiRootValidation(t *testing.T) {
	tests := []struct {
		name      string
		node      UPNode
		wantRoot  string
		wantError bool
	}{
		{
			name: "omitted",
			node: UPNode{Type: "UPF"},
		},
		{
			name: "normalizes path and trailing slash",
			node: UPNode{
				Type:          "UPF",
				NupfEeApiRoot: stringPtr("https://upf.example.com/api//"),
			},
			wantRoot: "https://upf.example.com/api",
		},
		{
			name: "empty rejected",
			node: UPNode{
				Type:          "UPF",
				NupfEeApiRoot: stringPtr(""),
			},
			wantError: true,
		},
		{
			name: "AN rejected",
			node: UPNode{
				Type:          "AN",
				NupfEeApiRoot: stringPtr("https://upf.example.com"),
			},
			wantError: true,
		},
		{
			name: "query rejected",
			node: UPNode{
				Type:          "UPF",
				NupfEeApiRoot: stringPtr("https://upf.example.com?x=1"),
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.node.validate()
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantRoot != "" {
				if tt.node.NupfEeApiRoot == nil || *tt.node.NupfEeApiRoot != tt.wantRoot {
					t.Fatalf("root mismatch: got %v want %q", tt.node.NupfEeApiRoot, tt.wantRoot)
				}
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}
