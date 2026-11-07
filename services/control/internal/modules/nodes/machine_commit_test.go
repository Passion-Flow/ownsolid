package nodes

import (
	"context"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// statementCounter counts the statements an Executor runs and affects no rows.
type statementCounter struct {
	dialect transaction.Dialect
	execs   int
}

func (c *statementCounter) Dialect() transaction.Dialect { return c.dialect }
func (c *statementCounter) Exec(context.Context, transaction.Statement) (int64, error) {
	c.execs++
	return 0, nil
}
func (c *statementCounter) Query(context.Context, transaction.Statement) (transaction.Rows, error) {
	return nil, nil
}
func (c *statementCounter) QueryRow(context.Context, transaction.Statement) transaction.Row {
	return nil
}

func TestOnlyAnUnchangedCallOnTheWatchedTransactionStopsWaitingForTheDisk(t *testing.T) {
	ctx := context.Background()
	write := transaction.SQL(transaction.PostgreSQL, `UPDATE node_tasks SET state='running'`)
	cases := map[string]struct {
		dialect transaction.Dialect
		change  bool
		legacy  bool
		relaxed bool
	}{
		"read only":          {transaction.PostgreSQL, false, false, true},
		"changed":            {transaction.PostgreSQL, true, false, false},
		"legacy transaction": {transaction.PostgreSQL, false, true, false},
		"another database":   {transaction.MySQL, false, false, false},
	}
	for name, c := range cases {
		counter := &statementCounter{dialect: c.dialect}
		watched := transaction.Watch(counter)
		if c.change {
			watched.QueryRow(ctx, write)
		}
		if e := finishMachineTransaction(ctx, counter, watched, c.legacy); e != nil {
			t.Fatal(name, e)
		}
		if relaxed := counter.execs == 1; relaxed != c.relaxed {
			t.Errorf("%s: relaxed %v, want %v", name, relaxed, c.relaxed)
		}
	}
}
