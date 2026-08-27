package gui

import "testing"

func TestDisplayAvailable(t *testing.T) {
	lookup := func(vars map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			v, ok := vars[key]
			return v, ok
		}
	}

	cases := []struct {
		name string
		vars map[string]string
		want bool
	}{
		{"both unset", map[string]string{}, false},
		{"DISPLAY set", map[string]string{"DISPLAY": ":0"}, true},
		{"WAYLAND_DISPLAY set", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, true},
		{"both set", map[string]string{"DISPLAY": ":0", "WAYLAND_DISPLAY": "wayland-0"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayAvailable(lookup(tc.vars)); got != tc.want {
				t.Errorf("displayAvailable() = %v, want %v", got, tc.want)
			}
		})
	}
}
