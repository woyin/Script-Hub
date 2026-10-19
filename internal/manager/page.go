package manager

import (
	_ "embed"
	"strings"
)

//go:embed index.html
var managementHTML string

//go:embed management.css
var managementCSS string

//go:embed management.js
var managementJS string

//go:embed icons.svg
var managementIcons string

// Inline assets keep Backend URL credentials out of asset requests and referrers.
var managementPage = strings.NewReplacer(
	"/* MANAGEMENT_CSS */", managementCSS,
	"/* MANAGEMENT_JS */", managementJS,
	"<!-- MANAGEMENT_ICONS -->", managementIcons,
).Replace(managementHTML)
