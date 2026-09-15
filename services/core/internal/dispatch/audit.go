package dispatch

import (
	"encoding/json"
	"net/url"
	"regexp"
)

// sensitiveTargetKey matches target_config fields that are credentials rather
// than routing metadata. Slack and Teams incoming-webhook URLs carry their token
// in the path, so anything URL-shaped is stripped to scheme://host as well.
var sensitiveTargetKey = regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|authorization|webhook|(^|_)url$|(^|/)url$)`)

// redactTargetConfig returns a copy of an integration target config that is safe
// to write into the audit log, which every member of the organization can read.
// Routing metadata (channel, project key, team id) survives; credentials do not.
func redactTargetConfig(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var config map[string]any
	if err := json.Unmarshal(raw, &config); err != nil {
		// Not an object we can walk key by key, so it cannot be filtered: keep
		// the shape of the config without its contents.
		return json.RawMessage(`{"_redacted":"non-object target config"}`)
	}
	redacted, err := json.Marshal(redactMap(config))
	if err != nil {
		return json.RawMessage(`{"_redacted":"unserializable target config"}`)
	}
	return redacted
}

func redactMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if sensitiveTargetKey.MatchString(k) {
			out[k] = redactValue(v)
			continue
		}
		out[k] = redactNested(v)
	}
	return out
}

// redactNested walks containers so credentials nested inside an object or array
// are caught too, while scalars pass through untouched.
func redactNested(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return redactMap(t)
	case []any:
		items := make([]any, len(t))
		for i, item := range t {
			items[i] = redactNested(item)
		}
		return items
	default:
		return v
	}
}

func redactValue(v any) any {
	s, ok := v.(string)
	if !ok {
		return "[redacted]"
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") {
		// The host names the integration; everything after it can be a secret.
		return u.Scheme + "://" + u.Host + "/[redacted]"
	}
	return "[redacted]"
}
