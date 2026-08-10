package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/moment/moment/internal/buildinfo"
)

func TestLayeredHelpGoldens(t *testing.T) {
	application := NewMoment(buildinfo.Info{Version: "dev", Commit: "unknown", BuildDate: "unknown"})
	tests := []struct {
		name string
		args []string
	}{
		{name: "root", args: []string{"--help"}},
		{name: "group", args: []string{"post", "--help"}},
		{name: "leaf", args: []string{"version", "--help"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := execute(application, "", test.args...)
			if output.exit != ExitSuccess {
				t.Fatalf("exit = %d, stderr = %q", output.exit, output.stderr)
			}
			if output.stderr != "" {
				t.Fatalf("help wrote stderr: %q", output.stderr)
			}
			goldenPath := filepath.Join("testdata", "help", test.name+".golden")
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatal(err)
			}
			if output.stdout != string(want) {
				t.Fatalf("help differs from %s\n--- want ---\n%s\n--- got ---\n%s", goldenPath, want, output.stdout)
			}
		})
	}
}
