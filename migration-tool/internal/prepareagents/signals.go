package prepareagents

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

// InstallSignalHandler returns a context that is cancelled when SIGINT or SIGTERM is received,
// and a stop function that releases the signal handler.
//
// The first signal cancels the context (graceful shutdown). A SECOND signal forces immediate exit
// via os.Exit(130) — this is a safety hatch in case the orchestrator is wedged somewhere.
func InstallSignalHandler(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-sigCh:
			ui.Blank()
			ui.Warning("Received " + sig.String() + " — initiating graceful shutdown (press again to force exit)")
			cancel()
		case <-ctx.Done():
			return
		}

		// Second signal -> force exit.
		select {
		case <-sigCh:
			ui.Error("Received second signal — forcing exit")
			os.Exit(130)
		case <-ctx.Done():
			return
		}
	}()

	stop := func() {
		signal.Stop(sigCh)
		cancel()
	}
	return ctx, stop
}

// PromptSkipPending asks the user whether to mark all non-terminal agents as skipped.
// Returns true if the user accepts.
func PromptSkipPending(stats Stats) bool {
	if stats.Active() == 0 && stats.Failed == 0 {
		return false
	}
	ui.Blank()
	ui.SubHeader("Pending agents")
	ui.Info("The following agents have not been deployed yet:")
	if stats.Pending > 0 {
		ui.Detail("  pending", "")
	}
	if stats.Offline > 0 {
		ui.Detail("  offline", "")
	}
	if stats.Failed > 0 {
		ui.Detail("  failed (still retriable)", "")
	}
	ui.Blank()
	return ui.Confirm("Mark these agents as 'skipped' and exit? (you'll have to install migrate_agent on them manually)")
}
