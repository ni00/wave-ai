# Console Instructions

Run the local server yourself and open the preview in the browser available to this environment. Do not give the user server-start instructions when you can run it.

Before making substantial visual changes, use the Product Design plugin's `get-context` skill when the visual source is unclear or no longer matches the current goal. When the user gives durable prototype-specific design feedback, preferences, or decisions, record them in `AGENTS.md`.

When implementing from a selected generated mock, treat that image as the source of truth for layout, component anatomy, density, spacing, color, typography, visible content, and hierarchy.

Build app UI in `src/`. The console is embedded in Wave and served at `/console/`.

## Console copy

Keep interface text concise and functional. Do not add slogans, promotional copy, decorative subtitles, or explanations that repeat a page title or action. Use short labels and empty states; retain brief error, data-limit, and operational notices only where they affect the user's next action or interpretation of data.

Use English consistently throughout the sidebar, including section headings, menu items, and footer actions.

Use “Benchmarks” for the console navigation and page name. Keep the CLI command `wave bench`.

Use the shared `PageHeader` for every workspace page. Take its title and icon from navigation; keep heading typography, icon size, spacing, and alignment consistent across resource and monitoring pages.

Use consistent, slim native scrollbars across workspace panels: rounded gray thumbs, transparent tracks, and darker hover/drag states. Preserve native scrolling and system high-contrast styling.

Keep build artifacts, Git submission rules, and asset caching internals out of user guides. Put contributor instructions in `AGENTS.md`.

## Build integration

Keep frontend sources, generated assets, and the Go embedding entry point in this directory. `npm run build` writes browser assets to `dist/client` and gzip assets to `dist/compressed`; `embed.go` embeds that compressed directory. Track only `dist/compressed/.gitkeep` so Go tooling can load the package before the frontend build. Preserve this placeholder when cleaning generated assets. Do not commit generated assets or copy them into `internal/`. The `internal/platform/webui` package serves the embedded assets over HTTP.

Run `make build`, `make test`, or `make check` from the repository root to build the frontend before compiling Go. These commands require Node.js 22+ and npm as well as Go. Before invoking Go commands directly on packages that import the console, run `make web-build`. Docker builds the frontend in its Node stage and copies the compressed assets into the Go stage.
