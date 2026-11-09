package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

const (
	maximumPageRows    = 100
	maximumCursorBytes = 512
)

type boundedPage struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type pageOptions struct {
	limit  int
	cursor string
}

func (p *pageOptions) bind(cmd *cobra.Command, cursorHelp string) {
	cmd.Flags().IntVar(&p.limit, "limit", maximumPageRows, "Maximum rows to read (the server selects the page size when omitted)")
	cmd.Flags().StringVar(&p.cursor, "cursor", "", cursorHelp)
}

func (p pageOptions) query(cmd *cobra.Command) (url.Values, error) {
	query := make(url.Values)
	if cmd.Flags().Changed("limit") {
		if p.limit < 1 || p.limit > maximumPageRows {
			return nil, fmt.Errorf("--limit must be between 1 and %d", maximumPageRows)
		}
		query.Set("limit", strconv.Itoa(p.limit))
	}
	if cmd.Flags().Changed("cursor") {
		if !validPageCursor(p.cursor) {
			return nil, fmt.Errorf("--cursor must be printable and between 1 and %d bytes; use the previous page's next cursor", maximumCursorBytes)
		}
		query.Set("cursor", p.cursor)
	}
	return query, nil
}

func (p pageOptions) validate(page boundedPage, resourceName string) error {
	if page.Items == nil || len(page.Items) > p.limit {
		return fmt.Errorf("the server returned an invalid %s page", resourceName)
	}
	if next := page.NextCursor; next != nil &&
		(len(page.Items) == 0 || *next == p.cursor || !validPageCursor(*next)) {
		return fmt.Errorf("the server returned an invalid %s cursor", resourceName)
	}
	return nil
}

func validPageCursor(value string) bool {
	return value != "" && len(value) <= maximumCursorBytes &&
		!strings.ContainsFunc(value, func(r rune) bool { return !unicode.IsPrint(r) })
}

func (opts *options) renderPage(cmd *cobra.Command, columns []column, page boundedPage) error {
	out := cmd.OutOrStdout()
	if err := render(out, opts.output, columns, page, page.Items); err != nil {
		return err
	}
	if opts.output == "table" && page.NextCursor != nil {
		_, err := fmt.Fprintf(out, "\nNext cursor: %q\n", *page.NextCursor)
		return err
	}
	return nil
}
