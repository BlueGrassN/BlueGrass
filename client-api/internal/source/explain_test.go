package source

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"bluegrass/client-api/internal/model"
)

func explainRows(values ...driver.Value) *fixtureRows {
	rows := &fixtureRows{columns: []string{"EXPLAIN"}}
	for _, value := range values {
		rows.values = append(rows.values, []driver.Value{value})
	}
	return rows
}

func explainStep(customerID, after int64, limit int, rows *fixtureRows) queryStep {
	step := nodeStep("nodes", customerID, after, limit, rows)
	check := step.check
	step.kind = "explain_nodes"
	step.check = func(q string, args []driver.NamedValue) error {
		return check(strings.TrimPrefix(q, "explain format=json "), args)
	}
	return step
}

func TestExplainNodesUsesExactListQueryAndReturnsUnmodifiedJSON(t *testing.T) {
	const plan = "{\n  \"query_block\": {\"select_id\": 1, \"cost_info\": {\"query_cost\": \"12.34\"}}\n}\n"
	for _, value := range []driver.Value{plan, []byte(plan)} {
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			repo, script := repositoryFixture(t, func(cfg *Config) { cfg.TablePrefix = "tenant_" })
			var selectSQL string
			var selectArgs []driver.NamedValue
			list := nodeStep("nodes", 7, 11, 100, nodeRows())
			checkList := list.check
			list.check = func(q string, args []driver.NamedValue) error {
				if err := checkList(q, args); err != nil {
					return err
				}
				selectSQL = q
				selectArgs = append([]driver.NamedValue(nil), args...)
				return nil
			}
			script.expect(list)
			if _, err := repo.Nodes(context.Background(), 7, 11, 100); err != nil {
				t.Fatal(err)
			}
			diagnostic := explainStep(7, 11, 100, explainRows(value))
			checkDiagnostic := diagnostic.check
			diagnostic.check = func(q string, args []driver.NamedValue) error {
				if err := checkDiagnostic(q, args); err != nil {
					return err
				}
				if q != "explain format=json "+selectSQL || !reflect.DeepEqual(args, selectArgs) {
					return errors.New("diagnostic changed the list query or its bound arguments")
				}
				return nil
			}
			script.expect(diagnostic)
			got, err := repo.ExplainNodes(context.Background(), 7, 11, 100)
			if err != nil || got != plan {
				t.Fatalf("raw plan: %q, %v", got, err)
			}
		})
	}
}

func TestExplainNodesRejectsInvalidInputsBeforeSQL(t *testing.T) {
	for _, input := range []struct {
		customerID, after int64
		limit             int
	}{
		{7, -1, 10}, {7, 0, 0}, {7, 0, -1}, {7, 0, 101}, {0, 0, 10}, {-1, 0, 10},
	} {
		repo, _ := repositoryFixture(t, nil)
		plan, err := repo.ExplainNodes(context.Background(), input.customerID, input.after, input.limit)
		if plan != "" || err == nil {
			t.Fatalf("invalid diagnostic input accepted: %+v", input)
		}
		if input.customerID <= 0 {
			if !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("invalid customer: %v", err)
			}
		} else {
			assertAPIError(t, err, "validation_error", 422)
		}
	}
}

func TestExplainNodesHonorsQueryTimeoutAndEarlierCallerDeadline(t *testing.T) {
	for _, callerDeadline := range []bool{false, true} {
		t.Run(fmt.Sprint(callerDeadline), func(t *testing.T) {
			repo, script := repositoryFixture(t, func(cfg *Config) {
				if !callerDeadline {
					cfg.QueryTimeout = 15 * time.Millisecond
				}
			})
			ctx := context.Background()
			if callerDeadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 15*time.Millisecond)
				defer cancel()
			}
			step := explainStep(7, 0, 10, explainRows("{}"))
			step.block = make(chan struct{})
			script.expect(step)
			plan, err := repo.ExplainNodes(ctx, 7, 0, 10)
			if plan != "" || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("diagnostic cancellation: %q, %v", plan, err)
			}
		})
	}
}

func TestExplainNodesPropagatesReadFailuresAndClosesRows(t *testing.T) {
	for _, failure := range []string{"query", "read", "empty", "null"} {
		t.Run(failure, func(t *testing.T) {
			repo, script := repositoryFixture(t, nil)
			step := explainStep(7, 0, 10, explainRows())
			want := errors.New("fixture EXPLAIN failed")
			switch failure {
			case "query":
				step.err = want
			case "read":
				step.rows.readErr = want
			case "empty":
				want = sql.ErrNoRows
			case "null":
				step.rows = explainRows(nil)
			}
			script.expect(step)
			plan, err := repo.ExplainNodes(context.Background(), 7, 0, 10)
			if plan != "" || err == nil {
				t.Fatalf("diagnostic read failure was hidden: %q, %v", plan, err)
			}
			if failure != "null" && !errors.Is(err, want) {
				t.Fatalf("diagnostic cause lost: %v", err)
			}
		})
	}
}
