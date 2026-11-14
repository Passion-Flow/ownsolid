package identityfixture

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const fixtureStatementTimeoutMilliseconds = "5000"
const fixtureLockTimeoutMilliseconds = "3000"

// SetupOptions belongs only to synthetic fixture construction. Zero keeps the
// historical 5s statement/3s lock budgets and adds no overall setup deadline.
// An explicit nonzero value requires both fields: total creation/migration time
// in seconds (30..900), and per-statement time in seconds (5..300). The total
// must exceed the statement budget plus the unchanged 3s lock budget.
// These values never configure the returned business pool or product policy.
type SetupOptions struct {
	TimeoutSeconds          int
	StatementTimeoutSeconds int
}

type setupPlan struct {
	explicit              bool
	total                 time.Duration
	statementMilliseconds string
}

func (o SetupOptions) plan() (setupPlan, error) {
	if o == (SetupOptions{}) {
		return setupPlan{statementMilliseconds: fixtureStatementTimeoutMilliseconds}, nil
	}
	if o.TimeoutSeconds < 30 || o.TimeoutSeconds > 900 || o.StatementTimeoutSeconds < 5 || o.StatementTimeoutSeconds > 300 || o.TimeoutSeconds <= o.StatementTimeoutSeconds+3 {
		return setupPlan{}, errors.New("IDENTITY_FIXTURE_SETUP_OPTIONS_INVALID")
	}
	return setupPlan{explicit: true, total: time.Duration(o.TimeoutSeconds) * time.Second, statementMilliseconds: strconv.Itoa(o.StatementTimeoutSeconds * 1000)}, nil
}

func (p setupPlan) context() (context.Context, context.CancelFunc) {
	if !p.explicit {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), p.total)
}

func (p setupPlan) poolConfiguration(runtime *pgxpool.Config, schema string) *pgxpool.Config {
	setup := runtime.Copy()
	setup.ConnConfig.RuntimeParams["search_path"] = schema
	setup.ConnConfig.RuntimeParams["statement_timeout"] = p.statementMilliseconds
	setup.ConnConfig.RuntimeParams["application_name"] = "projectone-identity-setup-" + schema
	return setup
}
