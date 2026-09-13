package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const maxJSONBytes = 2 << 20

func readInput(cmd *cobra.Command, name string) ([]byte, error) {
	var reader io.Reader = cmd.InOrStdin()
	if name != "-" {
		file, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		reader = file
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxJSONBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxJSONBytes {
		return nil, usage("input exceeds 2 MiB")
	}
	if !utf8.Valid(data) {
		return nil, usage("text input must be valid UTF-8")
	}
	return data, nil
}
func bodyInput(cmd *cobra.Command, value string) (any, error) {
	data := []byte(value)
	var err error
	if value == "-" {
		data, err = readInput(cmd, "-")
	} else if strings.HasPrefix(value, "@") {
		data, err = readInput(cmd, value[1:])
	}
	if err != nil {
		return nil, err
	}
	if len(data) > maxJSONBytes {
		return nil, usage("JSON body exceeds 2 MiB")
	}
	return decodeJSON(data)
}
func decodeJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, usage("JSON must be valid UTF-8")
	}
	var value, extra any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, usage("invalid JSON: %v", err)
	}
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, usage("expected exactly one JSON value")
	}
	return value, nil
}
func requireBodySize(body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode body: %w", err)
	}
	if len(data) > maxJSONBytes {
		return usage("encoded JSON body exceeds 2 MiB")
	}
	return nil
}
