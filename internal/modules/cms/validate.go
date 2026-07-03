package cms

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Document validation against a Collection's field schema — a Go port of
// Django's services/validator.py. Pure functions, no Mongo/Postgres access,
// so this is fully unit-testable (see usecase_test.go).

var (
	emailRe = regexp.MustCompile(`^[\w.+-]+@[\w-]+\.[\w.-]+$`)
	phoneRe = regexp.MustCompile(`^\+?[0-9]{7,15}$`)
)

// ValidationErrors aggregates per-field validation failures, mirroring
// Django's `ValidationError(errors: dict[str, str])`.
type ValidationErrors map[string]string

func (e ValidationErrors) Error() string {
	return fmt.Sprintf("validation failed: %d field(s)", len(e))
}

// validateDocument coerces/validates a raw document against fields.
// partial=true skips required-field enforcement (used for PATCH updates).
// Unknown, non-underscore-prefixed keys pass through unchanged (matches
// Django's "flexible ingestion" comment, e.g. a bot's telegram_id).
func validateDocument(fields []Field, doc map[string]any, partial bool) (map[string]any, ValidationErrors) {
	errs := ValidationErrors{}
	out := map[string]any{}
	known := make(map[string]struct{}, len(fields))

	for _, f := range fields {
		known[f.Name] = struct{}{}
		rules := decodeRules(f.Validators)

		if raw, present := doc[f.Name]; present {
			cv, err := coerceField(f.Type, raw, rules)
			if err != nil {
				errs[f.Name] = err.Error()
				continue
			}
			out[f.Name] = cv
			continue
		}
		if partial {
			continue
		}
		def, hasDefault := decodeDefault(f.Default)
		if f.Required {
			if !hasDefault {
				errs[f.Name] = "required"
				continue
			}
		} else if !hasDefault {
			continue
		}
		cv, err := coerceField(f.Type, def, rules)
		if err != nil {
			errs[f.Name] = err.Error()
			continue
		}
		out[f.Name] = cv
	}

	for k, v := range doc {
		if _, isKnown := known[k]; isKnown {
			continue
		}
		if strings.HasPrefix(k, "_") {
			continue
		}
		out[k] = v
	}

	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// coerceField converts value into the canonical Go representation for kind,
// applying validators rules (min/max/min_length/max_length/regex/choices/array_kind).
func coerceField(kind string, value any, rules map[string]any) (any, error) {
	switch kind {
	case FieldEnum:
		s, err := asText(value)
		if err != nil {
			return nil, err
		}
		if choices := stringSlice(rules["choices"]); len(choices) > 0 && !contains(choices, s) {
			return nil, fmt.Errorf("must be one of %v", choices)
		}
		return s, nil
	case FieldArray:
		arr, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("expected array")
		}
		elemKind, _ := rules["array_kind"].(string)
		if elemKind == "" {
			elemKind = FieldText
		}
		out := make([]any, len(arr))
		for i, v := range arr {
			cv, err := coerceField(elemKind, v, nil)
			if err != nil {
				return nil, err
			}
			out[i] = cv
		}
		return out, nil
	}

	coerced, err := baseCoerce(kind, value)
	if err != nil {
		return nil, err
	}
	if err := checkExtraValidators(kind, coerced, rules); err != nil {
		return nil, err
	}
	return coerced, nil
}

func baseCoerce(kind string, value any) (any, error) {
	switch kind {
	case FieldText, FieldLongText:
		return asText(value)
	case FieldInt:
		return asInt(value)
	case FieldFloat:
		return asFloat(value)
	case FieldBool:
		return asBool(value)
	case FieldDateTime:
		t, err := asDateTime(value)
		if err != nil {
			return nil, err
		}
		return t.Format(time.RFC3339), nil
	case FieldDate:
		t, err := asDate(value)
		if err != nil {
			return nil, err
		}
		return t.Format("2006-01-02"), nil
	case FieldEmail:
		return asEmail(value)
	case FieldURL:
		return asURL(value)
	case FieldPhone:
		return asPhone(value)
	case FieldJSON:
		return value, nil
	case FieldRelation:
		return asText(value)
	case FieldGeo:
		return asGeo(value)
	case FieldFile, FieldImage, FieldAudio, FieldVideo:
		return asUUIDString(value)
	default:
		return asText(value)
	}
}

func checkExtraValidators(kind string, value any, rules map[string]any) error {
	switch kind {
	case FieldText, FieldLongText, FieldEmail, FieldURL, FieldPhone:
		s, _ := value.(string)
		if minLen, ok := intRule(rules, "min_length"); ok && len(s) < minLen {
			return fmt.Errorf("min_length=%d", minLen)
		}
		if maxLen, ok := intRule(rules, "max_length"); ok && len(s) > maxLen {
			return fmt.Errorf("max_length=%d", maxLen)
		}
		if pattern, ok := rules["regex"].(string); ok && pattern != "" {
			if re, err := regexp.Compile(pattern); err == nil && !re.MatchString(s) {
				return fmt.Errorf("regex mismatch")
			}
		}
	case FieldInt:
		n, _ := value.(int64)
		if minV, ok := floatRule(rules, "min"); ok && float64(n) < minV {
			return fmt.Errorf("min=%v", rules["min"])
		}
		if maxV, ok := floatRule(rules, "max"); ok && float64(n) > maxV {
			return fmt.Errorf("max=%v", rules["max"])
		}
	case FieldFloat:
		f, _ := value.(float64)
		if minV, ok := floatRule(rules, "min"); ok && f < minV {
			return fmt.Errorf("min=%v", rules["min"])
		}
		if maxV, ok := floatRule(rules, "max"); ok && f > maxV {
			return fmt.Errorf("max=%v", rules["max"])
		}
	}
	if choices := stringSlice(rules["choices"]); len(choices) > 0 {
		if !contains(choices, fmt.Sprintf("%v", value)) {
			return fmt.Errorf("must be one of %v", choices)
		}
	}
	return nil
}

// ── per-kind coercers (mirrors validator.py's _as_* functions) ────────────

func asText(value any) (string, error) {
	if value == nil {
		return "", fmt.Errorf("value is null")
	}
	if s, ok := value.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", value), nil
}

func asInt(value any) (int64, error) {
	switch v := value.(type) {
	case bool:
		return 0, fmt.Errorf("expected integer, got bool")
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("expected integer")
		}
		return int64(v), nil
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("expected integer")
		}
		return n, nil
	default:
		return 0, fmt.Errorf("expected integer")
	}
}

func asFloat(value any) (float64, error) {
	switch v := value.(type) {
	case bool:
		return 0, fmt.Errorf("expected number, got bool")
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("expected number")
		}
		return f, nil
	default:
		return 0, fmt.Errorf("expected number")
	}
}

func asBool(value any) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case float64:
		return v != 0, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "y", "on":
			return true, nil
		case "false", "0", "no", "n", "off", "":
			return false, nil
		}
	}
	return false, fmt.Errorf("expected boolean")
}

func asDateTime(value any) (time.Time, error) {
	switch v := value.(type) {
	case time.Time:
		return v, nil
	case string:
		s := strings.ReplaceAll(strings.TrimSpace(v), "Z", "+00:00")
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, nil
			}
		}
	case float64:
		return time.Unix(int64(v), 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("expected ISO-8601 datetime")
}

func asDate(value any) (time.Time, error) {
	t, err := asDateTime(value)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
}

func asEmail(value any) (string, error) {
	s, err := asText(value)
	if err != nil {
		return "", err
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if !emailRe.MatchString(s) {
		return "", fmt.Errorf("invalid email")
	}
	return s, nil
}

func asURL(value any) (string, error) {
	s, err := asText(value)
	if err != nil {
		return "", err
	}
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid URL")
	}
	return s, nil
}

func asPhone(value any) (string, error) {
	s, err := asText(value)
	if err != nil {
		return "", err
	}
	r := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "")
	s = r.Replace(strings.TrimSpace(s))
	if !phoneRe.MatchString(s) {
		return "", fmt.Errorf("invalid phone")
	}
	return s, nil
}

func asUUIDString(value any) (string, error) {
	s, err := asText(value)
	if err != nil {
		return "", err
	}
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("invalid uuid")
	}
	return id.String(), nil
}

func asGeo(value any) (map[string]float64, error) {
	switch v := value.(type) {
	case map[string]any:
		lat, err1 := asFloat(v["lat"])
		lng, err2 := asFloat(v["lng"])
		if err1 == nil && err2 == nil {
			return geoRange(lat, lng)
		}
	case []any:
		if len(v) == 2 {
			lat, err1 := asFloat(v[0])
			lng, err2 := asFloat(v[1])
			if err1 == nil && err2 == nil {
				return geoRange(lat, lng)
			}
		}
	case string:
		parts := strings.SplitN(v, ",", 2)
		if len(parts) == 2 {
			lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			lng, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			if err1 == nil && err2 == nil {
				return geoRange(lat, lng)
			}
		}
	}
	return nil, fmt.Errorf("expected geo as {lat,lng} or 'lat,lng'")
}

func geoRange(lat, lng float64) (map[string]float64, error) {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return nil, fmt.Errorf("geo out of range")
	}
	return map[string]float64{"lat": lat, "lng": lng}, nil
}

// ── small helpers ───────────────────────────────────────────────────────

func decodeRules(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

func decodeDefault(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false
	}
	return v, true
}

func intRule(rules map[string]any, key string) (int, bool) {
	switch n := rules[key].(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	}
	return 0, false
}

func floatRule(rules map[string]any, key string) (float64, bool) {
	switch n := rules[key].(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, fmt.Sprintf("%v", e))
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
