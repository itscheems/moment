package runtimefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	maxInstanceMetadataBytes = int64(16 << 10)
	maxCurrentPointerBytes   = int64(256)
)

// readBoundedAuthorityFile reads a small runtime authority file without ever
// following a symlink or opening a FIFO/device. The identity checks close the
// lstat/open race, while the byte ceiling prevents corrupt metadata from
// allocating in proportion to the database or filesystem size.
func readBoundedAuthorityFile(path string, limit int64) ([]byte, error) {
	return readBoundedAuthorityFileAfterLstat(path, limit, nil)
}

func readBoundedAuthorityFileAfterLstat(path string, limit int64, afterLstat func()) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() < 0 || before.Size() > limit {
		return nil, errors.New("authority file is unsafe or exceeds its byte limit")
	}
	if afterLstat != nil {
		afterLstat()
	}
	file, err := openAuthorityFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || opened.Size() != before.Size() || opened.Size() > limit {
		return nil, errors.Join(errors.New("authority file changed while opening"), err)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	finished, statErr := file.Stat()
	if statErr != nil || !finished.Mode().IsRegular() || !os.SameFile(opened, finished) ||
		finished.Size() != opened.Size() || finished.Mode() != opened.Mode() || !finished.ModTime().Equal(opened.ModTime()) || int64(len(data)) != finished.Size() || int64(len(data)) > limit {
		return nil, errors.Join(errors.New("authority file changed while reading"), statErr)
	}
	return data, nil
}

func decodeStrictInstance(data []byte, destination *Instance) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("instance metadata has trailing JSON data")
	}
	return rejectDuplicateInstanceFields(data)
}

func rejectDuplicateInstanceFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.Join(errors.New("instance metadata is not a JSON object"), err)
	}
	seen := make(map[string]struct{}, 4)
	for decoder.More() {
		token, tokenErr := decoder.Token()
		name, valid := token.(string)
		if tokenErr != nil || !valid {
			return errors.Join(errors.New("instance metadata field name is invalid"), tokenErr)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("instance metadata contains duplicate field %q", name)
		}
		seen[name] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return errors.Join(errors.New("instance metadata object is incomplete"), err)
	}
	return nil
}
