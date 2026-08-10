// Package observability provides bounded, redacted process event logging.
package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Format selects the service log encoding written to stderr.
type Format string

const (
	// FormatText writes one compact human-readable event per line.
	FormatText Format = "text"
	// FormatJSON writes one JSON object per line.
	FormatJSON Format = "json"
)

var priorities = map[string]int{
	"error": 0,
	"warn":  1,
	"info":  2,
	"debug": 3,
}

// Fields contains non-sensitive structured event attributes.
type Fields map[string]any

// Logger serializes complete events so concurrent requests cannot interleave.
type Logger struct {
	level  int
	format Format
	writer io.Writer
	now    func() time.Time
	mu     sync.Mutex
}

// Config defines the stable service logging policy.
type Config struct {
	Level  string
	Format string
	Writer io.Writer
	Now    func() time.Time
}

// New validates and constructs a redacting event logger.
func New(config Config) (*Logger, error) {
	level, ok := priorities[config.Level]
	if !ok {
		return nil, fmt.Errorf("unsupported log level %q", config.Level)
	}
	format := Format(config.Format)
	if format == "" {
		format = FormatText
	}
	if format != FormatText && format != FormatJSON {
		return nil, fmt.Errorf("unsupported log format %q", config.Format)
	}
	if config.Writer == nil {
		config.Writer = io.Discard
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Logger{level: level, format: format, writer: config.Writer, now: config.Now}, nil
}

// Enabled reports whether an event at level would be emitted.
func (logger *Logger) Enabled(level string) bool {
	priority, ok := priorities[level]
	return logger != nil && ok && priority <= logger.level
}

// Event writes one bounded event. Sensitive field names are redacted
// recursively even if a future caller accidentally supplies them.
func (logger *Logger) Event(level, event string, fields Fields) {
	if !logger.Enabled(level) {
		return
	}
	entry := make(map[string]any, len(fields)+3)
	entry["time"] = logger.now().UTC().Format(time.RFC3339Nano)
	entry["level"] = level
	entry["event"] = safeEventName(event)
	for key, value := range fields {
		key = safeFieldKey(key)
		if key == "" || key == "time" || key == "level" || key == "event" {
			continue
		}
		entry[key] = redactValue(key, value)
	}

	var line []byte
	if logger.format == FormatJSON {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return
		}
		line = encoded
	} else {
		line = []byte(formatText(entry))
	}
	line = append(line, '\n')
	logger.mu.Lock()
	defer logger.mu.Unlock()
	_, _ = logger.writer.Write(line)
}

func safeEventName(event string) string {
	event = strings.TrimSpace(event)
	if event == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, character := range event {
		switch {
		case character >= 'a' && character <= 'z':
			builder.WriteRune(character)
		case character >= 'A' && character <= 'Z':
			builder.WriteRune(character + ('a' - 'A'))
		case character >= '0' && character <= '9', character == '_', character == '-':
			builder.WriteRune(character)
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func safeFieldKey(key string) string {
	key = strings.TrimSpace(key)
	var builder strings.Builder
	for _, character := range key {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9', character == '_':
			builder.WriteRune(character)
		case character >= 'A' && character <= 'Z':
			builder.WriteRune(character + ('a' - 'A'))
		case character == '-':
			builder.WriteByte('_')
		default:
			builder.WriteByte('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func redactValue(key string, value any) any {
	if sensitiveField(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case Fields:
		result := make(Fields, len(typed))
		for nestedKey, nestedValue := range typed {
			result[nestedKey] = redactValue(nestedKey, nestedValue)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			result[nestedKey] = redactValue(nestedKey, nestedValue)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, nestedValue := range typed {
			result[index] = redactValue("item", nestedValue)
		}
		return result
	default:
		return value
	}
}

func sensitiveField(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, fragment := range []string{
		"authorization", "cookie", "token", "secret", "password",
		"request_body", "response_body", "content", "comment", "query", "search_term",
		"media_bytes", "archive_bytes",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	for _, exact := range []string{"error", "err", "cause", "panic", "stack"} {
		if normalized == exact {
			return true
		}
	}
	return false
}

func formatText(entry map[string]any) string {
	keys := make([]string, 0, len(entry))
	for key := range entry {
		if key != "time" && key != "level" && key != "event" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := []string{
		fmt.Sprintf("time=%s", entry["time"]),
		fmt.Sprintf("level=%s", entry["level"]),
		fmt.Sprintf("event=%s", entry["event"]),
	}
	for _, key := range keys {
		encoded, err := json.Marshal(entry[key])
		if err != nil {
			continue
		}
		parts = append(parts, key+"="+string(encoded))
	}
	return strings.Join(parts, " ")
}
