package cmd

import (
	"log/slog"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"
)

// selfApplyUserTargets is what "proxy serve" runs once at startup, before it
// starts listening, when the targets do not yet point at it. It never
// blocks startup on failure: a target that could not be configured is
// logged and skipped, the same principle State.Reload already follows — a
// bad write must never keep a working proxy from serving.
func selfApplyUserTargets(logger *slog.Logger, pf *proxy.ProfileFile) {
	if pf.ViaLocal {
		return
	}
	ex := &proxy.Executor{Escalation: proxy.EscalateNone}
	rep, err := app.ApplyUserTargets(deps(), ex, pf)
	if err != nil {
		logger.Warn("self_apply_failed", slog.String("error", err.Error()))
		return
	}
	for _, res := range rep.Results {
		if res.Outcome == app.OutcomeFailed {
			logger.Warn("self_apply_target_failed",
				slog.String("target", res.Target),
				slog.String("error", res.Err.Error()))
		}
	}
	if err := proxy.WithProfileLock(func(lpf *proxy.ProfileFile) error {
		lpf.ViaLocal = true
		return nil
	}); err != nil {
		logger.Warn("self_apply_persist_failed", slog.String("error", err.Error()))
		return
	}
	pf.ViaLocal = true
}
