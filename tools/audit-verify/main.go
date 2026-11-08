package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("audit-verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("file", "", "audit JSONL file, or - for standard input")
	output := flags.String("output", "text", "output format: text or json")
	previous := flags.String("previous-hash", zeroHash, "trusted hash immediately before a retained export")
	expected := flags.String("expected-last-hash", "", "trusted hash of the last record in the export")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 || (*output != "text" && *output != "json") {
		fmt.Fprintln(stderr, "Usage: audit-verify --file <path|-> [--output text|json]")
		return 2
	}
	if _, ok := hashBytes(*previous); !ok {
		fmt.Fprintln(stderr, "--previous-hash requires a lowercase SHA-256 digest")
		return 2
	}
	if _, ok := hashBytes(*expected); *expected != "" && !ok {
		fmt.Fprintln(stderr, "--expected-last-hash requires a lowercase SHA-256 digest")
		return 2
	}

	result := report{}
	input := stdin
	if *path != "-" {
		file, err := os.Open(*path)
		if err != nil {
			result.Error = "cannot open input file"
			return printReport(result, *output, stdout, stderr)
		}
		defer file.Close()
		input = file
	}
	return printReport(verify(input, *previous, *expected), *output, stdout, stderr)
}

func printReport(result report, output string, stdout, stderr io.Writer) int {
	var err error
	switch {
	case output == "json":
		err = json.NewEncoder(stdout).Encode(result)
	case result.Valid:
		_, err = fmt.Fprintf(stdout, "Verified %d records from %s through %s\nLast hash: %s\n",
			result.Records, result.From, result.Through, result.LastHash)
	case result.Line > 0:
		_, err = fmt.Fprintf(stderr, "Line %d (%s): %s\n", result.Line, result.Field, result.Error)
	default:
		_, err = fmt.Fprintln(stderr, result.Error)
	}
	if err != nil || !result.Valid {
		return 1
	}
	return 0
}
