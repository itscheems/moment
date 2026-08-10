package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

// Config provides deterministic seams without weakening production behavior.
type Config struct {
	BinaryName string
	Short      string
	Long       string
	Now        func() time.Time
	RequestID  func() string
}

// Application owns the reusable command contract and registration boundary.
// Register all commands during process composition, before Execute is called.
type Application struct {
	config Config
	mu     sync.RWMutex
	groups []GroupSpec
	leaves []LeafSpec
}

type executionState struct {
	started bool
	result  Result
}

// New constructs a featureless CLI kernel.
func New(config Config) *Application {
	if config.BinaryName == "" {
		config.BinaryName = "moment"
	}
	if config.Short == "" {
		config.Short = "A portable personal timeline"
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.RequestID == nil {
		config.RequestID = randomRequestID
	}
	return &Application{config: config}
}

// RegisterGroup adds a noun-level help boundary.
func (application *Application) RegisterGroup(spec GroupSpec) error {
	if strings.TrimSpace(spec.Name) == "" || strings.ContainsAny(spec.Name, " \t\r\n") {
		return fmt.Errorf("group name must be one command token")
	}
	if strings.TrimSpace(spec.Short) == "" {
		return fmt.Errorf("group %q requires a short description", spec.Name)
	}

	application.mu.Lock()
	defer application.mu.Unlock()
	for _, existing := range application.groups {
		if existing.Name == spec.Name {
			return fmt.Errorf("group %q is already registered", spec.Name)
		}
	}
	for _, existing := range application.leaves {
		if len(existing.Path) == 1 && existing.Path[0] == spec.Name {
			return fmt.Errorf("group %q conflicts with an existing top-level leaf", spec.Name)
		}
	}
	application.groups = append(application.groups, spec)
	return nil
}

// RegisterLeaf adds a top-level or group leaf while preserving the shared
// execution contract.
func (application *Application) RegisterLeaf(spec LeafSpec) error {
	if len(spec.Path) == 0 || len(spec.Path) > 2 {
		return fmt.Errorf("leaf path must contain one or two command tokens")
	}
	for _, token := range spec.Path {
		if strings.TrimSpace(token) == "" || strings.ContainsAny(token, " \t\r\n") {
			return fmt.Errorf("leaf path tokens must be non-empty command names")
		}
	}
	if spec.Run == nil {
		return fmt.Errorf("leaf %q requires a handler", strings.Join(spec.Path, " "))
	}
	if strings.TrimSpace(spec.Short) == "" {
		return fmt.Errorf("leaf %q requires a short description", strings.Join(spec.Path, " "))
	}
	if strings.TrimSpace(spec.Long) == "" || strings.TrimSpace(spec.Inputs) == "" || strings.TrimSpace(spec.Output) == "" || strings.TrimSpace(spec.Errors) == "" {
		return fmt.Errorf("leaf %q requires long, input, output, and error help", strings.Join(spec.Path, " "))
	}
	if strings.TrimSpace(spec.Example) == "" {
		return fmt.Errorf("leaf %q requires a runnable example", strings.Join(spec.Path, " "))
	}
	if spec.Capability == 0 || spec.Capability&^CapabilityBoth != 0 {
		return fmt.Errorf("leaf %q must support at least one transport mode", strings.Join(spec.Path, " "))
	}
	if spec.Policy.SupportsDryRun && !spec.Policy.Destructive {
		return fmt.Errorf("leaf %q cannot expose dry-run without a destructive policy", strings.Join(spec.Path, " "))
	}
	if spec.Policy.Destructive && strings.TrimSpace(spec.Safety) == "" {
		return fmt.Errorf("destructive leaf %q requires safety help", strings.Join(spec.Path, " "))
	}

	application.mu.Lock()
	defer application.mu.Unlock()
	if len(spec.Path) == 1 {
		for _, group := range application.groups {
			if group.Name == spec.Path[0] {
				return fmt.Errorf("leaf %q conflicts with an existing group", spec.Path[0])
			}
		}
	} else {
		groupExists := false
		for _, group := range application.groups {
			if group.Name == spec.Path[0] {
				groupExists = true
				break
			}
		}
		if !groupExists {
			return fmt.Errorf("leaf %q references unregistered group %q", strings.Join(spec.Path, " "), spec.Path[0])
		}
	}
	for _, existing := range application.leaves {
		if strings.Join(existing.Path, "\x00") == strings.Join(spec.Path, "\x00") {
			return fmt.Errorf("leaf %q is already registered", strings.Join(spec.Path, " "))
		}
	}
	application.leaves = append(application.leaves, spec)
	return nil
}

// MustRegisterGroup registers a group or panics during application composition.
func (application *Application) MustRegisterGroup(spec GroupSpec) {
	if err := application.RegisterGroup(spec); err != nil {
		panic(err)
	}
}

// MustRegisterLeaf registers a leaf or panics during application composition.
func (application *Application) MustRegisterLeaf(spec LeafSpec) {
	if err := application.RegisterLeaf(spec); err != nil {
		panic(err)
	}
}

// Execute parses one invocation and returns its stable process exit code.
func (application *Application) Execute(ctx context.Context, args []string, streams Streams) ExitCode {
	streams = completeStreams(streams)
	startedAt := application.config.Now()
	if containsJSONFlag(args) && containsHelpFlag(args) {
		commandError := invalidArgument(
			"--json and --help cannot be combined because help is human-readable text",
			"remove --json to read help, or run a command with --json for machine output",
		)
		exitCode, err := writeMachineError(streams.Out, commandError)
		if err != nil {
			_, _ = fmt.Fprintf(streams.Err, "INTERNAL: write JSON error output: %v\n", err)
			return ExitInternal
		}
		return exitCode
	}
	root, options, state := application.buildRoot(streams)
	root.SetArgs(args)
	root.SetIn(streams.In)
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)

	err := root.ExecuteContext(ctx)
	jsonMode := options.JSON || containsJSONFlag(args)
	if err != nil {
		commandError := normalizeError(err, state.started)
		if jsonMode {
			exitCode, writeErr := writeMachineError(streams.Out, commandError)
			if writeErr != nil {
				_, _ = fmt.Fprintf(streams.Err, "INTERNAL: write JSON error output: %v\n", writeErr)
				return ExitInternal
			}
			return exitCode
		} else if writeErr := writeHumanError(streams.Err, commandError); writeErr != nil {
			return ExitInternal
		}
		return commandError.ExitCode
	}

	if !state.started {
		return ExitSuccess
	}

	if jsonMode {
		meta := map[string]any{
			"request_id":  application.config.RequestID(),
			"duration_ms": durationMilliseconds(startedAt, application.config.Now()),
		}
		for key, value := range state.result.Meta {
			meta[key] = value
		}
		if err := writeJSONDocument(streams.Out, envelope{OK: true, Data: state.result.Data, Meta: meta}); err != nil {
			fallback := NewCommandError(
				"INTERNAL",
				"the command result could not be encoded",
				"rerun without --json and inspect stderr",
				ExitInternal,
			).WithCause(err)
			if writeErr := writeJSONDocument(streams.Out, envelope{OK: false, Error: fallback}); writeErr != nil {
				_, _ = fmt.Fprintf(streams.Err, "INTERNAL: write JSON fallback: %v\n", writeErr)
			}
			return ExitInternal
		}
		return ExitSuccess
	}

	if err := writeHumanResult(streams.Out, state.result); err != nil {
		_, _ = fmt.Fprintf(streams.Err, "INTERNAL: write command output: %v\n", err)
		return ExitInternal
	}
	return ExitSuccess
}

func (application *Application) buildRoot(streams Streams) (*cobra.Command, *Options, *executionState) {
	application.mu.RLock()
	groups := append([]GroupSpec(nil), application.groups...)
	leaves := append([]LeafSpec(nil), application.leaves...)
	application.mu.RUnlock()

	options := &Options{
		DataDir:   defaultDataDir,
		LogLevel:  "info",
		LogFormat: "text",
		Timeout:   30 * time.Second,
	}
	state := &executionState{}
	root := &cobra.Command{
		Use:           application.config.BinaryName,
		Short:         application.config.Short,
		Long:          application.config.Long,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if options.JSON {
				return invalidArgument("a command is required", "run 'moment --help' to inspect available command groups")
			}
			return command.Help()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return invalidArgument(err.Error(), "run 'moment --help' to inspect valid flags")
	})

	flags := root.PersistentFlags()
	flags.StringVar(&options.DataDir, "data-dir", defaultDataDir, "local Moment data directory (direct mode)")
	flags.StringVar(&options.URL, "url", "", "Moment HTTP API base URL (API mode)")
	flags.StringVar(&options.TokenFile, "token-file", "", "file containing the owner API token")
	flags.BoolVar(&options.JSON, "json", false, "write one machine-readable JSON document to stdout")
	flags.StringVar(&options.LogLevel, "log-level", "info", "stderr log level: error, warn, info, or debug")
	flags.StringVar(&options.LogFormat, "log-format", "text", "stderr log format: text or json")
	flags.DurationVar(&options.Timeout, "timeout", 30*time.Second, "maximum command duration")
	flags.BoolVar(&options.NoInput, "no-input", false, "fail instead of prompting for input")

	groupCommands := make(map[string]*cobra.Command, len(groups))
	for _, group := range groups {
		group := group
		groupCommand := &cobra.Command{
			Use:   group.Name,
			Short: group.Short,
			Long:  group.Long,
			Args:  cobra.NoArgs,
			RunE: func(command *cobra.Command, _ []string) error {
				if options.JSON {
					return invalidArgument(
						fmt.Sprintf("a %s command is required", group.Name),
						fmt.Sprintf("run 'moment %s --help' to inspect available commands", group.Name),
					)
				}
				return command.Help()
			},
		}
		groupCommands[group.Name] = groupCommand
		root.AddCommand(groupCommand)
	}

	for _, spec := range leaves {
		spec := spec
		leaf := application.buildLeaf(root, options, state, streams, spec)
		if len(spec.Path) == 1 {
			root.AddCommand(leaf)
			continue
		}
		parent, ok := groupCommands[spec.Path[0]]
		if !ok {
			panic(fmt.Sprintf("leaf %q references unregistered group %q", strings.Join(spec.Path, " "), spec.Path[0]))
		}
		parent.AddCommand(leaf)
	}

	return root, options, state
}

func (application *Application) buildLeaf(
	root *cobra.Command,
	options *Options,
	state *executionState,
	streams Streams,
	spec LeafSpec,
) *cobra.Command {
	use := spec.Path[len(spec.Path)-1]
	if strings.TrimSpace(spec.ArgsUsage) != "" {
		use += " " + strings.TrimSpace(spec.ArgsUsage)
	}
	command := &cobra.Command{
		Use:     use,
		Short:   spec.Short,
		Long:    leafDescription(spec),
		Example: strings.TrimSpace(spec.Example),
		Args:    spec.Args,
	}
	if command.Args == nil {
		command.Args = cobra.NoArgs
	}
	if spec.Flags != nil {
		spec.Flags(command.Flags())
	}

	safety := SafetyState{}
	if spec.Policy.Destructive {
		command.Flags().BoolVar(&safety.Yes, "yes", false, "confirm the destructive operation without prompting")
	}
	if spec.Policy.SupportsDryRun {
		command.Flags().BoolVar(&safety.DryRun, "dry-run", false, "describe the operation without changing state")
	}

	command.RunE = func(command *cobra.Command, args []string) error {
		state.started = true
		if options.Timeout <= 0 {
			return invalidArgument("--timeout must be greater than zero", "use a positive duration such as --timeout 30s")
		}
		if spec.LongRunning && root.PersistentFlags().Changed("timeout") {
			return invalidArgument(
				"--timeout cannot be used with a long-running lifecycle command",
				"remove --timeout and cancel the command with the parent process signal",
			)
		}

		outputLogger, err := newLogger(options.LogLevel, options.LogFormat, streams.Err)
		if err != nil {
			return err
		}
		target, err := resolveTransport(root, *options)
		if err != nil {
			return err
		}
		if !spec.Capability.supports(target.Mode()) {
			return NewCommandError(
				"MODE_UNSUPPORTED",
				fmt.Sprintf("%s mode is not supported for 'moment %s'", target.Mode(), strings.Join(spec.Path, " ")),
				fmt.Sprintf("run 'moment %s --help' and choose a supported mode", strings.Join(spec.Path, " ")),
				ExitInvalidInput,
			).WithDetails(map[string]any{
				"command": strings.Join(spec.Path, " "),
				"mode":    target.Mode(),
			})
		}
		if commandError := enforceSafety(streams, *options, spec.Policy, safety); commandError != nil {
			return commandError
		}

		invocation := Invocation{
			options: *options,
			target:  target,
			safety:  safety,
			logger:  outputLogger,
		}
		operationContext := command.Context()
		cancel := func() {}
		if !spec.LongRunning {
			operationContext, cancel = context.WithTimeout(operationContext, options.Timeout)
		}
		defer cancel()
		result, runErr := spec.Run(operationContext, invocation, CommandInput{
			args:  args,
			flags: command.Flags(),
		})
		if runErr != nil {
			return runErr
		}
		state.result = result
		return nil
	}
	return command
}

func resolveTransport(root *cobra.Command, options Options) (Transport, error) {
	dataDirChanged := root.PersistentFlags().Changed("data-dir")
	if options.URL == "" {
		if options.TokenFile != "" {
			return nil, invalidArgument("--token-file requires --url", "provide --url for API mode or remove --token-file")
		}
		return directTarget{dataDir: options.DataDir}, nil
	}
	if dataDirChanged {
		return nil, invalidArgument("--url and --data-dir select different transport modes", "use --url for API mode or --data-dir for direct mode, not both")
	}

	parsedURL, err := url.Parse(options.URL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, invalidArgument("--url must be an absolute HTTP or HTTPS URL", "use a URL such as http://127.0.0.1:8080")
	}
	if parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.ForceQuery || parsedURL.Fragment != "" {
		return nil, invalidArgument(
			"--url cannot contain credentials, a query, or a fragment",
			"use an origin or reverse-proxy base path and provide credentials through MOMENT_TOKEN or --token-file",
		)
	}
	return apiTarget{baseURL: parsedURL, tokenFile: options.TokenFile}, nil
}

func leafDescription(spec LeafSpec) string {
	sections := []string{strings.TrimSpace(spec.Long)}
	for _, section := range []struct {
		title string
		body  string
	}{
		{title: "INPUTS", body: spec.Inputs},
		{title: "OUTPUT", body: spec.Output},
		{title: "ERRORS", body: spec.Errors},
		{title: "SAFETY", body: spec.Safety},
	} {
		if strings.TrimSpace(section.body) != "" {
			sections = append(sections, section.title+"\n  "+strings.TrimSpace(section.body))
		}
	}
	return strings.Join(sections, "\n\n")
}

func completeStreams(streams Streams) Streams {
	if streams.In == nil {
		streams.In = strings.NewReader("")
	}
	if streams.Out == nil {
		streams.Out = io.Discard
	}
	if streams.Err == nil {
		streams.Err = io.Discard
	}
	return streams
}

func containsJSONFlag(args []string) bool {
	for _, argument := range args {
		if argument == "--json" || argument == "--json=true" {
			return true
		}
	}
	return false
}

func containsHelpFlag(args []string) bool {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" || argument == "--help=true" {
			return true
		}
	}
	return false
}

func durationMilliseconds(start, end time.Time) int64 {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func randomRequestID() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(random[:])
}
