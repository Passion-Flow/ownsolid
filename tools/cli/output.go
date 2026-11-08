package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"golang.org/x/text/width"
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
	type cell struct {
		text  string
		width int
	}
	table := make([][]cell, 1, len(rows)+1)
	table[0] = make([]cell, len(columns))
	widths := make([]int, len(columns))
	for i, col := range columns {
		size := displayWidth(col.header)
		table[0][i] = cell{col.header, size}
		widths[i] = size
	}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(row, &fields); err != nil || fields == nil {
			return errors.New("the server returned a row that is not a resource object")
		}
		cells := make([]cell, len(columns))
		for i, col := range columns {
			raw := fields[col.field]
			var value string
			switch {
			case len(raw) == 0 || string(raw) == "null":
				value = "-"
			case json.Unmarshal(raw, &value) != nil:
				value = string(raw)
			}
			if strings.ContainsFunc(value, func(r rune) bool { return !unicode.IsPrint(r) }) {
				value = strconv.Quote(value)
			}
			size := displayWidth(value)
			cells[i] = cell{value, size}
			widths[i] = max(widths[i], size)
		}
		table = append(table, cells)
	}
	output := bufio.NewWriter(w)
	for _, cells := range table {
		for i, entry := range cells {
			if _, err := output.WriteString(entry.text); err != nil {
				return err
			}
			if i < len(cells)-1 {
				padding := strings.Repeat(" ", widths[i]-entry.width+2)
				if _, err := output.WriteString(padding); err != nil {
					return err
				}
			}
		}
		if err := output.WriteByte('\n'); err != nil {
			return err
		}
	}
	return output.Flush()
}

func displayWidth(value string) int {
	size := 0
	for _, r := range value {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
			continue
		}
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			size += 2
		default:
			size++
		}
	}
	return size
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
