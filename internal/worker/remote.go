package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/esignoretti/ds3-sql-server/internal/job"
	"github.com/esignoretti/ds3-sql-server/internal/query"
)

// WireResolver resolves a SQL string to worker-bound bindings (with objects +
// storage class for cache-locality routing). The coordinator's catalog adapter
// implements this.
type WireResolver interface {
	ResolveWire(ctx context.Context, projectID, sql string) ([]WireBinding, error)
}

// RemoteExecutor dispatches resolved plans to a static pool of workers, choosing
// a worker by consistent-hashing the referenced object/table set (cache
// locality) and falling back to the least-loaded worker on failure.
type RemoteExecutor struct {
	endpoints []string
	secret    string
	resolver  WireResolver
	ring      *HashRing
	client    *http.Client

	mu       sync.Mutex
	inFlight map[string]int // endpoint -> in-flight count (for least-loaded fallback)
}

func NewRemoteExecutor(endpoints []string, secret string, resolver WireResolver) *RemoteExecutor {
	return &RemoteExecutor{
		endpoints: endpoints,
		secret:    secret,
		resolver:  resolver,
		ring:      NewHashRing(endpoints, 100),
		client:    &http.Client{Timeout: 5 * time.Minute},
		inFlight:  make(map[string]int),
	}
}

// routeKey builds the consistent-hash key from the referenced object/table set
// so a table's data repeatedly lands on the same worker (warm SSD cache).
func routeKey(bindings []WireBinding) string {
	var parts []string
	for _, b := range bindings {
		if len(b.Objects) > 0 {
			for _, o := range b.Objects {
				parts = append(parts, o.Bucket+"/"+o.Key)
			}
		} else {
			parts = append(parts, b.Schema+"."+b.Name)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// orderedTargets returns the worker endpoints to try, preferred first (ring
// owner), then the remaining workers ordered least-loaded first.
func (e *RemoteExecutor) orderedTargets(bindings []WireBinding) []string {
	if len(e.endpoints) == 0 {
		return nil
	}
	preferred := e.ring.Get(routeKey(bindings))

	e.mu.Lock()
	rest := make([]string, 0, len(e.endpoints))
	for _, ep := range e.endpoints {
		if ep != preferred {
			rest = append(rest, ep)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return e.inFlight[rest[i]] < e.inFlight[rest[j]] })
	e.mu.Unlock()

	if preferred == "" {
		return rest
	}
	return append([]string{preferred}, rest...)
}

func (e *RemoteExecutor) Execute(ctx context.Context, req job.ExecRequest) *query.Result {
	bindings, err := e.resolver.ResolveWire(ctx, req.ProjectID, req.SQL)
	if err != nil {
		return &query.Result{Error: "resolve tables: " + err.Error()}
	}
	targets := e.orderedTargets(bindings)
	if len(targets) == 0 {
		return &query.Result{Error: "no workers configured"}
	}

	payload, err := json.Marshal(ExecuteRequest{
		SQL:       req.SQL,
		Bindings:  bindings,
		AccessKey: req.AccessKey,
		SecretKey: req.SecretKey,
		Endpoint:  req.Endpoint,
	})
	if err != nil {
		return &query.Result{Error: "marshal request: " + err.Error()}
	}

	var lastErr error
	for _, target := range targets {
		res, err := e.post(ctx, target, payload)
		if err != nil {
			lastErr = err
			continue // try the next worker (fallback)
		}
		return res
	}
	return &query.Result{Error: fmt.Sprintf("all workers failed: %v", lastErr)}
}

func (e *RemoteExecutor) post(ctx context.Context, endpoint string, payload []byte) (*query.Result, error) {
	e.mu.Lock()
	e.inFlight[endpoint]++
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.inFlight[endpoint]--
		e.mu.Unlock()
	}()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(endpoint, "/")+"/internal/execute", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(SecretHeader, e.secret)

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("worker %s returned %d", endpoint, resp.StatusCode)
	}
	var res query.Result
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return &res, nil
}

var _ job.Executor = (*RemoteExecutor)(nil)
