package bridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func configWithMode(value string) string {
	return strings.TrimSuffix(validConfig, "}") + `,"routingMode":` + value + `}`
}

func TestRoutingConfigOptionalStrictMode(t *testing.T) {
	for _, tt := range []struct{ value, want string }{{"", "global"}, {`"global"`, "global"}, {`"bypass_cn"`, "bypass_cn"}} {
		raw := validConfig
		if tt.value != "" {
			raw = configWithMode(tt.value)
		}
		c, err := parseConfig(raw)
		if err != nil {
			t.Errorf("%s: %v", tt.value, err)
			continue
		}
		wire, _ := json.Marshal(c)
		var fields map[string]any
		_ = json.Unmarshal(wire, &fields)
		if fields["routingMode"] != tt.want {
			t.Errorf("%s: effective mode = %v, want %s", tt.value, fields["routingMode"], tt.want)
		}
	}
	for _, value := range []string{`null`, `""`, `"GLOBAL"`, `" bypass_cn"`, `"direct"`, `1`, `true`, `{}`, `[]`} {
		if err := Validate(configWithMode(value)); err != errConfig {
			t.Errorf("accepted invalid mode %s: %v", value, err)
		}
	}
	for _, raw := range []string{
		strings.TrimSuffix(configWithMode(`"global"`), "}") + `,"routingMode":"bypass_cn"}`,
		strings.Replace(configWithMode(`"global"`), `"routingMode"`, `"RoutingMode"`, 1),
		strings.Replace(configWithMode(`"global"`), `"expiresAtUtc":null,`, "", 1),
		configWithMode(`"global"`) + ` {}`,
	} {
		if Validate(raw) != errConfig {
			t.Errorf("accepted non-strict envelope")
		}
	}
}
