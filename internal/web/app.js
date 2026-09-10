import {
  positions,
  houseNames,
  houseShortcuts,
  routePosition,
  queueFor,
  visibleEvents,
  safeURL,
  townSummary,
} from "./town.js";
import { management } from "./manage.js";
import { landscape, drawWorking, easeDelivery } from "./scenery.js";
const $ = (s) => document.querySelector(s),
  esc = (value) =>
    String(value ?? "").replace(
      /[&<>"']/g,
      (c) =>
        ({
          "&": "&amp;",
          "<": "&lt;",
          ">": "&gt;",
          '"': "&quot;",
          "'": "&#39;",
        })[c],
    );
let token =
  new URLSearchParams(location.hash.slice(1)).get("token") ||
  sessionStorage.getItem("brokk-town-token") ||
  "";
if (token) {
  sessionStorage.setItem("brokk-town-token", token);
  history.replaceState(null, "", location.pathname);
}
let overview = true;
let state = null,
  selectedTown = "",
  selectedHouse = "hall",
  selectedTask = "",
  sequence = null,
  moving = [],
  motion = !matchMedia("(prefers-reduced-motion: reduce)").matches,
  streamAbort = null;
const canvas = $("#world"),
  ctx = canvas.getContext("2d"),
  buildings = new Image(),
  actors = new Image(),
  featureStudy = new Image(),
  featureReader = new Image();
buildings.src = "/assets/buildings-atlas.png";
actors.src = "/assets/actors-atlas.png";
featureStudy.src = "/assets/feature-study.png";
featureReader.src = "/assets/feature-reader.png";
const crops = [
  [0, 0, 512, 512],
  [512, 0, 512, 480],
  [1024, 0, 512, 512],
  [0, 512, 512, 512],
  [512, 480, 512, 544],
  [1024, 512, 512, 512],
];
let field = null,
  fieldTown = null;
const actorCrops = [
  [116, 145, 333, 296],
  [86, 145, 311, 293],
  [175, 145, 213, 296],
  [49, 77, 428, 327],
  [62, 95, 434, 311],
  [163, 110, 232, 306],
];
const indices = {
  bug: 0, issue: 1, review: 2, release: 3, repo: 4, hall: 5, feature: 6,
};
async function api(path, body, signal) {
  const response = await fetch(path, {
    method: body ? "POST" : "GET",
    headers: {
      Authorization: `Bearer ${token}`,
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
    signal,
  });
  if (!response.ok) {
    const error = await response
      .json()
      .catch(() => ({ error: response.statusText }));
    throw new Error(error.error || response.statusText);
  }
  return response.json();
}
function town() {
  return state?.towns[selectedTown];
}
const renderManagement = management({
  api,
  getTown: town,
  getState: () => state,
  refresh: async () => receive(await api("/api/state")),
});
function showError(message) {
  $("#error").textContent = message;
  $("#error").hidden = !message;
}
async function connect() {
  if (!token) {
    $("#connect-dialog").showModal();
    return;
  }
  streamAbort?.abort();
  streamAbort = new AbortController();
  try {
    const response = await fetch("/api/events", {
      headers: { Authorization: `Bearer ${token}` },
      signal: streamAbort.signal,
    });
    if (!response.ok) {
      if (response.status === 401) {
        $("#connect-dialog").showModal();
        return;
      }
      throw new Error("Town service unavailable");
    }
    $("#connection").textContent = "Connected";
    $("#connection-dot").className = "dot active";
    let buffer = "";
    const reader = response.body.getReader(),
      decoder = new TextDecoder();
    for (;;) {
      const { value, done } = await reader.read();
      if (done) throw new Error("Connection closed");
      buffer += decoder.decode(value, { stream: true });
      let end;
      while ((end = buffer.indexOf("\n\n")) >= 0) {
        const message = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        const data = message.split("\n").find((l) => l.startsWith("data: "));
        if (data) receive(JSON.parse(data.slice(6)));
      }
    }
  } catch (error) {
    if (error.name === "AbortError") return;
    $("#connection").textContent = "Reconnecting";
    $("#connection-dot").className = "dot";
    setTimeout(connect, 2000);
  }
}
function receive(next) {
  if (sequence !== null && next.seq >= sequence) {
    for (const event of visibleEvents(next.events, sequence, selectedTown)) {
      if (event.kind === "delivery" && !overview && motion && !document.hidden)
        moving.push({ ...event, start: performance.now() });
    }
  } else {
    moving = [];
  }
  moving = moving.slice(-24);
  sequence = next.seq;
  state = next;
  if (!state.towns[selectedTown])
    selectedTown = Object.keys(state.towns)[0] || "";
  render();
}
function chooseHouse(role) {
  overview = false;
  selectedHouse = role;
  selectedTask = "";
  $("#inspector").classList.add("open");
  render();
}
function render() {
  const t = town();
  $("#mode").hidden = !state.demo;
  $("#demo-note").hidden = !state.demo;
  $("#empty").hidden = !!t;
  $("#start-all").disabled = !t;
  $("#pause-all").disabled = !t;
  $("#repo-owner").textContent = t ? t.config.repo.split("/")[0] : "WELCOME TO";
  $("#town-name").textContent = t
    ? t.config.repo.split("/")[1]
    : "Your next little town";
  $("#town-meta").textContent = t
    ? `${t.config.branch || "Reading repository…"} · ${Object.values(t.workers).filter((w) => w.status === "working").length} agents at work · ${Object.values(t.tasks).filter((task) => task.blocked).length} need attention`
    : "Connect a repository to bring its agents together.";
  $("#towns").innerHTML = Object.values(state.towns)
    .map(
      (item) =>
        `<button class="town-link ${item.id === selectedTown ? "selected" : ""}" data-town="${esc(item.id)}"><strong>▧ ${esc(item.config.repo.split("/")[1])}</strong><small>${esc(item.config.repo.split("/")[0])} · ${Object.values(item.workers).filter((w) => w.enabled).length} awake</small></button>`,
    )
    .join("");
  $("#towns")
    .querySelectorAll("button")
    .forEach(
      (button) =>
        (button.onclick = () => {
          selectTown(button.dataset.town);
        }),
    );
  renderOverview();
  showError(t?.error || "");
  renderHouses();
  renderInspection();
  renderJournal();
  renderManagement();
}
function selectTown(id) {
  if (!state?.towns[id]) throw new Error("Unknown town");
  selectedTown = id;
  selectedTask = "";
  moving = [];
  overview = false;
  render();
}
function renderOverview() {
  $("#overview").hidden = !overview;
  $(".world").hidden = overview;
  $(".activity").hidden = overview;
  $("#inspector").hidden = overview;
  $(".town-controls").hidden = overview;
  $("#all-towns").classList.toggle("selected", overview);
  if (!overview) return;
  $("#repo-owner").textContent = "YOUR LOCAL WORLD";
  $("#town-name").textContent = "Every town, together.";
  $("#town-meta").textContent =
    `${Object.keys(state.towns).length} repositories · independent workers, queues, and releases`;
  $("#overview").innerHTML =
    Object.values(state.towns)
      .map((t) => {
        const stats = townSummary(t);
        return `<button class="town-card" data-visit="${esc(t.id)}"><span class="eyebrow">${esc(t.config.repo.split("/")[0])}</span><h2>${esc(t.config.repo.split("/")[1])}</h2><div class="town-card-houses" aria-hidden="true"></div><div class="town-stats"><span><strong>${stats.busy}</strong> working</span><span><strong>${stats.queued}</strong> at the doors</span><span><strong>${stats.blocked + stats.failed}</strong> need attention</span></div><p>${esc(t.error || t.reports.at(-1)?.title || "Repo-bot is taking the first inventory")}</p><small>${esc(stats.release)} · Visit town →</small></button>`;
      })
      .join("") ||
    '<div class="overview-empty"><h2>Your world starts with one repository.</h2><p>Add a town using New town above. Each repository gets its own team of agents.</p></div>';
  $("#overview")
    .querySelectorAll("[data-visit]")
    .forEach((b) => (b.onclick = () => selectTown(b.dataset.visit)));
}
$("#all-towns").onclick = () => {
  overview = true;
  moving = [];
  if (state) render();
};
function renderHouses() {
  const t = town();
  $("#houses").innerHTML = Object.entries(positions)
    .filter(([role]) => indices[role] !== undefined)
    .map(([role, [x, y]]) => {
      const w = t?.workers[role],
        count = t ? queueFor(t, role).length : 0,
        status = role === "hall" ? "Town reports" : w?.status || "paused",
        dot =
          status === "working" || status === "pausing"
            ? "active"
            : status === "blocked" || status === "failed"
              ? "blocked"
              : "waiting";
      return `<button class="house ${selectedHouse === role ? "selected" : ""}" style="left:${x / 11.2}%;top:${y / 6.8}%;" data-house="${role}" aria-label="Visit ${houseNames[role]}" title="${houseNames[role]} · ${esc(status.replaceAll("_", " "))}" aria-keyshortcuts="${houseShortcuts.indexOf(role) + 1}"><span class="house-label"><strong>${houseNames[role].replace(" BOT", '<span class="bot-suffix"> BOT</span>')}${count ? `<span class="count">${count}</span>` : ""}</strong><small><i class="dot ${dot}"></i>${esc(status.replaceAll("_", " "))}</small></span></button>`;
    })
    .join("");
  $("#houses")
    .querySelectorAll("button")
    .forEach((b) => (b.onclick = () => chooseHouse(b.dataset.house)));
}
function renderInspection() {
  const t = town(),
    out = $("#inspection");
  if (!t) {
    out.innerHTML =
      '<h2>Take a look around</h2><p class="muted">Add a repository to establish the first town.</p>';
    return;
  }
  if (selectedTask && t.tasks[selectedTask]) {
    const task = t.tasks[selectedTask];
    out.innerHTML = `<button id="back-house" class="quiet">← ${houseNames[selectedHouse] || "House"}</button><h2>${esc(task.title)}</h2><div class="status-line">${esc(task.stage.replaceAll("_", " "))} ${task.external ? "· external arrival" : ""}</div><div class="task-detail">${safeURL(task.url) ? `<a href="${esc(task.url)}" target="_blank" rel="noopener noreferrer">Open on GitHub ↗</a>` : ""}<p>${esc(task.detail || "Following the next step through town.")}</p>${task.head ? `<p>Revision <code>${esc(task.head.slice(0, 10))}</code> · repair round ${task.cycles}</p>` : ""}${task.audit ? `<h3>${esc(task.audit.verdict.replaceAll("_", " "))}</h3><p>${esc(task.audit.summary)}</p>${task.audit.findings.map((f) => `<p><strong>${esc(f.state)}</strong> ${esc(f.detail)}</p>`).join("")}` : ""}</div>${task.blocked ? '<button id="retry-task" class="primary">Reconcile and retry</button>' : ""}`;
    $("#back-house").onclick = () => {
      selectedTask = "";
      renderInspection();
    };
    if ($("#retry-task"))
      $("#retry-task").onclick = () =>
        command("retry", selectedHouse, selectedTask);
    return;
  }
  if (selectedHouse === "hall") {
    out.innerHTML = `<p class="worker-type">THE TOWN HALL</p><h2>News from repo-bot</h2><p class="muted">Repository changes, busy arrivals, and work that needs attention.</p>${
      t.reports
        .slice()
        .reverse()
        .map(
          (r) =>
            `<article class="report"><time>${new Date(r.at).toLocaleTimeString()}</time><h4>${esc(r.title)}</h4><p>${esc(r.body)}</p></article>`,
        )
        .join("") ||
      '<p class="muted">The first report will arrive after the repository check.</p>'
    }`;
    return;
  }
  const w = t.workers[selectedHouse],
    queue = queueFor(t, selectedHouse);
  if (!w) return;
  out.innerHTML = `<p class="worker-type">${{ bug: "THE GREENHOUSE", feature: "THE STUDY", issue: "THE WORKSHOP", review: "THE OBSERVATORY", release: "THE SHIPPING DEPOT", repo: "THE WATCHTOWER" }[selectedHouse]}</p><h2>${houseNames[selectedHouse]}</h2><p class="muted">${esc(w.task || (selectedHouse === "feature" ? "Finds useful new features by studying this repository" : "Waiting for work"))}</p><div class="status-line"><i class="dot ${w.status === "working" ? "active" : w.status === "failed" ? "blocked" : "waiting"}"></i>${esc(w.status)}${w.next && Date.parse(w.next) > Date.now() ? ` · next check ${new Date(w.next).toLocaleTimeString()}` : ""}</div><div class="inspector-actions"><button class="primary" data-action="start">▶ Start</button><button data-action="pause">Ⅱ Pause</button><button data-action="stop">■ Stop</button></div>${w.error ? `<p class="muted">${esc(w.error)}</p>` : ""}<h3>AT THE DOOR · ${queue.length}</h3>${
    queue
      .slice(0, 40)
      .map(
        (task) =>
          `<button class="task-card" data-task="${esc(task.id)}"><strong>${esc(task.title)}</strong><small>${esc(task.stage.replaceAll("_", " "))}${task.external ? " · external" : ""}${task.blocked ? " · needs attention" : ""}</small></button>`,
      )
      .join("") || '<p class="muted">Nothing waiting at the door.</p>'
  }<h3>WORKBENCH LOG</h3><div class="worker-logs">${
    esc(
      (w.logs || [])
        .slice(-35)
        .map((l) => `${new Date(l.at).toLocaleTimeString()}  ${l.text}`)
        .join("\n"),
    ) || "No activity yet."
  }</div>`;
  out
    .querySelectorAll("[data-action]")
    .forEach(
      (b) => (b.onclick = () => command(b.dataset.action, selectedHouse)),
    );
  out.querySelectorAll("[data-task]").forEach(
    (b) =>
      (b.onclick = () => {
        selectedTask = b.dataset.task;
        renderInspection();
      }),
  );
}
function renderJournal() {
  const events = (state?.events || [])
    .filter((e) => e.town === selectedTown)
    .slice(-30)
    .reverse();
  $("#activity-count").textContent = `· ${events.length}`;
  $("#journal").innerHTML =
    events
      .map(
        (e) =>
          `<li><time>${new Date(e.at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time><span class="event-route">${esc(e.kind === "delivery" ? `${e.from} → ${e.to}` : e.kind)}</span><button data-cargo="${esc(e.cargo || "")}" data-house="${esc(e.to || "hall")}">${esc(e.title)}</button></li>`,
      )
      .join("") ||
    '<li class="muted">The journal will fill as work moves through town.</li>';
  $("#journal")
    .querySelectorAll("button")
    .forEach(
      (b) =>
        (b.onclick = () => {
          selectedHouse =
            indices[b.dataset.house] !== undefined ? b.dataset.house : "hall";
          selectedTask = b.dataset.cargo;
          $("#inspector").classList.add("open");
          renderInspection();
        }),
    );
}
async function command(action, role = "all", task = "") {
  try {
    await api("/api/control", { town: selectedTown, role, action, task });
    showError("");
  } catch (e) {
    showError(e.message);
  }
}
function sprite(image, index, x, y, size) {
  if (!image.complete || !image.naturalWidth) return;
  const crop =
    image === buildings
      ? crops[index]
      : [
          (index % 3) * 512 + actorCrops[index][0],
          Math.floor(index / 3) * 512 + actorCrops[index][1],
          ...actorCrops[index].slice(2),
        ];
  const width = image === buildings ? size : (size * crop[2]) / crop[3];
  ctx.drawImage(image, ...crop, x - width / 2, y - size / 2, width, size);
}
function singleSprite(image, x, y, height) {
  if (!image.complete || !image.naturalWidth) return;
  const width = (height * image.naturalWidth) / image.naturalHeight;
  ctx.drawImage(image, x - width / 2, y - height / 2, width, height);
}
function draw(now) {
  if (overview || document.hidden) {
    requestAnimationFrame(draw);
    return;
  }
  if (!field || fieldTown !== selectedTown) {
    field = landscape(selectedTown);
    fieldTown = selectedTown;
  }
  ctx.drawImage(field, 0, 0);
  const t = town();
  for (const [role, [x, y]] of Object.entries(positions)) {
    if (indices[role] === undefined) continue;
    if (role === selectedHouse) {
      ctx.fillStyle = "#b0e98115";
      ctx.beginPath();
      ctx.ellipse(x, y + 72, 118, 28, 0, 0, Math.PI * 2);
      ctx.fill();
    }
    if (role === "feature") singleSprite(featureStudy, x, y, 235);
    else sprite(buildings, indices[role], x, y, role === "repo" ? 220 : 235);
    const w = t?.workers[role];
    if (w?.status === "working" || w?.status === "pausing") {
      drawWorking(ctx, role, x, y, now, motion, (index) =>
        index === "feature"
          ? singleSprite(featureReader, 0, 0, 58)
          : sprite(actors, index, 0, 0, 52),
      );
    }
  }

  moving = moving.filter((m) => now - m.start < 6500);
  if (motion)
    for (const m of moving) {
      if (m.town !== selectedTown) continue;
      const p = routePosition(
          m.from,
          m.to,
          easeDelivery((now - m.start) / 6500),
        ),
        right = p.direction >= 0,
        truck = m.from === "outside" || m.to === "outside";
      ctx.fillStyle = "#192b2270";
      ctx.beginPath();
      ctx.ellipse(p.x + 6, p.y + 25, truck ? 38 : 23, 7, 0, 0, Math.PI * 2);
      ctx.fill();
      for (let i = 0; i < 3; i++) {
        const dust = (now / 500 + i / 3) % 1;
        ctx.fillStyle = `rgba(219,202,155,${(1 - dust) * 0.3})`;
        ctx.beginPath();
        ctx.ellipse(
          p.x - (right ? 1 : -1) * (20 + dust * 25),
          p.y + 22 - dust * 5,
          2 + dust * 5,
          2 + dust * 2,
          0,
          0,
          Math.PI * 2,
        );
        ctx.fill();
      }
      sprite(
        actors,
        truck ? (right ? 3 : 4) : right ? 0 : 1,
        p.x,
        p.y + Math.sin(now / 100) * 1.3,
        truck ? 57 : 58,
      );
      ctx.fillStyle = "#0b1a10";
      ctx.fillRect(p.x - 34, p.y - 48, 68, 19);
      ctx.fillStyle = "#dbe6cc";
      ctx.font = "11px monospace";
      ctx.textAlign = "center";
      ctx.fillText(
        m.cargo?.replace("issue:", "#").replace("pr:", "PR #").slice(0, 10) ||
          "cargo",
        p.x,
        p.y - 35,
      );
    }
  requestAnimationFrame(draw);
}
$("#start-all").onclick = () => command("start");
$("#pause-all").onclick = () => command("pause");
$("#close-inspector").onclick = () => $("#inspector").classList.remove("open");
for (const id of ["new-town", "empty-add"])
  $("#" + id).onclick = () => {
    $("#add-error").textContent = "";
    $("#add-dialog").showModal();
  };
$("#cancel-add").onclick = () => $("#add-dialog").close();
$("#add-form").onsubmit = async (e) => {
  e.preventDefault();
  const button = e.submitter;
  button.disabled = true;
  try {
    const result = await api("/api/towns", {
      repo: $("#repo-input").value.trim(),
      merge_policy: $("#merge-policy").value,
    });
    selectedTown = result.id;
    overview = false;
    $("#add-dialog").close();
    $("#repo-input").value = "";
  } catch (error) {
    $("#add-error").textContent = error.message;
  } finally {
    button.disabled = false;
  }
};
$("#connect-form").onsubmit = (e) => {
  e.preventDefault();
  token = $("#token-input").value.trim();
  sessionStorage.setItem("brokk-town-token", token);
  $("#connect-dialog").close();
  connect();
};
function motionUI() {
  $("#motion").textContent = motion ? "Motion on" : "Motion off";
  $("#motion").setAttribute("aria-pressed", String(!motion));
}
$("#motion").onclick = () => {
  motion = !motion;
  if (!motion) moving = [];
  motionUI();
};
motionUI();
matchMedia("(prefers-reduced-motion: reduce)").addEventListener(
  "change",
  (e) => {
    motion = !e.matches;
    moving = [];
    motionUI();
  },
);
$("#help").onclick = () => $("#help-dialog").showModal();
$("#close-help").onclick = () => $("#help-dialog").close();
document.addEventListener("keydown", (e) => {
  if (e.target.matches("input,select,textarea") || $("dialog[open]")) return;
  if (e.key === "0") {
    $("#all-towns").click();
    return;
  }
  if (e.key === "?") $("#help-dialog").showModal();
  if (e.key === "Escape") $("#inspector").classList.remove("open");
  if (/^[1-7]$/.test(e.key)) chooseHouse(houseShortcuts[Number(e.key) - 1]);
});
canvas.onclick = (e) => {
  const r = canvas.getBoundingClientRect(),
    x = ((e.clientX - r.left) * 1120) / r.width,
    y = ((e.clientY - r.top) * 680) / r.height;
  for (const m of moving) {
    const p = routePosition(
      m.from,
      m.to,
      easeDelivery((performance.now() - m.start) / 6500),
    );
    if (Math.hypot(x - p.x, y - p.y) < 50) {
      selectedTask = m.cargo;
      selectedHouse = indices[m.to] !== undefined ? m.to : "hall";
      $("#inspector").classList.add("open");
      renderInspection();
      return;
    }
  }
};
setInterval(() => {
  $("#clock").textContent = new Date().toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}, 1000);
requestAnimationFrame(draw);
connect();

// Read and navigation tools mirror the visible multi-town controls. Worker
// mutation remains an explicit action through the operator controls.
import { registerTownTools } from "./tools.js";
registerTownTools(document.modelContext, () => state, selectTown, chooseHouse);
