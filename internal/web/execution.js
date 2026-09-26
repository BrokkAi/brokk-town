const $ = (id) => document.querySelector(`#${id}`);

// Rendering consumes a snapshot only. Catalog reads and refresh polling live in
// cancellable async handlers; selecting placement never edits an agent profile.
export function executionControls({ api, getConfig, refresh }) {
  let town = "", role = "", config = {}, listing = null;
  let generation = 0, abort = null, timer = null, saving = false;
  const own = () => role ? config.bot_execution?.[role] : config.execution;
  const effective = () => own() ?? config.execution ?? {};
  function options(id, entries, selected, label) {
    const choices = entries.map((entry) => new Option(label(entry), entry.id));
    if (selected && !entries.some((entry) => entry.id === selected))
      choices.push(new Option(`Saved: ${selected} (missing from catalog)`, selected));
    $(id).replaceChildren(...choices);
    $(id).value = selected || entries[0]?.id || "";
  }
  function detail() {
    for (const id of ["execution-mode", "execution-target", "execution-profile"]) $(id).disabled = saving;
    $("refresh-execution").disabled = saving || !!listing?.refreshing || !listing?.configured;
    const mode = $("execution-mode").value;
    const managed = mode === "mjolnir";
    $("execution-target-field").hidden = !managed || (listing?.targets?.length === 1 && listing.targets[0].id === $("execution-target").value);
    $("execution-profile-field").hidden = !managed || (listing?.profiles?.length === 1 && listing.profiles[0].id === $("execution-profile").value);
    const selected = mode === "inherit" ? (config.execution || {}) : { target_id: $("execution-target").value, profile_id: $("execution-profile").value };
    const target = listing?.targets?.find((t) => t.id === selected.target_id);
    $("execution-detail").textContent = (managed || mode === "inherit") && selected.target_id
      ? `${selected.target_id} / ${selected.profile_id || "No profile"} · ${target?.availability || "missing from catalog"}${target?.unavailable_reason ? `: ${target.unavailable_reason}` : ""}`
      : "Runs directly on this machine.";
    $("execution-note").textContent = managed || (mode === "inherit" && selected.target_id)
      ? "Mjolnir execution is not available yet. This selection holds agent work until remote checkout and evidence support are ready. Repo Bot continues inventory reads. Availability is advisory; Mjolnir checks again at launch."
      : "Direct local execution uses the agent settings below and Town’s worker limit.";
    $("save-execution").disabled = saving || (managed && (!$("execution-target").value || !$("execution-profile").value));
  }
  function render(reset = false) {
    const targets = listing?.targets || [], profiles = listing?.profiles || [];
    const saved = own(), resolved = effective();
    const meaningful = targets.length > 1 || profiles.length > 1 || targets.some((t) => t.kind !== "local-bare");
    $("execution-settings").hidden = !(meaningful || saved || config.execution || listing?.error);
    const oldMode = $("execution-mode").value;
    $("execution-mode").replaceChildren(
      ...(role ? [new Option("Use town default", "inherit")] : []),
      new Option("Direct local execution", "local"), new Option("Mjolnir", "mjolnir"),
    );
    $("execution-mode").value = reset ? (role && !saved ? "inherit" : resolved.target_id ? "mjolnir" : "local") : oldMode;
    const target = reset ? resolved.target_id : $("execution-target").value;
    const profile = reset ? resolved.profile_id : $("execution-profile").value;
    options("execution-target", targets, target || listing?.default?.target_id, (t) => `${t.id} · ${t.availability}`);
    options("execution-profile", profiles, profile || listing?.default?.profile_id, (p) => `${p.id} · ${p.harness}`);
    $("execution-status").textContent = listing?.demo ? "Demo: Mjolnir stays offline."
      : listing?.error ? `${listing.error} Showing the last known options.`
      : listing?.refreshing ? "Refreshing Mjolnir options…"
      : listing?.stale ? "Cached options are stale."
      : listing?.fetched ? `Options fetched ${listing.fetched}.` : "No Mjolnir options loaded.";
    $("refresh-execution").disabled = saving || !!listing?.refreshing || !listing?.configured;
    detail();
  }
  async function load(force = false) {
    abort?.abort();
    clearTimeout(timer);
    const controller = new AbortController(), version = generation;
    abort = controller;
    try {
      const next = await api(force ? "/api/execution-options/refresh" : "/api/execution-options", force ? {} : undefined, controller.signal);
      if (controller.signal.aborted || generation !== version) return;
      listing = next;
      render();
      if (!force && next.configured && next.stale && !next.error && !next.refreshing) return load(true);
      if (next.refreshing) timer = setTimeout(() => { void load(); }, 1000);
    } catch (error) {
      if (!controller.signal.aborted && generation === version) {
        $("execution-settings").hidden = false;
        $("execution-status").textContent = error.message;
      }
    }
  }
  $("execution-mode").onchange = detail;
  $("execution-target").onchange = detail;
  $("execution-profile").onchange = detail;
  $("refresh-execution").onclick = () => load(true);
  $("save-execution").onclick = async () => {
    if (saving) return;
    const version = generation, id = town, selectedRole = role;
    const mode = $("execution-mode").value;
    const selection = mode === "inherit" ? null : mode === "local" ? { target_id: "", profile_id: "" }
      : { target_id: $("execution-target").value, profile_id: $("execution-profile").value };
    saving = true;
    $("execution-result").textContent = "";
    detail();
    const signal = AbortSignal.timeout(30000), expired = Symbol("expired execution save");
    let active = true, onTimeout;
    const timeout = new Promise((resolve) => {
      onTimeout = () => resolve(expired);
      signal.addEventListener("abort", onTimeout, { once: true });
    });
    try {
      const result = await Promise.race([timeout, (async () => {
        await api("/api/execution", { town: id, role: selectedRole, selection }, signal);
        if (!active) return;
        await refresh();
        if (!active || generation !== version) return;
        config = getConfig(id) || config;
        $("execution-result").textContent = "Execution location saved for future work.";
        render(true);
      })()]);
      if (result === expired && generation === version)
        $("execution-result").textContent = "Town did not answer within 30 seconds. The request may still have been applied; check its state before trying again.";
    } catch (error) {
      if (generation === version) $("execution-result").textContent = error.message;
    } finally {
      active = false;
      signal.removeEventListener("abort", onTimeout);
      if (generation === version) { saving = false; detail(); }
    }
  };
  return {
    show(id, nextRole, nextConfig) {
      generation++;
      abort?.abort();
      clearTimeout(timer);
      town = id; role = nextRole; config = getConfig(id) || nextConfig; saving = false;
      $("execution-result").textContent = "";
      render(true);
      void load();
    },
    load,
    close() { generation++; abort?.abort(); clearTimeout(timer); },
  };
}
