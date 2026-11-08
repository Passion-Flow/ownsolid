package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

type column struct {
	header, field string
}

func renderValue(cmd *cobra.Command, columns []column, result map[string]string) error {
	row, err := json.Marshal(result)
	if err != nil {
		return err
	}
	format, err := cmd.Flags().GetString("output")
	if err != nil {
		return err
	}
	return render(cmd.OutOrStdout(), format, columns, result, []json.RawMessage{row})
}

func render(w io.Writer, format string, columns []column, result any, rows []json.RawMessage) error {
	switch format {
	case "table":
		return writeTable(w, columns, rows)
	case "json":
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	case "yaml":
		return writeYAML(w, result)
	default:
		return errors.New("unsupported output format")
	}
}

func writeTable(w io.Writer, columns []column, rows []json.RawMessage) error {
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	cells := make([]string, len(columns))
	for i, col := range columns {
		cells[i] = col.header
	}
	if _, err := fmt.Fprintln(table, strings.Join(cells, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(row, &fields); err != nil || fields == nil {
			return errors.New("the server returned a row that is not a resource object")
		}
		for i, col := range columns {
			raw := fields[col.field]
			var text string
			switch {
			case len(raw) == 0 || string(raw) == "null":
				text = "-"
			case json.Unmarshal(raw, &text) != nil:
				text = string(raw)
			}
			if strings.ContainsFunc(text, func(r rune) bool { return !unicode.IsPrint(r) }) {
				text = strconv.Quote(text)
			}
			cells[i] = text
		}
		if _, err := fmt.Fprintln(table, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return table.Flush()
}

func writeYAML(w io.Writer, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return err
	}
	blockStyle(&node)
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(2)
	return errors.Join(encoder.Encode(&node), encoder.Close())
}

func blockStyle(node *yaml.Node) {
	node.Style &^= yaml.FlowStyle
	for _, child := range node.Content {
		blockStyle(child)
	}
}
