const $ = (id) => document.querySelector(`#${id}`);

// Disk/Git inspection only runs after an explicit action, never during render.
export function storagePanel({ api, getTown }) {
  let version = 0, controller = null, busy = false, inventory = null, townID = "";
  const selected = new Set();
  function controls() {
    $("storage-refresh").disabled = busy;
    $("storage-age").disabled = busy;
    $("storage-remove").disabled = busy || !selected.size;
    for (const input of $("storage-items").querySelectorAll("input")) input.disabled = busy || input.dataset.retained === "true";
  }
  function show(result) {
    inventory = result; selected.clear();
    $("storage-summary").textContent = Object.entries(result.roles).map(([role, usage]) => `${role}: ${usage.bytes.toLocaleString()} bytes, ${usage.files} files, ${usage.artifacts} artifacts`).join(" · ") || "No local worker artifacts.";
    $("storage-hold").textContent = result.incomplete ? "Inventory incomplete; no artifacts can be removed." : result.cleanup_hold || "Select eligible artifacts to remove. Task and write identities will be retained.";
    const rows = result.artifacts.map((artifact) => {
      const row = document.createElement("tr");
      const check = document.createElement("input"); check.type = "checkbox";
      check.dataset.retained = String(!artifact.eligible); check.disabled = !artifact.eligible;
      check.setAttribute("aria-label", `Remove ${artifact.path}`);
      check.onchange = () => { if (check.checked) selected.add(artifact.id); else selected.delete(artifact.id); controls(); };
      const choice = document.createElement("td"); choice.append(check); row.append(choice);
      for (const value of [artifact.path, artifact.task || "—", `${artifact.bytes.toLocaleString()} bytes`, artifact.modified.startsWith("0001-") ? "Unknown" : new Date(artifact.modified).toLocaleString(), artifact.reason]) {
        const cell = document.createElement("td"); cell.textContent = value; row.append(cell);
      }
      return row;
    });
    $("storage-items").replaceChildren(...rows); controls();
  }
  async function run(remove) {
    if (busy || !townID) return;
    const age = Number($("storage-age").value);
    if (!Number.isInteger(age) || age < 0 || age > 87600) { $("storage-error").textContent = "Enter a whole number of hours between 0 and 87600."; return; }
    if (remove && (!selected.size || age !== inventory?.minimum_age_hours)) { $("storage-error").textContent = "Refresh the inventory after changing its retention period."; return; }
    busy = true; controls();
    const generation = version;
    controller = new AbortController();
    const signal = AbortSignal.any([controller.signal, AbortSignal.timeout(45000)]);
    $("storage-error").textContent = "";
    $("storage-status").textContent = remove ? "Removing the selected artifacts…" : "Inspecting local storage…";
    try {
      const body = { town: townID, minimum_age_hours: age };
      if (remove) body.ids = [...selected];
      const result = await api(remove ? "/api/storage/cleanup" : "/api/storage", body, signal);
      if (generation !== version) return;
      if (remove) {
        $("storage-status").textContent = result.map((item) => `${item.id}: ${item.status}. ${item.detail}`).join("\n");
        selected.clear(); inventory = null; $("storage-items").replaceChildren();
        $("storage-hold").textContent = "Refresh storage to inspect the current files before another cleanup.";
      } else { show(result); $("storage-status").textContent = "Dry run complete. No files changed."; }
    } catch (error) {
      if (generation === version) {
        $("storage-status").textContent = "";
        $("storage-error").textContent = signal.aborted && remove ? "Cleanup was not confirmed. Refresh storage to inspect the outcome before trying again." : error.message;
        if (remove) { selected.clear(); inventory = null; $("storage-items").replaceChildren(); }
      }
    } finally { if (generation === version) { busy = false; controls(); } }
  }
  $("storage-refresh").onclick = () => run(false);
  $("storage-remove").onclick = () => run(true);
  $("storage-close").onclick = () => $("storage-dialog").close();
  $("storage-dialog").addEventListener("close", () => { version++; controller?.abort(); busy = false; selected.clear(); });
  $("town-storage").onclick = () => {
    const town = getTown(); if (!town) return;
    version++; controller?.abort(); busy = false; selected.clear(); inventory = null; townID = town.id;
    $("storage-town").textContent = town.id;
    $("storage-summary").textContent = ""; $("storage-items").replaceChildren();
    $("storage-status").textContent = ""; $("storage-error").textContent = "";
    $("storage-hold").textContent = "Inventory is a dry run. Pause all workers before cleanup.";
    controls(); $("storage-dialog").showModal(); void run(false);
  };
}
