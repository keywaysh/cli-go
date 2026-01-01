package version

import "testing"

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name     string
		latest   string
		current  string
		expected bool
	}{
		// Basic comparisons
		{"newer major", "v2.0.0", "v1.0.0", true},
		{"newer minor", "v1.1.0", "v1.0.0", true},
		{"newer patch", "v1.0.1", "v1.0.0", true},
		{"same version", "v1.0.0", "v1.0.0", false},
		{"older major", "v1.0.0", "v2.0.0", false},
		{"older minor", "v1.0.0", "v1.1.0", false},
		{"older patch", "v1.0.0", "v1.0.1", false},

		// Without 'v' prefix
		{"no prefix newer", "1.1.0", "1.0.0", true},
		{"no prefix same", "1.0.0", "1.0.0", false},
		{"no prefix older", "1.0.0", "1.1.0", false},

		// Mixed prefixes
		{"mixed prefix newer", "v1.1.0", "1.0.0", true},
		{"mixed prefix same", "1.0.0", "v1.0.0", false},

		// With suffixes (dirty, dev, etc.)
		{"dirty suffix newer", "v1.1.0", "v1.0.0-dirty", true},
		{"dev suffix same", "v1.0.0", "v1.0.0-dev", false},
		{"prerelease newer", "v1.1.0-beta", "v1.0.0", true},

		// Two-part versions
		{"two parts newer", "1.1", "1.0", true},
		{"two parts same", "1.0", "1.0", false},

		// Edge cases
		{"empty latest", "", "1.0.0", false},
		{"empty current", "1.0.0", "", false},
		{"both empty", "", "", false},
		{"dev version", "v1.0.0", "dev", false},
		{"invalid latest", "abc", "1.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsNewerVersion(tt.latest, tt.current)
			if result != tt.expected {
				t.Errorf("IsNewerVersion(%q, %q) = %v, want %v",
					tt.latest, tt.current, result, tt.expected)
			}
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected []int
	}{
		{"1.2.3", []int{1, 2, 3}},
		{"v1.2.3", []int{1, 2, 3}},
		{"1.2", []int{1, 2}},
		{"1", []int{1}},
		{"1.2.3-dirty", []int{1, 2, 3}},
		{"1.2.3+build", []int{1, 2, 3}},
		{"v1.2.3-beta.1", []int{1, 2, 3}},
		{"", []int{}},
		{"dev", []int{}},
		{"abc.def", []int{}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseVersion(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("parseVersion(%q) = %v, want %v",
					tt.input, result, tt.expected)
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("parseVersion(%q) = %v, want %v",
						tt.input, result, tt.expected)
					return
				}
			}
		})
	}
}

func TestGetUpdateCommand(t *testing.T) {
	tests := []struct {
		method   InstallMethod
		expected string
	}{
		{InstallMethodNPM, "npm update -g @keywaysh/cli"},
		{InstallMethodHomebrew, "brew upgrade keyway"},
		{InstallMethodBinary, "curl -fsSL https://keyway.sh/install.sh | sh"},
	}

	for _, tt := range tests {
		t.Run(string(tt.method), func(t *testing.T) {
			result := GetUpdateCommand(tt.method)
			if result != tt.expected {
				t.Errorf("GetUpdateCommand(%q) = %q, want %q",
					tt.method, result, tt.expected)
			}
		})
	}
}
