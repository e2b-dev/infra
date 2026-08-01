// View model for provision.sh: the profile registry prepared for template
// splicing, with all shell-quoting done here in Go where it is testable.
package distro

import (
	"fmt"
	"strings"
)

// ProfileView is one profile prepared for provision.sh's selection template.
// Quoted fields carry their own double quotes (TestQuotedFieldsAreShellSafe
// pins the %q == sh-double-quote assumption); fragment fields are raw shell.
type ProfileView struct {
	Key          string
	CasePattern  string // IDs joined with "|" — the unquoted case-arm pattern
	Bootstrap    string // raw shell; empty for all but nixos
	Packages     string // sh-quoted, space-joined package list
	PkgQuery     string // raw shell, spliced into { ...; }
	PkgInstall   string // raw shell, spliced into { ...; }
	InitBinary   string // sh-quoted
	TimeSyncUnit string // sh-quoted
	SSHUnit      string // sh-quoted
	AdminGroup   string // sh-quoted
	CABundle     string // sh-quoted
	CARefresh    string // raw shell, spliced into { ...; }
	InitSystem   string // sh-quoted
	InitSetup    string // init-<init>.sh body, pre-indented for the function body
}

// TemplateData is everything the distro registry contributes to provision.sh.
type TemplateData struct {
	Profiles           []ProfileView
	RejectedIDsPattern string // e.g. "rhel|ol|amzn"
	SupportedIDs       string // comma-joined, for the fatal no-match message
}

// NewTemplateData builds the provision.sh view of the profile registry.
func NewTemplateData() TemplateData {
	views := make([]ProfileView, 0, len(Profiles))
	for _, p := range Profiles {
		views = append(views, ProfileView{
			Key:          p.Key,
			CasePattern:  strings.Join(p.IDs, "|"),
			Bootstrap:    p.Bootstrap,
			Packages:     shQuote(strings.Join(p.Packages, " ")),
			PkgQuery:     p.PkgQueryBody,
			PkgInstall:   p.PkgInstall,
			InitBinary:   shQuote(p.InitBinary),
			TimeSyncUnit: shQuote(p.TimeSyncUnit),
			SSHUnit:      shQuote(p.SSHUnit),
			AdminGroup:   shQuote(p.AdminGroup),
			CABundle:     shQuote(p.CABundle),
			CARefresh:    p.CARefresh,
			InitSystem:   shQuote(string(p.Init)),
			InitSetup:    indentBlock(initSetup[p.Init], "        "),
		})
	}

	return TemplateData{
		Profiles:           views,
		RejectedIDsPattern: strings.Join(RejectedIDs, "|"),
		SupportedIDs:       strings.Join(SupportedIDs(), ", "),
	}
}

// shQuote double-quotes a value for sh. Go %q matches plain sh double-quoting
// only for values without `"`, `\`, `$`, backticks or control characters —
// enforced by TestQuotedFieldsAreShellSafe.
func shQuote(s string) string {
	return fmt.Sprintf("%q", s)
}
