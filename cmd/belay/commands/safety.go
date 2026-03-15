package commands

import (
	"fmt"

	"github.com/davidparkercodes/belay/internal/config"
)

const safetyBlockedMessage = `SAFETY: This command modifies files outside .belay/ and is currently DISABLED.

Belay is running in safe mode (the default). Destructive commands only
show what WOULD happen without making changes.

To enable writes, edit .belay/config.toml:

  [safety]
  allow_writes = true

Then also pass --execute to confirm each invocation:

  belay %s --execute %s

This two-step requirement (config + flag) prevents accidental data loss.`

func checkSafetyGate(cfg *config.Config, execute bool, cmdName string, flagHint string) error {
	if !cfg.WritesAllowed() {
		return fmt.Errorf(safetyBlockedMessage, cmdName, flagHint)
	}
	if !execute {
		return fmt.Errorf("DRY RUN: Pass --execute to actually perform this operation.\n\nWithout --execute, this command only shows what would happen.")
	}
	return nil
}
