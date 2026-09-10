import { safeURL } from "./town.js";

const $ = (s) => document.querySelector(s);
const esc = (v) =>
  String(v ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const profileNames = {
  "": "Town defaults",
  bug: "Bug Bot",
  feature: "Feature Bot",
  issue: "Issue Bot",
  review: "Review Bot",
  release: "Release Bot",
};

function agentDraft(config, role) {
  const profile = (role && config.bot_agents?.[role]) || config;
  return {
    harness: profile.harness || "codex-acp",
    version: profile.harness_version || "",
    model: profile.model || "",
    effort: profile.effort || "",
    command: "",
    inherited: !!role && profile.inherited !== false,
    dirty: false,
  };
}

export function management({ api, getTown, getState, refresh }) {
  let settingsTown = "",
    settingsRole = "",
    settingsVersion = 0,
    settingsSaving = false,
    settingsConfig = null,
    drafts = {},
    requestTown = "",
    choicesVersion = 0,
    choicesAbort = null,
    choicesLoading = false,
    loaded = false;
  let catalog = null,
    catalogVersion = 0,
    catalogAbort = null,
    catalogLoading = false,
    savedHarness = "",
    savedVersion = "",
    selectedVersion = null;
  const pendingReset = () =>
    !!drafts[settingsRole]?.inherited && drafts[settingsRole].dirty;
  function syncProfileControls() {
    for (const id of ["harness-input", "model-input", "effort-input", "command-input", "update-harness"])
      $("#" + id).disabled = settingsSaving || pendingReset();
    $("#load-choices").disabled = settingsSaving || pendingReset() || choicesLoading;
  }
  function renderProfileStatus() {
    const draft = drafts[settingsRole];
    for (const option of $("#agent-role").options) {
      const other = drafts[option.value];
      option.textContent = `${profileNames[option.value]}${option.value ? (other.inherited ? " · town defaults" : " · custom") : ""}${other.dirty ? " · unsaved" : ""}`;
    }
    $("#agent-profile-status").textContent =
      `${settingsRole ? (draft.inherited ? "Uses town defaults" : "Custom agent profile") : "Default agent for bots without an override"}${draft.dirty ? " · unsaved changes" : ""}.`;
    $("#inherit-agent").hidden = !settingsRole || draft.inherited;
    $("#save-agent").textContent =
      `Save ${settingsRole ? profileNames[settingsRole] : "town defaults"}`;
    $("#agent-profile-note").textContent = pendingReset()
      ? "Save to use town defaults before customizing this bot."
      : settingsRole
        ? `${profileNames[settingsRole]} uses this profile on its next run. Editing an inherited profile creates a custom profile for this bot.`
        : "Town defaults apply on the next run to bots that use them. Custom bot profiles keep their own settings. Repo Bot does not use an agent.";
    syncProfileControls();
  }
  function rememberDraft() {
    if (!drafts[settingsRole]) return;
    Object.assign(drafts[settingsRole], {
      harness: $("#harness-input").value,
      version: selectedVersion || "",
      model: $("#model-input").value,
      effort: $("#effort-input").value,
      command: $("#command-input").value,
    });
  }
  function markEdited() {
    rememberDraft();
    drafts[settingsRole].inherited = false;
    drafts[settingsRole].dirty = true;
    $("#settings-success").textContent = "";
    renderProfileStatus();
  }
  const cancelChoices = () => {
    choicesVersion++;
    choicesAbort?.abort();
    choicesAbort = null;
    choicesLoading = false;
    $("#load-choices").disabled = settingsSaving || pendingReset();
  };
  $("#settings-dialog").addEventListener("close", cancelChoices);
  $("#settings-dialog").addEventListener("close", () => {
    catalogVersion++;
    catalogAbort?.abort();
    catalogLoading = false;
  });

  function harnessDetail() {
    const id = $("#harness-input").value,
      entry = catalog?.agents.find((a) => a.id === id);
    $("#harness-detail").textContent = entry
      ? `${entry.description} ${entry.setup}`
      : "";
    const version =
      selectedVersion ||
      (id === savedHarness && savedVersion ? savedVersion : entry?.version);
    $("#harness-version").textContent =
      version === "installed"
        ? "Uses your installed version."
        : version
          ? `Selected version: ${version}.`
          : "";
    $("#update-harness").hidden =
      !entry || !version || version === entry.version;
    $("#update-harness").textContent = entry
      ? `Use registry version ${entry.version}`
      : "Use registry version";
    $("#harness-project").hidden = !safeURL(entry?.repository);
    if (safeURL(entry?.repository))
      $("#harness-project").href = entry.repository;
  }
  function renderCatalog() {
    const select = $("#harness-input"),
      selected = select.value || savedHarness;
    const options = [];
    for (const [source, label] of [
      ["registry", "Official ACP registry"],
      ["additional", "Additional harnesses"],
    ]) {
      const group = document.createElement("optgroup");
      group.label = label;
      for (const agent of catalog.agents.filter((a) => a.source === source)) {
        const option = new Option(
          `${agent.name}${agent.available ? "" : " — unavailable on this platform"}`,
          agent.id,
        );
        option.disabled = !agent.available;
        group.append(option);
      }
      options.push(group);
    }
    if (selected !== "custom" && !catalog.agents.some((a) => a.id === selected))
      options.push(new Option(`Saved harness: ${selected}`, selected));
    options.push(new Option("Custom ACP command", "custom"));
    select.replaceChildren(...options);
    select.value = selected;
    harnessDetail();
  }
  async function loadCatalog(refresh = false) {
    catalogAbort?.abort();
    catalogAbort = new AbortController();
    const version = ++catalogVersion;
    catalogLoading = true;
    $("#refresh-harnesses").disabled = true;
    $("#registry-status").textContent = refresh
      ? "Refreshing the official registry…"
      : "Loading harnesses…";
    try {
      const next = await api(
        refresh ? "/api/harnesses/refresh" : "/api/harnesses",
        refresh ? {} : undefined,
        catalogAbort.signal,
      );
      if (version !== catalogVersion) return;
      catalog = next;
      renderCatalog();
      $("#registry-status").textContent = next.demo
        ? "Bundled registry · demo is offline"
        : next.stale
          ? "Using bundled or cached registry"
          : `Registry updated ${new Date(next.fetched).toLocaleDateString()}`;
      if (!refresh && next.stale && !next.demo) {
        void loadCatalog(true);
        return;
      }
    } catch (e) {
      if (version === catalogVersion)
        $("#registry-status").textContent = e.message;
    } finally {
      if (version === catalogVersion) {
        catalogLoading = false;
        $("#refresh-harnesses").disabled = settingsSaving;
      }
    }
  }
  $("#refresh-harnesses").onclick = () => loadCatalog(true);
  $("#update-harness").onclick = () => {
    const entry = catalog?.agents.find(
      (a) => a.id === $("#harness-input").value,
    );
    if (entry) {
      selectedVersion = entry.version;
      cancelChoices();
      loaded = false;
      markEdited();
      harnessDetail();
    }
  };
  const draftKey = () => `brokk-town-request:${requestTown}`;
  const readAgent = () => {
    if (settingsRole && drafts[settingsRole].inherited)
      return { inherit: true };
    const agent = {
      harness: $("#harness-input").value,
      model: $("#model-input").value.trim(),
      effort: $("#effort-input").value.trim(),
    };
    if (selectedVersion && agent.harness !== "custom")
      agent.version = selectedVersion;
    if (agent.harness === "custom" && $("#command-input").value.trim()) {
      agent.command = JSON.parse($("#command-input").value);
      if (
        !Array.isArray(agent.command) ||
        !agent.command.every((a) => typeof a === "string")
      )
        throw new Error("Enter the command as a JSON array of strings.");
    }
    return agent;
  };
  const busyForm = async (event, errorID, action, isCurrent = () => true) => {
    event.preventDefault();
    const fields = [...event.target.querySelectorAll("input,select,textarea,button")];
    const disabled = fields.map((f) => f.disabled);
    fields.forEach((f) => {
      f.disabled = true;
    });
    $(errorID).textContent = "";
    try {
      await action();
    } catch (e) {
      if (isCurrent()) $(errorID).textContent = e.message;
    } finally {
      if (isCurrent()) {
        fields.forEach((f, i) => {
          f.disabled = disabled[i];
        });
        if (event.target === $("#settings-form")) {
          syncProfileControls();
          $("#refresh-harnesses").disabled = catalogLoading;
        }
      }
    }
  };
  document.querySelectorAll("[data-close]").forEach((b) => {
    b.onclick = () => $("#" + b.dataset.close).close();
  });

  function showProfile(role) {
    settingsRole = role;
    const draft = drafts[role];
    savedHarness = draft.harness;
    savedVersion = draft.version;
    selectedVersion = draft.version || null;
    cancelChoices();
    loaded = false;
    $("#agent-role").value = role;
    $("#harness-input").replaceChildren(new Option(savedHarness, savedHarness));
    $("#harness-input").value = savedHarness;
    $("#model-input").value = draft.model;
    $("#effort-input").value = draft.effort;
    $("#command-input").value = draft.command;
    $("#custom-agent").hidden = $("#harness-input").value !== "custom";
    $("#settings-error").textContent = "";
    $("#choices-status").textContent = "";
    $("#model-choices").replaceChildren();
    $("#effort-choices").replaceChildren();
    $("#load-choices").disabled = false;
    if (catalog) renderCatalog();
    else harnessDetail();
    renderProfileStatus();
  }
  function openSettings(role = "") {
    const t = getTown();
    if (!t) return;
    settingsTown = t.id;
    settingsVersion++;
    settingsSaving = false;
    settingsConfig = t.config;
    drafts = Object.fromEntries(
      Object.keys(profileNames).map((r) => [r, agentDraft(t.config, r)]),
    );
    $("#settings-form").reset();
    $("#settings-form").querySelectorAll("input,select,textarea,button")
      .forEach((field) => { field.disabled = false; });
    $("#settings-repo").textContent = t.config.repo;
    $("#settings-success").textContent = "";
    showProfile(Object.hasOwn(profileNames, role) ? role : "");
    $("#settings-dialog").showModal();
    void loadCatalog();
  }
  $("#town-settings").onclick = () => openSettings();
  document.addEventListener("open-settings", (event) =>
    openSettings(event.detail?.role),
  );
  $("#agent-role").onchange = () => {
    rememberDraft();
    $("#settings-success").textContent = "";
    showProfile($("#agent-role").value);
  };
  $("#inherit-agent").onclick = () => {
    drafts[settingsRole] = {
      ...agentDraft(settingsConfig, ""),
      inherited: true,
      dirty: true,
    };
    $("#settings-success").textContent = "";
    showProfile(settingsRole);
  };
  $("#harness-input").onchange = () => {
    selectedVersion =
      catalog?.agents.find((a) => a.id === $("#harness-input").value)
        ?.version || null;
    cancelChoices();
    loaded = false;
    $("#custom-agent").hidden = $("#harness-input").value !== "custom";
    for (const id of ["model-input", "effort-input", "command-input"])
      $("#" + id).value = "";
    for (const id of ["model-choices", "effort-choices"])
      $("#" + id).replaceChildren();
    $("#choices-status").textContent = "";
    $("#load-choices").disabled = false;
    markEdited();
    harnessDetail();
  };
  async function loadChoices() {
    cancelChoices();
    const version = ++choicesVersion;
    let agent;
    try {
      agent = readAgent();
    } catch (e) {
      $("#settings-error").textContent = e.message;
      return;
    }
    $("#load-choices").disabled = true;
    choicesLoading = true;
    $("#choices-status").textContent =
      "Preparing the harness and loading choices…";
    $("#settings-error").textContent = "";
    choicesAbort = new AbortController();
    try {
      const result = await api(
        "/api/choices",
        { town: settingsTown, role: settingsRole, agent },
        choicesAbort.signal,
      );
      if (version !== choicesVersion) return;
      for (const [id, values] of [
        ["model-choices", result.models],
        ["effort-choices", result.efforts],
      ]) {
        $("#" + id).replaceChildren(
          ...values.map((v) => {
            const o = document.createElement("option");
            o.value = v.value;
            o.label = v.name;
            return o;
          }),
        );
      }
      loaded = true;
      $("#choices-status").textContent =
        result.models.length || result.efforts.length
          ? "Choices loaded. Select a field to choose."
          : "This harness does not advertise selectors. Use its defaults.";
    } catch (e) {
      if (version === choicesVersion)
        $("#choices-status").textContent = e.message;
    } finally {
      if (version === choicesVersion) {
        choicesLoading = false;
        $("#load-choices").disabled = settingsSaving || pendingReset();
      }
    }
  }
  $("#load-choices").onclick = loadChoices;
  $("#model-input").oninput = () => {
    markEdited();
    cancelChoices();
    $("#effort-choices").replaceChildren();
    $("#choices-status").textContent =
      "Load choices for this model’s effort levels.";
  };
  $("#command-input").oninput = () => {
    markEdited();
    cancelChoices();
    loaded = false;
  };
  $("#model-input").onchange = () => {
    $("#effort-input").value = "";
    markEdited();
    if (loaded) loadChoices();
  };
  $("#effort-input").oninput = markEdited;
  $("#settings-form").onsubmit = (e) => {
    const version = settingsVersion;
    return busyForm(e, "#settings-error", async () => {
      const role = settingsRole;
      rememberDraft();
      settingsSaving = true;
      cancelChoices();
      try {
        await api("/api/settings", { town: settingsTown, role, agent: readAgent() });
        if (version === settingsVersion)
          $("#settings-success").textContent = `${profileNames[role]} saved.`;
        await refresh();
        if (version !== settingsVersion || !$("#settings-dialog").open) return;
        const config = getState()?.towns[settingsTown]?.config;
        if (!config) return;
        settingsConfig = config;
        for (const [r, draft] of Object.entries(drafts)) {
          if (r === role || !draft.dirty) drafts[r] = agentDraft(config, r);
          else if (draft.inherited)
            drafts[r] = { ...agentDraft(config, ""), inherited: true, dirty: true };
        }
        showProfile(role);
      } finally {
        if (version === settingsVersion) settingsSaving = false;
      }
    }, () => version === settingsVersion);
  };
  $("#open-delete").onclick = () => {
    $("#delete-repo").textContent = settingsTown;
    $("#delete-error").textContent = "";
    $("#settings-dialog").close();
    $("#delete-dialog").showModal();
  };
  $("#delete-form").onsubmit = (e) =>
    busyForm(e, "#delete-error", async () => {
      await api("/api/control", {
        town: settingsTown,
        role: "all",
        action: "delete",
      });
      $("#delete-dialog").close();
      await refresh();
    });

  function requestPlaceholder() {
    $("#request-body").placeholder =
      $("#request-kind").value === "bug"
        ? "What happened? What did you expect? Include reproduction steps and any useful logs."
        : "What should change, and what would a good result look like?";
  }
  $("#request-kind").onchange = requestPlaceholder;
  $("#new-request").onclick = () => {
    const t = getTown();
    if (!t) return;
    requestTown = t.id;
    $("#request-form").reset();
    try {
      const draft = JSON.parse(sessionStorage.getItem(draftKey()));
      if (draft)
        for (const field of ["kind", "title", "body"])
          $("#request-" + field).value = draft[field];
    } catch {
      /* A broken local draft must not prevent opening the form. */
    }
    $("#request-repo").textContent = t.config.repo;
    $("#request-error").textContent = "";
    $("#request-success").textContent = "";
    const demo = getState().demo;
    $("#submit-request").textContent = demo
      ? "Create demo issue"
      : "Create GitHub issue";
    $("#request-note").textContent = demo
      ? "Demo: this creates a simulated issue in the workshop. Nothing is sent to GitHub."
      : "This posts an issue to this repository and adds it to the workshop queue. If issue-bot is paused, wake it when you’re ready for implementation.";
    requestPlaceholder();
    renderRequests();
    $("#request-dialog").showModal();
  };
  $("#request-form").onsubmit = (e) =>
    busyForm(e, "#request-error", async () => {
      const targetTown = requestTown,
        storageKey = draftKey();
      const draft = {
        kind: $("#request-kind").value,
        title: $("#request-title").value.trim(),
        body: $("#request-body").value.trim(),
      };
      let previous;
      try {
        previous = JSON.parse(sessionStorage.getItem(storageKey));
      } catch {
        /* Start a new draft. */
      }
      draft.id =
        previous &&
        ["kind", "title", "body"].every((k) => previous[k] === draft[k])
          ? previous.id
          : crypto.randomUUID();
      sessionStorage.setItem(storageKey, JSON.stringify(draft));
      const result = await api("/api/requests", {
        town: targetTown,
        ...draft,
      });
      sessionStorage.removeItem(storageKey);
      if (requestTown !== targetTown) {
        await refresh();
        return;
      }
      $("#request-form").reset();
      requestPlaceholder();
      $("#request-success").textContent =
        result.status === "confirmed"
          ? "Issue added to the workshop."
          : "Submission saved. GitHub confirmation will appear below.";
      await refresh();
      renderRequests();
    });

  function renderRequests() {
    const t = getState()?.towns[requestTown];
    const requests = Object.values(t?.requests || {})
      .sort((a, b) => Date.parse(b.created) - Date.parse(a.created))
      .slice(0, 12);
    $("#request-history").innerHTML =
      requests
        .map(
          (r) =>
            `<article class="submission"><strong>${esc(r.title)}</strong><small>${esc(r.kind)} · ${esc(r.status)}${r.number ? ` · #${r.number}` : ""}</small><p>${esc(r.detail || "Waiting to contact GitHub…")}</p>${safeURL(r.url) ? `<a href="${esc(r.url)}" target="_blank" rel="noopener noreferrer">Open issue ↗</a>` : ""}${r.status === "uncertain" ? `<button type="button" data-recheck="${esc(r.id)}">Check GitHub for receipt</button>` : ""}</article>`,
        )
        .join("") || '<p class="muted">Your submissions will appear here.</p>';
    $("#request-history")
      .querySelectorAll("[data-recheck]")
      .forEach((b) => {
        b.onclick = async () => {
          b.disabled = true;
          try {
            await api("/api/requests/check", {
              town: requestTown,
              id: b.dataset.recheck,
            });
            $("#request-success").textContent =
              "Receipt check queued. This only reads GitHub.";
          } catch (e) {
            $("#request-error").textContent = e.message;
          } finally {
            b.disabled = false;
          }
        };
      });
  }
  return () => {
    const t = getTown();
    $("#town-settings").disabled = !t;
    $("#new-request").disabled = !t;
    if ($("#request-dialog").open) renderRequests();
  };
}
