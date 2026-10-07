package mjolnir

import "testing"

// The Mjolnir settings are named SLOPCOP_SQUAD_MJOLNIR_*. An install from
// before the rename still sets BT_MJOLNIR_*, which is read when the new name
// is unset; the new name wins when both are set.
func TestEnvironmentReadsPreRenameNames(t *testing.T) {
	for _, test := range []struct {
		name string
		env  map[string]string
		want Connection
	}{
		{name: "new names", env: map[string]string{"SLOPCOP_SQUAD_MJOLNIR_API_URL": "http://127.0.0.1:1/api/v1", "SLOPCOP_SQUAD_MJOLNIR_TOKEN_FILE": "new"}, want: Connection{"http://127.0.0.1:1/api/v1", "new"}},
		{name: "pre-rename names", env: map[string]string{"BT_MJOLNIR_API_URL": "http://127.0.0.1:2/api/v1", "BT_MJOLNIR_TOKEN_FILE": "old"}, want: Connection{"http://127.0.0.1:2/api/v1", "old"}},
		{name: "new name wins", env: map[string]string{"SLOPCOP_SQUAD_MJOLNIR_TOKEN_FILE": "new", "BT_MJOLNIR_TOKEN_FILE": "old"}, want: Connection{"", "new"}},
		{name: "unset", want: Connection{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"SLOPCOP_SQUAD_MJOLNIR_API_URL", "SLOPCOP_SQUAD_MJOLNIR_TOKEN_FILE", "BT_MJOLNIR_API_URL", "BT_MJOLNIR_TOKEN_FILE"} {
				t.Setenv(key, test.env[key])
			}
			if got := Environment(); got != test.want {
				t.Fatalf("Environment() = %+v, want %+v", got, test.want)
			}
		})
	}
	t.Setenv("SLOPCOP_SQUAD_MJOLNIR_COMMAND", "")
	t.Setenv("BT_MJOLNIR_COMMAND", `["mj"]`)
	if got := Setting("SLOPCOP_SQUAD_MJOLNIR_COMMAND"); got != `["mj"]` {
		t.Fatalf("Setting(command) = %q, want the pre-rename value", got)
	}
}
