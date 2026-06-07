package job

import (
	"context"

	"github.com/esignoretti/ds3-sql-server/internal/write"
)

// LocalWriteExecutor adapts *write.Writer to the WriteExecutor interface,
// translating the job-layer LoadRequest into the write-layer one.
type LocalWriteExecutor struct {
	w *write.Writer
}

func NewLocalWriteExecutor(w *write.Writer) *LocalWriteExecutor {
	return &LocalWriteExecutor{w: w}
}

func (l *LocalWriteExecutor) RunCTAS(ctx context.Context, projectID, sql, ak, sk, ep string) (string, error) {
	tbl, err := l.w.RunCTAS(ctx, projectID, sql, ak, sk, ep)
	if err != nil {
		return "", err
	}
	return tbl.Dataset + "." + tbl.Name, nil
}

func (l *LocalWriteExecutor) RunLoad(ctx context.Context, projectID string, req LoadRequest, ak, sk, ep string) (string, error) {
	tbl, err := l.w.RunLoad(ctx, projectID, write.LoadRequest{
		Source:      req.Source,
		Into:        req.Into,
		Format:      req.Format,
		PartitionBy: req.PartitionBy,
		Mode:        req.Mode,
	}, ak, sk, ep)
	if err != nil {
		return "", err
	}
	return tbl.Dataset + "." + tbl.Name, nil
}

var _ WriteExecutor = (*LocalWriteExecutor)(nil)
