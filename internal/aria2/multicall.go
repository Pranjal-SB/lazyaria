package aria2

import (
	"context"
	"encoding/json"
	"fmt"
)

// Result is the outcome of one sub-call of a Multicall.
type Result struct {
	Raw json.RawMessage
	Err *RPCError
}

func (c *Client) withToken(params []any) []any {
	if c.secret == "" {
		return params
	}
	return append([]any{"token:" + c.secret}, params...)
}

// Multicall batches calls through system.multicall, with a token on each
// sub-call. aria2 runs every sub-call, so each index carries its own value or
// fault; the returned error is only for transport or a malformed reply.
func (c *Client) Multicall(ctx context.Context, calls []Call) ([]Result, error) {
	subs := make([]map[string]any, len(calls))
	for i, cl := range calls {
		subs[i] = map[string]any{"methodName": cl.Method, "params": c.withToken(cl.Params)}
	}
	var raw []json.RawMessage
	if err := c.call(ctx, "system.multicall", []any{subs}, &raw); err != nil {
		return nil, err
	}
	if len(raw) != len(calls) {
		return nil, fmt.Errorf("aria2: multicall returned %d results for %d calls", len(raw), len(calls))
	}
	res := make([]Result, len(raw))
	for i, r := range raw {
		var ok []json.RawMessage
		if json.Unmarshal(r, &ok) == nil && len(ok) == 1 {
			res[i].Raw = ok[0]
			continue
		}
		var e RPCError
		if json.Unmarshal(r, &e) == nil && e.Message != "" {
			res[i].Err = &e
			continue
		}
		return nil, fmt.Errorf("aria2: malformed multicall result %s", r)
	}
	return res, nil
}
