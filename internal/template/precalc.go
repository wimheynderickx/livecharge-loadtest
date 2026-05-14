package template

import "regexp"

var (
	// reSession matches any reference to a .session.X variable in a template.
	reSession = regexp.MustCompile(`\.session\.\w+`)
	// reCtx captures the variable name after .ctx.
	reCtx = regexp.MustCompile(`\.ctx\.(\w+)`)
)

// IsStatic reports whether a template can be rendered once at startup and
// reused for every session.
//
// A template qualifies when:
//
//   - It contains no .session.X references (those are populated at runtime).
//   - Every .ctx.X reference points to a static scalar (no sequence or
//     random generator).
//
// staticCtx maps context keys to true when they are static. The map comes
// from ContextFactory.Definitions().
func IsStatic(tmpl string, staticCtx map[string]bool) bool {
	if reSession.MatchString(tmpl) {
		return false
	}
	matches := reCtx.FindAllStringSubmatch(tmpl, -1)
	for _, m := range matches {
		key := m[1]
		// Unknown key → treat as non-static (the template will fail at
		// render time, but that's a separate concern).
		if !staticCtx[key] {
			return false
		}
	}
	return true
}
