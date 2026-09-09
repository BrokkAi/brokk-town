import { houseNames, townSummary } from "./town.js";
export function registerTownTools(context, getState, selectTown, chooseHouse) {
  if (!context?.registerTool) return () => {};
  const lifecycle = new AbortController();
  const tools = [
    {
      name: "list_towns",
      description:
        "Read repository towns, worker activity, queues, and attention counts.",
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
        if (!state) throw new Error("Town service has not connected");
        return {
          demo: state.demo,
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
        "Navigate to a repository town and inspect a house. Does not start or stop workers.",
      inputSchema: {
        type: "object",
        properties: {
          town: { type: "string" },
          house: { type: "string", enum: Object.keys(houseNames) },
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
          !Object.hasOwn(houseNames, input.house)
        )
          throw new Error("Unknown town or house");
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
