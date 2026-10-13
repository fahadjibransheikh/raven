package main

import (
	"context"
	_ "embed"
	"fmt"
	"github.com/fahadjibransheikh/raven/internal/auth"
	"github.com/fahadjibransheikh/raven/internal/authoperator"
	"github.com/fahadjibransheikh/raven/internal/handler"
	"github.com/fahadjibransheikh/raven/internal/legacyenv"
	"github.com/fahadjibransheikh/raven/internal/server"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/joho/godotenv"
)

// licenseText is shipped inside every build, since the MIT License requires its
// notice to accompany copies of the software. The handler serves it at /licenses.
//
//go:embed LICENSE
var licenseText string

func main() {
	_ = godotenv.Load()
	handler.SetLicenseText(licenseText)
	legacyenv.LogLegacy()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverExit := 0
	exitCode := runApplication(ctx, os.Args[1:], os.Stdout, os.Stderr, func() {
		if auth.LoadConfig("").AuthenticationMode() == auth.ModeManaged {
			if err := server.RunManaged(ctx, os.Stdout, os.Stderr); err != nil {
				fmt.Fprintf(os.Stderr, "Raven startup or shutdown failed: %v\n", err)
				serverExit = 1
			}
		} else {
			// The shared runtime retains its existing signal behavior.
			stop()
			server.RunShared()
		}
	})
	if exitCode == 0 {
		exitCode = serverExit
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

const usage = `Usage:
  raven                 start the server
  raven help            show this help

Authentication:
  raven auth status
  raven auth users list
  raven auth setup-token rotate
  raven auth recover (--user-id <id> | --username <name>) --confirm <same value>
  raven auth sessions revoke (--user-id <id> | --username <name>) --confirm <same value>

Storage:
  raven storage inspect [--db PATH]
  raven storage migrate [--db PATH] --to PATH [--working-dir PATH] [--retry]
`

func runApplication(ctx context.Context, args []string, stdout, stderr io.Writer, serve func()) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		_, _ = io.WriteString(stdout, usage)
		return 0
	}
	if handled, exitCode := runAuthCommand(ctx, args, stdout, stderr); handled {
		return exitCode
	}
	if handled, exitCode := server.RunStorageCommand(ctx, args, stdout, stderr); handled {
		return exitCode
	}
	if len(args) != 0 {
		_, _ = fmt.Fprintf(stderr, "raven: unknown command %q\n\n", strings.Join(args, " "))
		_, _ = io.WriteString(stderr, usage)
		return 2
	}
	serve()
	return 0
}

func runAuthCommand(ctx context.Context, args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "auth" {
		return false, 0
	}
	return true, authoperator.Run(ctx, args[1:], server.ConfiguredDatabasePath(), stdout, stderr)
}
