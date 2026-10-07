import { botNames, townSummary } from "./town.js";
export function registerTownTools(context, getState, selectTown, chooseHouse) {
  if (!context?.registerTool) return () => {};
  const lifecycle = new AbortController();
  const tools = [
    {
      name: "list_towns",
      description:
        "Read every precinct: units on a case, open cases, rulings awaiting a decision, and stuck cases. A precinct's id is its repository.",
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
        if (!state) throw new Error("SlopCop Squad has not connected");
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
        "Navigate to a repository's precinct and open one of its units. town is the precinct id (its repository). house is the unit's role key: repo (Patrol), bug (Detectives), feature (Intel), simplifier (Slop Squad), hall (Courthouse), issue (Task Force), review (Forensics) or release (Release). Does not deploy or stand down any unit.",
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
          throw new Error("Unknown precinct or unit");
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
