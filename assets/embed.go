//go:build embedded_assets

package assets

import "embed"

// FS contains the browser assets needed by production builds.
//
// Build with `-tags embedded_assets` after generating css/output.css.
//
//go:embed css/output.css js/*.js fonts/*.woff2 fonts/COPYING.LIB fonts/README.md icons/express/*.png icons/express/*.svg icons/express/README.md brand/logo-*.svg logo.svg logo.png apple-touch-icon.png manifest.webmanifest
var FS embed.FS
