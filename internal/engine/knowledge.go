package engine

import "log"

// knowledgeText resolves the project knowledge base for the planner/implementer
// prompts. It returns "" when no config is present, and logs and returns "" if
// resolving the configured knowledge (inline text or external file) fails.
func (e *Engine) knowledgeText() string {
	if e.cfg == nil {
		return ""
	}
	k, err := e.cfg.ResolveKnowledge(e.repoRoot)
	if err != nil {
		log.Printf("engine: resolve project knowledge: %v", err)
		return ""
	}
	return k
}
