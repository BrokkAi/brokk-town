const $ = (id) => document.querySelector(`#${id}`);

// History is read only on explicit navigation. Normal rendering uses its count.
export function historyPanel({ api, getTown }) {
  let version = 0, abort = null, busy = false, town = "", next = "";
  function controls() {
    $("history-newest").disabled = busy;
    $("history-older").disabled = busy || !next;
  }
  async function load(after = "", task = "") {
    if (busy || !town) return;
    busy = true; controls();
    const generation = version;
    abort = new AbortController();
    const signal = AbortSignal.any([abort.signal, AbortSignal.timeout(30000)]);
    $("history-error").textContent = "";
    $("history-status").textContent = task ? "Loading archived task…" : "Loading saved history…";
    try {
      const body = task ? { town, task } : { town, after, limit: 50 };
      const result = await api("/api/history", body, signal);
      if (generation !== version) return;
      if (task) {
        $("history-detail").textContent = [`${result.id} · ${result.stage}`, result.title, result.detail, result.mayoral_decision ? `Mayor decision: ${result.mayoral_decision}` : "", result.audit?.summary || ""].filter(Boolean).join("\n\n");
      } else {
        next = result.next || "";
        $("history-detail").textContent = "";
        $("history-summary").textContent = `${result.total} archived tasks. Completed tasks leave active history after ${result.retention_days} days; unfinished work stays active.`;
        const rows = result.items.map((item) => {
          const row = document.createElement("tr");
          for (const text of [item.id, item.title, item.stage, new Date(item.updated).toLocaleDateString()]) {
            const cell = document.createElement("td"); cell.textContent = text; row.append(cell);
          }
          const cell = document.createElement("td"), button = document.createElement("button");
          button.type = "button"; button.textContent = "Inspect"; button.onclick = () => load("", item.id);
          cell.append(button); row.append(cell); return row;
        });
        $("history-items").replaceChildren(...rows);
      }
      $("history-status").textContent = "Saved history. Reopened work returns to the active town automatically.";
    } catch (error) {
      if (generation === version) { $("history-error").textContent = signal.aborted ? "History lookup stopped. You can refresh to try again." : error.message; $("history-status").textContent = ""; }
    } finally { if (generation === version) { busy = false; controls(); } }
  }
  $("history-newest").onclick = () => load();
  $("history-older").onclick = () => load(next);
  $("history-close").onclick = () => $("history-dialog").close();
  $("history-dialog").addEventListener("close", () => { version++; abort?.abort(); busy = false; });
  $("town-history").onclick = () => {
    const selected = getTown(); if (!selected) return;
    version++; abort?.abort(); busy = false; next = ""; town = selected.id;
    $("history-town").textContent = town; $("history-items").replaceChildren(); $("history-detail").textContent = ""; $("history-summary").textContent = "";
    controls(); $("history-dialog").showModal(); void load();
  };
}
