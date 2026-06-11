export interface ModelOption {
  id: string;
  label: string;
}

export interface AgentKind {
  id: string;
  label: string;
  command: string;
  models: ModelOption[];
}

const ANTHROPIC_BARE: ModelOption[] = [
  { id: "claude-opus-4-8", label: "Claude Opus 4.8" },
  { id: "claude-sonnet-4-6", label: "Claude Sonnet 4.6" },
  { id: "claude-haiku-4-5", label: "Claude Haiku 4.5" },
];

// OpenCode/Pi expect provider/model ids; they add DeepSeek alongside Anthropic.
const PROVIDER_MODELS: ModelOption[] = [
  { id: "anthropic/claude-opus-4-8", label: "Claude Opus 4.8" },
  { id: "anthropic/claude-sonnet-4-6", label: "Claude Sonnet 4.6" },
  { id: "anthropic/claude-haiku-4-5", label: "Claude Haiku 4.5" },
  { id: "deepseek/deepseek-chat", label: "DeepSeek Chat" },
  { id: "deepseek/deepseek-reasoner", label: "DeepSeek Reasoner" },
  { id: "deepseek/deepseek-v4-flash", label: "DeepSeek V4 Flash" },
  { id: "deepseek/deepseek-v4-pro", label: "DeepSeek V4 Pro" },
];

export const AGENT_KINDS: AgentKind[] = [
  {
    id: "claude-code",
    label: "Claude Code",
    command: `claude -p "$(cat {prompt_file})" --dangerously-skip-permissions --model {model}`,
    models: ANTHROPIC_BARE,
  },
  {
    id: "opencode",
    label: "OpenCode",
    command: `opencode run "$(cat {prompt_file})" --model {model}`,
    models: PROVIDER_MODELS,
  },
  {
    id: "pi",
    label: "Pi",
    command: `pi "$(cat {prompt_file})" --model {model}`,
    models: PROVIDER_MODELS,
  },
  {
    id: "codex",
    label: "Codex",
    command: `codex exec "$(cat {prompt_file})" --model {model} --dangerously-bypass-approvals-and-sandbox`,
    models: [
      { id: "gpt-5-codex", label: "GPT-5 Codex" },
      { id: "gpt-5", label: "GPT-5" },
      { id: "gpt-5-mini", label: "GPT-5 Mini" },
    ],
  },
];
