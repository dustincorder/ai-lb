package codex

import (
	"os"
	"strings"
)

var launcherEnvBlocked = map[string]struct{}{
	"CODEX_HOME":          {},
	"OPENAI_API_KEY":      {},
	"CODEX_API_KEY":       {},
	"OPENAI_BASE_URL":     {},
	"OPENAI_API_BASE":     {},
	"CODEX_BASE_URL":      {},
	"OPENAI_ORG_ID":       {},
	"OPENAI_ORGANIZATION": {},
	"OPENAI_PROJECT":      {},
}

// InteractiveEnvForLauncher preserves the user's normal interactive
// environment. Only variables that could replace the selected Codex home,
// credential, or API route are removed; terminal, proxy, CA, locale, and
// command runtime variables remain available to Codex and its children.
func InteractiveEnvForLauncher(codexHome string) []string {
	out := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if ok {
			if _, blocked := launcherEnvBlocked[key]; blocked {
				continue
			}
			out = append(out, kv)
		}
	}
	out = append(out, "CODEX_HOME="+codexHome)
	return out
}
