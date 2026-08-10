package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const defaultDataDir = ""

// Streams separates input, machine output, and diagnostics for deterministic
// execution and tests.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Mode identifies the selected application adapter.
type Mode string

const (
	// ModeDirect selects local application services over the data directory.
	ModeDirect Mode = "direct"
	// ModeAPI selects the remote Moment HTTP API.
	ModeAPI Mode = "api"
)

// Transport is the mode-specific target selected from global flags.
type Transport interface {
	Mode() Mode
}

// DirectTransport is the local application-service target.
type DirectTransport interface {
	Transport
	DataDirectory() string
}

// APITransport is the remote HTTP API target. Token material is intentionally
// not read or retained by the kernel.
type APITransport interface {
	Transport
	BaseURL() *url.URL
	TokenFile() string
}

type directTarget struct{ dataDir string }

func (target directTarget) Mode() Mode            { return ModeDirect }
func (target directTarget) DataDirectory() string { return target.dataDir }

type apiTarget struct {
	baseURL   *url.URL
	tokenFile string
}

func (target apiTarget) Mode() Mode        { return ModeAPI }
func (target apiTarget) BaseURL() *url.URL { return target.baseURL }
func (target apiTarget) TokenFile() string { return target.tokenFile }

// Capability declares transport support for one leaf command.
type Capability uint8

const (
	// CapabilityDirectOnly permits only the local application-service adapter.
	CapabilityDirectOnly Capability = 1 << iota
	// CapabilityAPIOnly permits only the remote HTTP API adapter.
	CapabilityAPIOnly
	// CapabilityBoth permits both adapters with equivalent domain data.
	CapabilityBoth = CapabilityDirectOnly | CapabilityAPIOnly
)

func (capability Capability) supports(mode Mode) bool {
	switch mode {
	case ModeDirect:
		return capability&CapabilityDirectOnly != 0
	case ModeAPI:
		return capability&CapabilityAPIOnly != 0
	default:
		return false
	}
}

// Options contains the common flags shared by every command.
type Options struct {
	DataDir   string
	URL       string
	TokenFile string
	JSON      bool
	LogLevel  string
	Timeout   time.Duration
	NoInput   bool
}

// SafetyPolicy adds confirmation flags only to commands that need them.
type SafetyPolicy struct {
	Destructive    bool
	SupportsDryRun bool
	Confirmation   string
}

// SafetyState is the parsed safety intent available to a handler. It is not an
// authorization or integrity decision.
type SafetyState struct {
	Yes    bool
	DryRun bool
}

// Result keeps human presentation separate from the stable machine data.
type Result struct {
	Data any
	Text string
	Meta map[string]any
}

// CommandInput exposes parsed values without giving handlers access to Cobra's
// output streams. This keeps every result inside the shared envelope boundary.
type CommandInput struct {
	args  []string
	flags *pflag.FlagSet
}

// Args returns a defensive copy of positional command arguments.
func (input CommandInput) Args() []string {
	return append([]string(nil), input.args...)
}

// Changed reports whether a flag was explicitly supplied.
func (input CommandInput) Changed(name string) bool {
	return input.flags.Changed(name)
}

// String returns a parsed string flag.
func (input CommandInput) String(name string) (string, error) {
	return input.flags.GetString(name)
}

// Bool returns a parsed boolean flag.
func (input CommandInput) Bool(name string) (bool, error) {
	return input.flags.GetBool(name)
}

// Int returns a parsed integer flag.
func (input CommandInput) Int(name string) (int, error) {
	return input.flags.GetInt(name)
}

// Int64 returns a parsed 64-bit integer flag.
func (input CommandInput) Int64(name string) (int64, error) {
	return input.flags.GetInt64(name)
}

// Duration returns a parsed duration flag.
func (input CommandInput) Duration(name string) (time.Duration, error) {
	return input.flags.GetDuration(name)
}

// StringSlice returns a copy of a parsed string-slice flag.
func (input CommandInput) StringSlice(name string) ([]string, error) {
	values, err := input.flags.GetStringSlice(name)
	return append([]string(nil), values...), err
}

// StringArray returns a copy of a parsed repeatable string flag.
func (input CommandInput) StringArray(name string) ([]string, error) {
	values, err := input.flags.GetStringArray(name)
	return append([]string(nil), values...), err
}

// Handler adapts one leaf command to an application service or HTTP client.
// Business rules and transactions do not belong in this function.
type Handler func(context.Context, Invocation, CommandInput) (Result, error)

// LeafSpec describes a command while leaving execution policy with the kernel.
type LeafSpec struct {
	Path       []string
	ArgsUsage  string
	Short      string
	Long       string
	Inputs     string
	Output     string
	Errors     string
	Safety     string
	Example    string
	Args       cobra.PositionalArgs
	Flags      func(*pflag.FlagSet)
	Capability Capability
	Policy     SafetyPolicy
	// LongRunning reserves the parent context for lifecycle commands such as
	// serve. Other commands remain bounded by --timeout.
	LongRunning bool
	Run         Handler
}

// GroupSpec describes one root-level noun command.
type GroupSpec struct {
	Name  string
	Short string
	Long  string
}

// Invocation is the immutable global execution context supplied to a handler.
type Invocation struct {
	options Options
	target  Transport
	safety  SafetyState
	logger  logger
}

// Options returns a copy of the parsed common command options.
func (invocation Invocation) Options() Options { return invocation.options }

// Mode returns the explicitly selected direct or API mode.
func (invocation Invocation) Mode() Mode { return invocation.target.Mode() }

// Transport returns the selected target through its minimal shared interface.
func (invocation Invocation) Transport() Transport { return invocation.target }

// Safety returns confirmation and dry-run intent without bypassing service guards.
func (invocation Invocation) Safety() SafetyState { return invocation.safety }

// Log writes a level-filtered diagnostic to stderr.
func (invocation Invocation) Log(level, message string) {
	invocation.logger.log(level, message)
}

// DirectTransport returns the direct target or an error when API mode is active.
func (invocation Invocation) DirectTransport() (DirectTransport, error) {
	target, ok := invocation.target.(DirectTransport)
	if !ok {
		return nil, fmt.Errorf("selected transport is %s, not direct", invocation.Mode())
	}
	return target, nil
}

// APITransport returns the API target or an error when direct mode is active.
func (invocation Invocation) APITransport() (APITransport, error) {
	target, ok := invocation.target.(APITransport)
	if !ok {
		return nil, fmt.Errorf("selected transport is %s, not api", invocation.Mode())
	}
	return target, nil
}
