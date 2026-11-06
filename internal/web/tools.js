import { botNames, townSummary } from "./town.js";
export function registerTownTools(context, getState, selectTown, chooseHouse) {
  if (!context?.registerTool) return () => {};
  const lifecycle = new AbortController();
  const tools = [
    {
      name: "list_towns",
      description:
        "Read every repository: working bots, open tasks, triage decisions waiting, and blocked tasks. A repository's id is its owner/name.",
      inputSchema: {
        type: "object",
        properties: {},
        additionalProperties: false,
      },
      annotations: { readOnlyHint: true, untrustedContentHint: true },
      execute(input) {
        if (!input || Object.keys(input).length)
          throw new Error("Expected empty input");
        const state = getState();
        if (!state) throw new Error("Brokk Town has not connected");
        return {
          towns: Object.values(state.towns).map((t) => ({
            id: t.id,
            ...townSummary(t),
          })),
        };
      },
    },
    {
      name: "visit_town_house",
      description:
        "Navigate to a repository and open one of its bots. town is the repository id. house is the bot's role key: repo (Repo Bot), bug (Bug Bot), feature (Feature Bot), simplifier (Simplifier Bot), hall (Mayor Bot, triage), issue (Issue Bot), review (Review Bot) or release (Release Bot). Does not start or pause any bot.",
      inputSchema: {
        type: "object",
        properties: {
          town: { type: "string" },
          house: { type: "string", enum: Object.keys(botNames) },
        },
        required: ["town", "house"],
        additionalProperties: false,
      },
      annotations: { readOnlyHint: false, untrustedContentHint: true },
      execute(input) {
        if (
          !input ||
          Object.keys(input).some((k) => !["town", "house"].includes(k)) ||
          !getState()?.towns[input.town] ||
          !Object.hasOwn(botNames, input.house)
        )
          throw new Error("Unknown repository or bot");
        selectTown(input.town);
        chooseHouse(input.house);
        return { town: input.town, house: input.house };
      },
    },
  ];
  for (const tool of tools) {
    try {
      Promise.resolve(
        context.registerTool(tool, { signal: lifecycle.signal }),
      ).catch(() => {});
    } catch {}
  }
  globalThis.addEventListener?.("pagehide", () => lifecycle.abort(), {
    once: true,
  });
  return () => lifecycle.abort();
}
