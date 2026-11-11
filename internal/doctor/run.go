package doctor

import "context"

// Options selects optional checks.
type Options struct{ E2E bool }

// Run executes all checks in order. Missing config short-circuits (nothing else
// can be evaluated without it).
func Run(ctx context.Context, e *Env, o Options) []Result {
	rs := []Result{CheckEngine(ctx, e)}
	rs = append(rs, CheckServices(ctx, e)...)
	rs = append(rs, CheckPorts(ctx, e)...)
	rs = append(rs, CheckStandin(ctx, e))
	rs = append(rs, CheckPinball(ctx, e)...)
	rs = append(rs, CheckProvider(ctx, e))
	rs = append(rs, CheckOllamaModels(ctx, e)...)
	rs = append(rs, CheckFailMode(e), CheckSkills(e))
	if o.E2E {
		rs = append(rs, CheckE2E(ctx, e))
	}
	return rs
}
