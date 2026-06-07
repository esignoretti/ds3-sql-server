package job

import (
	"context"

	"github.com/esignoretti/ds3-sql-server/internal/catalog"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// LocalExecutor resolves catalog table references and runs the query in-process.
type LocalExecutor struct {
	cat    *catalog.Service
	engine *query.Engine
}

func NewLocalExecutor(cat *catalog.Service, engine *query.Engine) *LocalExecutor {
	return &LocalExecutor{cat: cat, engine: engine}
}

func (l *LocalExecutor) Execute(ctx context.Context, req ExecRequest) *query.Result {
	bindings, err := l.cat.Resolve(ctx, req.ProjectID, req.SQL)
	if err != nil {
		return &query.Result{Error: "resolve tables: " + err.Error()}
	}
	return l.engine.QueryView(req.SQL, bindings, req.AccessKey, req.SecretKey, req.Endpoint)
}

var _ Executor = (*LocalExecutor)(nil)
