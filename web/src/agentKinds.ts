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

// Full provider catalog for OpenCode (models.dev-style provider/model slugs).
export const OPENCODE_MODELS: ModelOption[] = [
  // Anthropic
  { id: "anthropic/claude-opus-4-8", label: "Claude Opus 4.8" },
  { id: "anthropic/claude-sonnet-4-6", label: "Claude Sonnet 4.6" },
  { id: "anthropic/claude-haiku-4-5", label: "Claude Haiku 4.5" },
  // OpenAI
  { id: "openai/gpt-4o", label: "GPT-4o" },
  { id: "openai/gpt-4o-mini", label: "GPT-4o Mini" },
  { id: "openai/o3", label: "OpenAI o3" },
  { id: "openai/o4-mini", label: "OpenAI o4 Mini" },
  // Google Gemini
  { id: "google/gemini-2.5-pro", label: "Gemini 2.5 Pro" },
  { id: "google/gemini-2.5-flash", label: "Gemini 2.5 Flash" },
  { id: "google/gemini-2.0-flash", label: "Gemini 2.0 Flash" },
  // DeepSeek
  { id: "deepseek/deepseek-chat", label: "DeepSeek Chat" },
  { id: "deepseek/deepseek-reasoner", label: "DeepSeek Reasoner" },
  // xAI Grok
  { id: "xai/grok-3", label: "Grok 3" },
  { id: "xai/grok-3-mini", label: "Grok 3 Mini" },
  // Mistral
  { id: "mistral/mistral-large-latest", label: "Mistral Large" },
  // Groq
  { id: "groq/llama-3.3-70b-versatile", label: "Llama 3.3 70B (Groq)" },
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
    models: OPENCODE_MODELS,
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
