// Command visual-companion is an MCP stdio server that gives a coding agent a
// browser tab for showing mockups, diagrams, and visual options during a
// brainstorming session.
//
// The agent never sees the page. It pushes HTML fragments; the user looks at
// them in a browser and clicks; the agent reads the clicks back. The browser
// keeps working between the agent's turns because the MCP host owns this
// process's lifetime.
//
// It builds to a single self-contained binary: assets are embedded, and the
// only runtime requirement is the Go standard library compiled into it.
package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ystyle/visual-companion/internal/companion"
)

//go:embed assets/frame-template.html
var frameTemplate []byte

//go:embed assets/helper.js
var helperScript []byte

// version is overridable at build time:
//
//	go build -ldflags "-X main.version=1.2.3"
var version = "dev"

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		sessionDir  = flag.String("session-dir", "", "pin sessions to this directory instead of discovering a workspace")
		projectDir  = flag.String("project-dir", "", "deprecated alias for --session-dir")
		host        = flag.String("host", "127.0.0.1", "interface to bind the browser server to")
		urlHost     = flag.String("url-host", "", "hostname to put in the URL given to the user")
		openBrowser = flag.Bool("open", false, "open the user's browser as soon as a session starts")
		noRoots     = flag.Bool("no-roots", false, "do not ask the MCP client for its workspace directory")
	)
	flag.Parse()

	if *showVersion {
		if version == "dev" {
			fmt.Println("visual-companion dev")
		} else {
			fmt.Printf("visual-companion v%s\n", version)
		}
		return
	}

	log.SetOutput(os.Stderr)
	log.SetPrefix("visual-companion: ")
	// The stdio transport owns stdout. Nothing else may write there, or it
	// corrupts the JSON-RPC stream.
	log.SetFlags(0)

	// Where sessions live is deliberately NOT decided here. Service-shaped
	// hosts (dsh, opencode) launch this process before a workspace exists, so
	// the only reliable source is the client's MCP roots, queried when
	// start_companion is called. An explicit flag still wins if given.
	pin := *sessionDir
	if pin == "" {
		pin = *projectDir
	}
	if pin != "" {
		if abs, err := filepath.Abs(pin); err == nil {
			pin = abs
		}
	}

	assets := companion.Assets{Frame: frameTemplate, Helper: helperScript}
	registry := companion.NewRegistry(assets, pin, *host, *urlHost, *openBrowser)
	registry.SetRootsDiscovery(!*noRoots)
	defer registry.CloseAll()

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "visual-companion",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: companion.ServerInstructions,
	})

	companion.RegisterTools(server, registry, version)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server stopped: %v", err)
	}
}
