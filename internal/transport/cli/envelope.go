package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type envelope struct {
	OK    bool           `json:"ok"`
	Data  any            `json:"data,omitempty"`
	Meta  map[string]any `json:"meta,omitempty"`
	Error *CommandError  `json:"error,omitempty"`
}

func writeJSONDocument(writer io.Writer, value any) error {
	document, err := json.Marshal(value)
	if err != nil {
		return err
	}
	document = append(document, '\n')
	_, err = writer.Write(document)
	return err
}

func writeMachineError(writer io.Writer, commandError *CommandError) (ExitCode, error) {
	err := writeJSONDocument(writer, envelope{OK: false, Error: commandError})
	if err == nil {
		return commandError.ExitCode, nil
	}
	fallback := internalError(err)
	return ExitInternal, writeJSONDocument(writer, envelope{OK: false, Error: fallback})
}

func writeHumanResult(writer io.Writer, result Result) error {
	if result.Text != "" {
		if _, err := io.WriteString(writer, result.Text); err != nil {
			return err
		}
		if !strings.HasSuffix(result.Text, "\n") {
			_, err := io.WriteString(writer, "\n")
			return err
		}
		return nil
	}
	if result.Data == nil {
		return nil
	}
	encoded, err := json.MarshalIndent(result.Data, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "%s\n", encoded)
	return err
}

func writeHumanError(writer io.Writer, commandError *CommandError) error {
	if _, err := fmt.Fprintf(writer, "%s: %s\n", commandError.Code, commandError.Message); err != nil {
		return err
	}
	_, err := fmt.Fprintf(writer, "Suggestion: %s\n", commandError.Suggestion)
	return err
}
