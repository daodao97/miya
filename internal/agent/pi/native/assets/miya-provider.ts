// Miya's pi extension: registers the provider Miya gives pi — one configured
// in Miya's settings, else the Anthropic-compatible endpoint of Miya's
// Claude Code settings. Miya passes the endpoint, protocol and models in the
// environment; the token stays in MIYA_PI_API_KEY and is never written here.
export default function (pi: any) {
  const baseUrl = process.env.MIYA_PI_BASE_URL;
  if (!baseUrl) return;
  let ids: string[] = [];
  try {
    ids = JSON.parse(process.env.MIYA_PI_MODELS || "[]");
  } catch {
    ids = [];
  }
  const api = process.env.MIYA_PI_API || "anthropic-messages";
  const config: any = {
    name: process.env.MIYA_PI_NAME || "Claude Code (Miya)",
    baseUrl,
    api,
    apiKey: "MIYA_PI_API_KEY",
    models: ids.map((id) => ({
      id,
      name: id,
      reasoning: true,
      input: ["text", "image"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 200000,
      maxTokens: 32000,
    })),
  };
  if (process.env.MIYA_PI_BEARER === "1") config.authHeader = true;
  pi.registerProvider(process.env.MIYA_PI_PROVIDER || "miya-claude", config);
}
