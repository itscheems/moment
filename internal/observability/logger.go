// Package observability provides bounded, redacted process event logging.
package observability

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
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

	redactedValue       = "[REDACTED]"
	maxLogValueDepth    = 8
	maxLogCollectionLen = 64
	maxLogStringBytes   = 4096
	maxLogNameBytes     = 128
)

var priorities = map[string]int{
	"error": 0,
	"warn":  1,
	"info":  2,
	"debug": 3,
}

// Fields contains non-sensitive structured event attributes.
type Fields map[string]any

// Surface names the client entry point an operation arrived through.
//
// It answers "where did this come from", not "which binary is running" — the
// difference matters because `moment serve` is a CLI process whose every
// browser request originates on the web. Classifying by process would stamp
// every page interaction as cli, which is the same as not classifying at all.
//
// The set is open on purpose. A native client adds a constant here and nothing
// downstream has to learn about it: no consumer switches exhaustively on this
// value, and the field is a plain string on the wire.
type Surface string

const (
	// SurfaceCLI marks an operation a person invoked in a terminal.
	SurfaceCLI Surface = "cli"
	// SurfaceWeb marks an operation that arrived over HTTP from the browser.
	SurfaceWeb Surface = "web"
	// SurfaceAPI marks a programmatic HTTP client: a script, curl, anything
	// holding a bearer token that has not said what it is. It is deliberately
	// separate from web — both arrive over HTTP, and collapsing them would
	// make web mean "HTTP", which is the distinction this type exists to draw.
	SurfaceAPI Surface = "api"
	// SurfaceSystem marks an event that belongs to no client at all — process
	// startup, background reclamation, scheduled work. Filing those under a
	// client would be a convenient lie, and an audit trail that lies about
	// provenance is worse than one that admits a third case.
	SurfaceSystem Surface = "system"
)

// SurfaceHeader is how a client tells the server which entry point it is.
//
// It lives here rather than in the HTTP package so a client can name itself
// without depending on the server it is talking to.
const SurfaceHeader = "X-Moment-Surface"

// ClientSurface reports whether a client-declared value names a surface a
// client is allowed to claim.
//
// Only cli and api are claimable. A client cannot declare itself web: that is
// inferred from holding a browser session, and letting a header assert it
// would let any caller forge the one attribution the server can actually
// verify. Nothing can declare itself system, which by definition has no client.
func ClientSurface(declared string) (Surface, bool) {
	switch Surface(declared) {
	case SurfaceCLI:
		return SurfaceCLI, true
	case SurfaceAPI:
		return SurfaceAPI, true
	default:
		return "", false
	}
}

// Logger serializes complete events so concurrent requests cannot interleave.
//
// A logger is a surface bound to a shared writer. Deriving one for a different
// surface must keep the same lock: whole-event serialization is this type's
// core guarantee, and a derived logger holding its own mutex would let two
// surfaces interleave halves of a line.
type Logger struct {
	shared  *sharedWriter
	surface Surface
}

type sharedWriter struct {
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
	// Surface is the entry point events default to. An empty value means the
	// process could not say, which is recorded as such rather than guessed.
	Surface Surface
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
	shared := &sharedWriter{level: level, format: format, writer: config.Writer, now: config.Now}
	return &Logger{shared: shared, surface: config.Surface}, nil
}

// On returns a logger writing through the same lock under another surface.
//
// `moment serve` needs exactly this: one process, whose HTTP middleware speaks
// for the browser and whose lifecycle events speak for no client.
func (logger *Logger) On(surface Surface) *Logger {
	if logger == nil {
		return nil
	}
	return &Logger{shared: logger.shared, surface: surface}
}

// Surface reports the entry point this logger stamps on its events.
func (logger *Logger) Surface() Surface {
	if logger == nil {
		return ""
	}
	return logger.surface
}

// Enabled reports whether an event at level would be emitted.
func (logger *Logger) Enabled(level string) bool {
	priority, ok := priorities[level]
	return logger != nil && logger.shared != nil && ok && priority <= logger.shared.level
}

// Event writes one bounded event. Sensitive field names are redacted
// recursively even if a future caller accidentally supplies them.
func (logger *Logger) Event(level, event string, fields Fields) {
	if !logger.Enabled(level) {
		return
	}
	entry := make(map[string]any, len(fields)+4)
	entry["time"] = logger.shared.now().UTC().Format(time.RFC3339Nano)
	entry["level"] = level
	entry["event"] = safeEventName(event)
	if logger.surface != "" {
		entry["surface"] = string(logger.surface)
	}
	if len(fields) > maxLogCollectionLen {
		entry["fields"] = redactedValue
		fields = nil
	}
	for key, value := range fields {
		key = safeFieldKey(key)
		// surface joins the reserved names. A provenance marker business code
		// can overwrite is not a provenance marker.
		if key == "" || key == "time" || key == "level" || key == "event" || key == "surface" {
			continue
		}
		entry[key] = redactValue(key, value)
	}

	var line []byte
	if logger.shared.format == FormatJSON {
		encoded, err := json.Marshal(entry)
		if err != nil {
			return
		}
		line = encoded
	} else {
		line = []byte(formatText(entry))
	}
	line = append(line, '\n')
	logger.shared.mu.Lock()
	defer logger.shared.mu.Unlock()
	_, _ = logger.shared.writer.Write(line)
}

func safeEventName(event string) string {
	event = strings.TrimSpace(event)
	if event == "" || len(event) > maxLogNameBytes {
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
	if len(key) > maxLogNameBytes {
		return ""
	}
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
		return redactedValue
	}
	return redactReflectValue(reflect.ValueOf(value), 0)
}

func redactReflectValue(value reflect.Value, depth int) any {
	if !value.IsValid() {
		return nil
	}
	if depth >= maxLogValueDepth {
		return redactedValue
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return nil
		}
		return redactReflectValue(value.Elem(), depth+1)
	}
	if value.CanInterface() {
		if _, isError := value.Interface().(error); isError {
			return redactedValue
		}
		if timestamp, isTime := value.Interface().(time.Time); isTime {
			return timestamp.UTC().Format(time.RFC3339Nano)
		}
	}

	switch value.Kind() {
	case reflect.Bool:
		return value.Bool()
	case reflect.String:
		if value.Len() > maxLogStringBytes {
			return redactedValue
		}
		return value.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return value.Uint()
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return redactedValue
		}
		return number
	case reflect.Map:
		return redactMap(value, depth+1)
	case reflect.Slice, reflect.Array:
		return redactCollection(value, depth+1)
	case reflect.Invalid:
		return nil
	default:
		return redactedValue
	}
}

func redactMap(value reflect.Value, depth int) any {
	if value.Type().Key().Kind() != reflect.String || value.Len() > maxLogCollectionLen {
		return redactedValue
	}
	if value.Kind() == reflect.Map && value.IsNil() {
		return nil
	}
	result := make(map[string]any, value.Len())
	iterator := value.MapRange()
	for iterator.Next() {
		key := safeFieldKey(iterator.Key().String())
		if key == "" {
			continue
		}
		if sensitiveField(key) {
			result[key] = redactedValue
			continue
		}
		result[key] = redactReflectValue(iterator.Value(), depth)
	}
	return result
}

func redactCollection(value reflect.Value, depth int) any {
	if value.Len() > maxLogCollectionLen || value.Type().Elem().Kind() == reflect.Uint8 {
		return redactedValue
	}
	if value.Kind() == reflect.Slice && value.IsNil() {
		return nil
	}
	result := make([]any, value.Len())
	for index := range value.Len() {
		result[index] = redactReflectValue(value.Index(index), depth)
	}
	return result
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
		if key != "time" && key != "level" && key != "event" && key != "surface" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	// surface leads with the event rather than sorting in among the payload:
	// it says who the event is about, which is read together with what it is.
	parts := []string{
		fmt.Sprintf("time=%s", entry["time"]),
		fmt.Sprintf("level=%s", entry["level"]),
		fmt.Sprintf("event=%s", entry["event"]),
	}
	if surface, ok := entry["surface"]; ok {
		parts = append(parts, fmt.Sprintf("surface=%s", surface))
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
