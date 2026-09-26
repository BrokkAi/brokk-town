const $ = (id) => document.querySelector(`#${id}`);

export function attentionSettings({ api, getState, refresh }) {
  let generation = 0, abort = null, saving = false;
  function status(hook) {
    $("attention-hook-status").textContent = `${hook?.enabled ? "Enabled" : "Disabled"} · ${hook?.configured ? "private command saved" : "no command saved"}${getState()?.demo ? " · demo never runs hooks" : ""}.`;
  }
  $("attention-hook-form").onsubmit = async (event) => {
    event.preventDefault();
    if (saving) return;
    const body = { enabled: $("attention-hook-enabled").checked };
    const entered = $("attention-hook-command").value.trim();
    if (entered) {
      try { body.command = JSON.parse(entered); }
      catch { $("attention-hook-error").textContent = "Enter the command as a JSON array of strings."; return; }
      if (!Array.isArray(body.command) || !body.command.every((part) => typeof part === "string")) {
        $("attention-hook-error").textContent = "Enter the command as a JSON array of strings."; return;
      }
    }
    saving = true;
    const version = generation;
    abort = new AbortController();
    const signal = AbortSignal.any([abort.signal, AbortSignal.timeout(30000)]);
    for (const id of ["attention-hook-enabled", "attention-hook-command", "save-attention-hook"]) $(id).disabled = true;
    $("attention-hook-error").textContent = "";
    try {
      const saved = await api("/api/attention-hook", body, signal);
      if (version !== generation) return;
      $("attention-hook-command").value = "";
      status(saved);
      // A state refresh is independent of the confirmed settings receipt.
      void refresh().catch(() => {});
    } catch (error) {
      if (version === generation) $("attention-hook-error").textContent = signal.aborted
        ? "Town did not confirm the update. Check its saved state before trying again."
        : error.message;
    } finally {
      if (version === generation) {
        saving = false;
        for (const id of ["attention-hook-enabled", "attention-hook-command", "save-attention-hook"]) $(id).disabled = false;
      }
    }
  };
  $("capacity-dialog").addEventListener("close", () => {
    generation++; abort?.abort(); saving = false;
    $("attention-hook-command").value = "";
  });
  return {
    open() {
      generation++; abort?.abort(); saving = false;
      const hook = getState()?.service_config?.attention_hook;
      $("attention-hook-enabled").checked = !!hook?.enabled;
      $("attention-hook-command").value = "";
      $("attention-hook-error").textContent = "";
      for (const id of ["attention-hook-enabled", "attention-hook-command", "save-attention-hook"]) $(id).disabled = false;
      status(hook);
    },
  };
}
