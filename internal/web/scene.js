import { attentionStatuses, projectTask, settled } from "./town.js";
import { robotCop, slopBlob, reformedBlob, cruiser, bus, inCustody } from "./animation.js";

// The Precinct view is a cutaway of the precinct house with the courthouse
// next door. Every open case is a slop blob standing in the room of the unit
// that holds it, and when the snapshot moves a case its blob walks there:
// along a floor, up the elevator, over the skybridge or across the sidewalk to
// court. Slop the judges throw out is cuffed and locked in the Slop Tank; slop
// the Caseworker rehabilitates and Forensics clears comes out reformed and
// leaves the precinct on the release bus.
//
// Everything is drawn from the committed snapshot. The scene never invents a
// case and never sends a command; a click opens a file.

export const width = 1200;
export const height = 760;

// Feet lines of the three floors, and the fixed points every route uses.
const F3 = 322,
  F2 = 470,
  F1 = 632,
  CURB = 690,
  ELEVATOR = 416,
  COURT_STAIRS = 1158;
const FRONT_DOOR = { x: 34, y: F1 },
  EXIT_DOOR = { x: 794, y: F1 },
  JUDGE_DOOR = { x: 854, y: F2 },
  MAGISTRATE_DOOR = { x: 854, y: F1 },
  BUS_STOP = { x: 760, y: CURB };

const row = (x, step, n) => Array.from({ length: n }, (_, i) => x + i * step);

// Each room: its wall sign, the box it occupies, the floor its suspects stand
// on, the spots they stand at, and the units staffing it.
export const rooms = {
  squadroom: { sign: "SQUAD ROOM", roles: ["bug", "feature"], box: [42, 182, 358, 140], floor: F3, slots: row(70, 34, 4) },
  forensics: { sign: "EVIDENCE LAB", roles: ["review"], box: [432, 182, 356, 140], floor: F3, slots: row(462, 40, 6) },
  rehab: { sign: "REHAB", roles: ["issue"], box: [42, 330, 358, 140], floor: F2, slots: row(66, 40, 6) },
  breakroom: { sign: "BREAK ROOM", roles: [], box: [432, 330, 356, 140], floor: F2, slots: [] },
  frontdesk: { sign: "BOOKING", roles: ["repo"], box: [42, 478, 218, 154], floor: F1, slots: row(70, 30, 3) },
  holding: { sign: "SLOP TANK", roles: [], box: [260, 478, 140, 154], floor: F1, slots: row(280, 30, 4) },
  release: { sign: "RELEASE", roles: ["release"], box: [432, 478, 356, 154], floor: F1, slots: row(540, 42, 6) },
  judge: { sign: "PROBATION COURT", roles: ["hall"], box: [854, 310, 306, 160], floor: F2, slots: row(884, 32, 5) },
  magistrate: { sign: "ARRAIGNMENT", roles: ["simplifier"], box: [854, 478, 306, 154], floor: F1, slots: row(884, 32, 5) },
};

const homeOf = Object.fromEntries(Object.entries(rooms).flatMap(([id, room]) => room.roles.map((role) => [role, id])));

// Where each unit's officer stands on duty: the top-left of its 64×62 robot.
// In quiet hours the officer is on the break room couch instead.
const duty = {
  repo: [168, 538],
  bug: [206, 258],
  feature: [292, 258],
  review: [700, 258],
  issue: [310, 406],
  release: [446, 568],
  hall: [1070, 342],
  simplifier: [1070, 510],
};
const couch = [[566, 404], [616, 404], [666, 404], [716, 404], [516, 404], [466, 404], [600, 380], [650, 380]];

export function posts(lights = {}) {
  const at = {};
  let seat = 0;
  for (const [role, spot] of Object.entries(duty)) at[role] = lights[role] === "quiet" && seat < couch.length ? couch[seat++] : spot;
  return at;
}

// tagSpot is where an officer's tag hangs: over its head, except that the two
// detectives share a room, so the Feature Detective's tag hangs a row higher.
export function tagSpot(role, [x, y]) {
  return [x + 32, y - (role === "feature" ? 24 : 2)];
}

// overflowSpot is where a full room counts the rest of its cases.
export function overflowSpot(room) {
  const { slots, floor } = rooms[room];
  return [slots[slots.length - 1] + 38, floor - 6];
}

// roomFor says which room a case stands in, or null once it has left the
// precinct (or never entered it).
export function roomFor(task) {
  if (!task) return null;
  if (inCustody(task)) return "holding";
  if (settled(task)) return null;
  return homeOf[task.house] || null;
}

// lookFor is how a case appears: slop, stuck slop, cuffed slop, or a reformed
// citizen once its work has merged.
export function lookFor(task, status) {
  if (inCustody(task)) return "cuffed";
  if (task?.house === "release") return "reformed";
  if (attentionStatuses.includes(status)) return "stuck";
  return "slop";
}

const urgency = { blocked: 0, uncertain_write: 0, inconclusive: 0, failed: 0, working: 1 };

// placeCases stands every visible case in its room. A room shows as many
// suspects as it has spots; the rest are counted. Stuck and working cases
// stand at the front, then the newest arrivals, so a case that walks into a
// full room is seen walking in.
export function placeCases(town) {
  const byRoom = new Map();
  for (const task of Object.values(town?.tasks || {})) {
    const room = roomFor(task);
    if (!room) continue;
    if (!byRoom.has(room)) byRoom.set(room, []);
    byRoom.get(room).push(projectTask(town, task));
  }
  const placed = [],
    overflow = {},
    counts = {};
  for (const [room, cases] of byRoom) {
    const newest = (a, b) => (Date.parse(b.updated || "") || 0) - (Date.parse(a.updated || "") || 0) || String(b.id).localeCompare(String(a.id));
    cases.sort((a, b) => (room === "holding" ? 0 : (urgency[a.status] ?? 2) - (urgency[b.status] ?? 2)) || newest(a, b));
    const { slots, floor } = rooms[room];
    counts[room] = cases.length;
    cases.slice(0, slots.length).forEach((task, i) =>
      placed.push({ id: task.id, task, room, x: slots[i], y: floor, look: lookFor(task, task.status) }),
    );
    if (cases.length > slots.length) overflow[room] = cases.length - slots.length;
  }
  return { placed, overflow, counts };
}

// The way from a spot in a room to the elevator on that room's floor, and
// back. The elevator joins every floor of the precinct; the Probation Judge
// is over the skybridge, the Magistrate across the sidewalk.
function toLift(room, from) {
  if (room === "judge") return [JUDGE_DOOR, { x: ELEVATOR, y: F2 }];
  if (room === "magistrate") return [MAGISTRATE_DOOR, EXIT_DOOR, { x: ELEVATOR, y: F1 }];
  if (room === "street") return [{ x: FRONT_DOOR.x, y: CURB }, FRONT_DOOR, { x: ELEVATOR, y: F1 }];
  return [{ x: ELEVATOR, y: from.y }];
}
function fromLift(room, to) {
  if (room === "judge") return [{ x: ELEVATOR, y: F2 }, JUDGE_DOOR];
  if (room === "magistrate") return [{ x: ELEVATOR, y: F1 }, EXIT_DOOR, MAGISTRATE_DOOR];
  if (room === "bus" || room === "street") return [{ x: ELEVATOR, y: F1 }, EXIT_DOOR, { x: EXIT_DOOR.x, y: CURB }];
  return [{ x: ELEVATOR, y: to.y }];
}
const courtRoom = (room) => room === "judge" || room === "magistrate";
const precinctRoom = (room) => Object.hasOwn(rooms, room) && !courtRoom(room);

// route is the walk between two spots. Rooms on the same floor of the
// precinct are a straight walk; the two courtrooms share a staircase; every
// other trip goes by way of the elevator.
export function route(fromRoom, from, toRoom, to) {
  const points = [from];
  const go = (p) => {
    const last = points[points.length - 1];
    if (p && (p.x !== last.x || p.y !== last.y)) points.push(p);
  };
  if (fromRoom === toRoom || (precinctRoom(fromRoom) && precinctRoom(toRoom) && from.y === to?.y)) {
    go(to);
    return points;
  }
  if (courtRoom(fromRoom) && courtRoom(toRoom)) {
    go({ x: COURT_STAIRS, y: from.y });
    go({ x: COURT_STAIRS, y: to.y });
    go(to);
    return points;
  }
  toLift(fromRoom, from).forEach(go);
  fromLift(toRoom, to || from).forEach(go);
  go(to);
  return points;
}

// Why a case left the scene, read from where the snapshot put it.
function goneReason(task, room) {
  if (!task) return "gone";
  if (room) return "hidden";
  if (task.stage === "merged") return "rehabilitated";
  if (task.stage === "shipped") return "shipped";
  if (task.stage === "implemented") return "continued";
  if (task.stage === "complete") return "done";
  if (task.stage === "closed" || task.stage === "closing") return "closed";
  return "gone";
}

// changes compares the placements of two committed snapshots of one precinct:
// which suspects moved (to another room, or along the line in their own),
// which arrived, and which left and why.
export function changes(before, after, town) {
  const moved = [],
    arrived = [],
    gone = [];
  for (const [id, next] of after) {
    const prev = before.get(id);
    if (!prev) arrived.push({ id, ...next });
    else if (prev.room !== next.room || prev.x !== next.x) moved.push({ id, from: prev, to: next });
  }
  for (const [id, prev] of before) {
    if (after.has(id)) continue;
    const task = town?.tasks?.[id];
    gone.push({ id, from: prev, reason: goneReason(task, roomFor(task)) });
  }
  return { moved, arrived, gone };
}

// originOf finds where an arriving case came from: the latest transfer that
// carried it names the sender. Civilian reports come in off the street.
export function originOf(id, events = []) {
  const event = [...events].reverse().find((e) => e.kind === "delivery" && e.cargo === id);
  if (!event) return null;
  if (event.from === "outside") return "street";
  return homeOf[event.from] || null;
}

// The stamp a room gets when a case walks in.
export function markFor(fromRoom, toRoom) {
  if (toRoom === "holding") return { text: "LOCKED UP", tone: "red" };
  if (toRoom === "magistrate") return { text: "ARRAIGNED", tone: "yellow" };
  if (toRoom === "judge") return { text: "ON THE DOCKET", tone: "yellow" };
  if (courtRoom(fromRoom) && toRoom === "rehab") return { text: "PROBATION", tone: "green" };
  if (courtRoom(fromRoom)) return { text: "SET FREE", tone: "green" };
  if (fromRoom === "forensics" && toRoom === "rehab") return { text: "SENT BACK", tone: "orange" };
  if (toRoom === "forensics") return { text: "EVIDENCE IN", tone: "blue" };
  if (toRoom === "rehab") return { text: "ASSIGNED", tone: "blue" };
  if (toRoom === "frontdesk" || fromRoom === "street") return { text: "BOOKED", tone: "yellow" };
  return { text: "TRANSFERRED", tone: "blue" };
}

// ───────────── drawing ─────────────

const pct = (value, of) => `${((value / of) * 100).toFixed(3)}%`;
export const at = (x, y) => `left:${pct(x, width)};top:${pct(y, height)}`;
const xml = (value) =>
  String(value ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

function placeSvg(svg, x, y, w, h) {
  return svg.replace("<svg ", `<svg x="${x}" y="${y}" width="${w}" height="${h}" `);
}

function lamp(x, y, light) {
  return `<g class="lamp lamp-${light}"><path d="M${x - 9} ${y} h18 l-4 6 h-10 z" fill="#3a4558"/><path class="glow" d="M${x - 6} ${y + 6} L${x - 50} ${y + 96} H${x + 50} L${x + 6} ${y + 6} Z"/></g>`;
}

function wallSign(room) {
  const [x, y] = room.box;
  const w = room.sign.length * 7.2 + 16;
  return `<g class="wall-sign"><rect x="${x + 10}" y="${y + 8}" width="${w}" height="18" rx="2"/><text x="${x + 10 + w / 2}" y="${y + 20.5}" text-anchor="middle">${room.sign}</text></g>`;
}

function roomShell(id) {
  const [x, y, w, h] = rooms[id].box;
  return `<rect class="room room-${id}" x="${x}" y="${y}" width="${w}" height="${h}"/><rect class="wainscot" x="${x}" y="${y + h - 26}" width="${w}" height="26"/>${wallSign(rooms[id])}`;
}

// sceneArt draws the building: sky, street, the three floors, the elevator,
// the courthouse and every unit's officer in the state its light reports.
export function sceneArt({ repo = "", lights = {} } = {}) {
  const light = (role) => lights[role] || "waiting";
  const brightest = (...roles) => ["blocked", "active", "waiting", "quiet"].find((l) => roles.some((r) => light(r) === l)) || "waiting";
  const where = posts(lights);
  const cop = (role) => placeSvg(robotCop(role, light(role)), where[role][0], where[role][1], 64, 62);
  const stars = [[60, 40], [180, 80], [260, 24], [420, 60], [610, 30], [700, 86], [860, 50], [960, 110], [1150, 30], [1010, 20], [320, 120], [760, 140]]
    .map(([x, y], i) => `<circle class="star" cx="${x}" cy="${y}" r="${i % 3 ? 1.2 : 1.8}" style="animation-delay:${(i * 0.37).toFixed(2)}s"/>`)
    .join("");
  const benches = (y) => `<rect x="870" y="${y - 20}" width="164" height="7" fill="#6b4426"/><rect x="876" y="${y - 13}" width="5" height="13" fill="#4a2f1c"/><rect x="1022" y="${y - 13}" width="5" height="13" fill="#4a2f1c"/>`;
  // The bench's nameplate lights IN SESSION while its judge is on a case.
  const bench = (y, h, role) => `<rect x="1046" y="${y}" width="108" height="${h}" fill="#4a2f1c" stroke="#6b4426"/><rect x="1040" y="${y - 6}" width="120" height="8" fill="#6b4426"/><g class="court-lamp${light(role) === "active" ? " in-session" : ""}"><rect x="1068" y="${y + 8}" width="64" height="14" rx="2" fill="#0b1017"/><text x="1100" y="${y + 18.5}" text-anchor="middle">IN SESSION</text></g><path d="M1100 ${y + 32} l2.5 5 5.5.8-4 3.9 1 5.5-5-2.6-5 2.6 1-5.5-4-3.9 5.5-.8z" fill="#ffd23f"/>`;
  return `<svg class="scene-svg" viewBox="0 0 ${width} ${height}" preserveAspectRatio="xMidYMid meet" aria-hidden="true" focusable="false">
<defs><linearGradient id="sky" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#070b14"/><stop offset="1" stop-color="#13203a"/></linearGradient></defs>
<rect width="${width}" height="${height}" fill="url(#sky)"/>${stars}
<g class="moon"><circle cx="1090" cy="74" r="30" fill="#f4f1de"/><circle cx="1080" cy="66" r="6" fill="#dcd8c0"/><circle cx="1100" cy="86" r="4" fill="#dcd8c0"/></g>
<rect x="0" y="632" width="${width}" height="14" fill="#2a3242"/><rect x="0" y="646" width="${width}" height="92" fill="#14181f"/><path d="M0 692 H${width}" stroke="#ffd23f99" stroke-width="3" stroke-dasharray="26 22"/><rect x="0" y="738" width="${width}" height="22" fill="#0c1016"/>
<g class="bus-stop"><line x1="822" y1="590" x2="822" y2="632" stroke="#7b889d" stroke-width="3"/><rect x="808" y="578" width="28" height="16" rx="2" fill="#3b8cff"/><text x="822" y="590" text-anchor="middle">BUS</text></g>
<rect x="30" y="170" width="770" height="466" fill="#1b2433" stroke="#2b3749" stroke-width="2"/><rect x="24" y="160" width="782" height="14" fill="#2b3749"/>
<g class="antenna"><line x1="92" y1="104" x2="92" y2="160" stroke="#7b889d" stroke-width="3"/><circle class="beacon-light" cx="92" cy="100" r="5"/></g>
<line x1="306" y1="150" x2="306" y2="160" stroke="#7b889d" stroke-width="4"/><line x1="524" y1="150" x2="524" y2="160" stroke="#7b889d" stroke-width="4"/>
<g class="roof-sign"><rect x="250" y="104" width="330" height="48" rx="4"/><text class="big" x="415" y="131" text-anchor="middle">SLOPCOP SQUAD</text><text class="small" x="415" y="145" text-anchor="middle">PRECINCT · ${xml(repo).slice(0, 34)}</text></g>
<g class="siren"><rect x="704" y="150" width="34" height="10" rx="2" fill="#2b3749"/><path class="siren-red" d="M708 150 a7 7 0 0 1 13 0 z"/><path class="siren-blue" d="M721 150 a7 7 0 0 1 13 0 z"/></g>
${["squadroom", "forensics", "rehab", "breakroom", "frontdesk", "holding", "release"].map(roomShell).join("")}
<rect x="30" y="322" width="770" height="8" fill="#2b3749"/><rect x="30" y="470" width="770" height="8" fill="#2b3749"/>
<g class="elevator"><rect x="400" y="182" width="32" height="450" fill="#0b1017"/><line x1="405" y1="182" x2="405" y2="632" stroke="#2b3749" stroke-width="2"/><line x1="427" y1="182" x2="427" y2="632" stroke="#2b3749" stroke-width="2"/><text x="416" y="200">3</text><text x="416" y="348">2</text><text x="416" y="496">1</text></g>
${lamp(220, 182, brightest("bug", "feature"))}${lamp(610, 182, light("review"))}${lamp(220, 330, light("issue"))}${lamp(610, 330, "waiting")}${lamp(150, 478, light("repo"))}${lamp(610, 478, light("release"))}
<g class="furniture">
<rect x="60" y="200" width="122" height="64" rx="2" fill="#4a3420" stroke="#6b4426"/><path d="M78 216 L130 210 L112 248 L160 240" stroke="#ff4757" stroke-width="1.5" fill="none"/><circle cx="78" cy="216" r="3" fill="#ff4757"/><circle cx="130" cy="210" r="3" fill="#ff4757"/><circle cx="112" cy="248" r="3" fill="#ff4757"/><circle cx="160" cy="240" r="3" fill="#ff4757"/><rect x="88" y="222" width="18" height="14" fill="#e9eef6"/><rect x="138" y="220" width="16" height="12" fill="#e9eef6"/><text class="board-text" x="121" y="260" text-anchor="middle">PERSONS OF INTEREST</text>
<rect x="196" y="300" width="160" height="7" fill="#3a4558"/><rect x="202" y="307" width="5" height="15" fill="#2b3749"/><rect x="345" y="307" width="5" height="15" fill="#2b3749"/>
<rect x="540" y="288" width="140" height="8" fill="#3a4558"/><rect x="546" y="296" width="5" height="26" fill="#2b3749"/><rect x="669" y="296" width="5" height="26" fill="#2b3749"/><path d="M566 288 v-14 h6 l6 -10 l4 3 l-5 9 v12" fill="#9aa6b8"/><path d="M604 288 l6 -16 h6 l6 16 z" fill="#34d39988" stroke="#e9eef6"/><path d="M632 288 v-18 h8 v18 z" fill="#a78bfa88" stroke="#e9eef6"/>
<rect x="150" y="436" width="128" height="8" fill="#5a3b22"/><rect x="156" y="444" width="5" height="26" fill="#3d2816"/><rect x="267" y="444" width="5" height="26" fill="#3d2816"/><rect x="190" y="362" width="88" height="40" rx="2" fill="#16202e" stroke="#2b3749"/><text class="board-text" x="234" y="380" text-anchor="middle">REHAB PLAN</text><path d="M200 388 h56 M200 395 h40" stroke="#7b889d" stroke-width="2"/>
<rect x="452" y="400" width="30" height="70" rx="2" fill="#2b3749"/><rect x="458" y="410" width="18" height="12" fill="#0b1017"/><circle cx="467" cy="440" r="4" fill="#ff4757"/><rect x="510" y="450" width="40" height="6" fill="#5a3b22"/><circle cx="520" cy="446" r="5" fill="#e8a25c"/><circle cx="520" cy="446" r="2" fill="#13213a"/><circle cx="534" cy="446" r="5" fill="#e8a25c"/><circle cx="534" cy="446" r="2" fill="#13213a"/><path d="M500 470 v-22 q0 -6 6 -6 h270 q6 0 6 6 v22 z" fill="#2a3a5a" opacity=".55"/><rect x="560" y="432" width="210" height="16" rx="6" fill="#3a4a6c"/><text class="board-text" x="740" y="352" text-anchor="middle">☕ DONUTS</text>
<rect x="30" y="556" width="14" height="76" fill="#3d2816"/><rect x="33" y="566" width="8" height="18" fill="#3b8cff55"/><text class="door-sign" x="62" y="552">IN</text>
<rect x="776" y="556" width="14" height="76" fill="#1f3d2b"/><rect x="779" y="566" width="8" height="18" fill="#34d39955"/><rect x="744" y="532" width="40" height="16" rx="2" fill="#0d3b25"/><text class="exit-sign" x="764" y="544" text-anchor="middle">EXIT</text>
<rect x="268" y="598" width="54" height="8" rx="2" fill="#5c6a80"/><rect x="340" y="598" width="54" height="8" rx="2" fill="#5c6a80"/>
</g>
${["bug", "feature", "review", "issue", "repo", "release"].map(cop).join("")}
<g class="furniture"><rect x="146" y="578" width="108" height="6" fill="#3a4558"/><rect x="150" y="584" width="100" height="48" fill="#2b3749"/><text class="desk-text" x="200" y="612" text-anchor="middle">BOOKING</text><path d="M232 570 h14 v8 h-14 z M234 566 h10" stroke="#9aa6b8" fill="#16202e"/></g>
<rect x="800" y="430" width="54" height="40" fill="#1b2433" stroke="#2b3749"/><rect x="808" y="438" width="14" height="14" fill="#3b8cff33"/><rect x="832" y="438" width="14" height="14" fill="#3b8cff33"/>
<rect x="844" y="300" width="326" height="336" fill="#1b2433" stroke="#2b3749" stroke-width="2"/><path d="M836 302 L1007 232 L1178 302 Z" fill="#222c3d" stroke="#3a4558" stroke-width="2"/><text class="court-title" x="1007" y="288" text-anchor="middle">COURTHOUSE</text>
${roomShell("judge")}${roomShell("magistrate")}<rect x="844" y="470" width="326" height="8" fill="#3a2a1c"/>
<rect x="1152" y="310" width="10" height="322" fill="#0b1017"/><path d="M1153 330 h8 M1153 360 h8 M1153 390 h8 M1153 420 h8 M1153 450 h8 M1153 500 h8 M1153 530 h8 M1153 560 h8 M1153 590 h8" stroke="#2b3749" stroke-width="2"/>
<rect x="844" y="556" width="12" height="76" fill="#3d2816"/>
${light("hall") === "quiet" ? "" : cop("hall")}${light("simplifier") === "quiet" ? "" : cop("simplifier")}
<g class="furniture">${bench(392, 78, "hall")}${benches(F2)}${bench(560, 72, "simplifier")}${benches(F1)}</g>
${light("hall") === "quiet" ? cop("hall") : ""}${light("simplifier") === "quiet" ? cop("simplifier") : ""}
</svg>`;
}

// suspectArt draws one case as it looks now.
export function suspectArt(look) {
  if (look === "reformed") return reformedBlob();
  if (look === "cuffed") return slopBlob({ cuffed: true });
  return slopBlob() + (look === "stuck" ? '<span class="alert" aria-hidden="true">!</span>' : "");
}

// The holding cell's bars sit in front of whoever is locked up.
export function barsBox() {
  const [x, y, w, h] = rooms.holding.box;
  return `left:${pct(x, width)};top:${pct(y + 26, height)};width:${pct(w, width)};height:${pct(h - 26, height)}`;
}

// ───────────── choreography ─────────────

const walkSpeed = 0.17; // viewBox units per millisecond on a floor
const liftSpeed = 0.24; // in the elevator shaft and on the court stairs
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// A scene needs the Web Animations API on a real page; anywhere without it (a
// test shim, an old browser) it quietly does nothing.
export function canStage(ctx) {
  return !!(ctx?.fx?.ownerDocument && typeof ctx.fx.animate === "function");
}

export function frames(points) {
  const lengths = points.slice(1).map((p, i) => {
    const d = Math.hypot(p.x - points[i].x, p.y - points[i].y);
    return d / (p.x === points[i].x ? liftSpeed : walkSpeed);
  });
  const total = lengths.reduce((a, b) => a + b, 0) || 1;
  let elapsed = 0;
  return {
    duration: total,
    keyframes: points.map((p, i) => {
      if (i > 0) elapsed += lengths[i - 1];
      return { left: pct(p.x, width), top: pct(p.y, height), offset: Math.min(1, elapsed / total) };
    }),
  };
}

async function run(element, keyframes, options) {
  try {
    await element.animate(keyframes, options).finished;
  } catch {
    /* A cancelled animation simply ends the scene. */
  }
}

// walk moves a suspect along a route, at walking pace and lift pace.
function walk(element, points, delay = 0) {
  const { keyframes, duration } = frames(points);
  return run(element, keyframes, { duration, delay, easing: "linear", fill: "backwards" }).then(() => duration);
}

function actor(ctx, className, html, x, y) {
  const element = ctx.fx.ownerDocument.createElement("div");
  element.className = className;
  element.innerHTML = html;
  element.setAttribute("style", at(x, y));
  ctx.fx.append(element);
  return element;
}

function stamp(ctx, roomId, mark) {
  if (!mark || !rooms[roomId]) return;
  const [x, y, w] = rooms[roomId].box;
  const element = actor(ctx, `stamp tone-${mark.tone}`, mark.text, x + w / 2, y + 46);
  run(element, [
    { transform: "translate(-50%, -50%) scale(2.4) rotate(-14deg)", opacity: 0 },
    { transform: "translate(-50%, -50%) scale(0.92) rotate(-8deg)", opacity: 1, offset: 0.18 },
    { transform: "translate(-50%, -50%) scale(1) rotate(-8deg)", opacity: 1, offset: 0.26 },
    { transform: "translate(-50%, -50%) scale(1) rotate(-8deg)", opacity: 1, offset: 0.8 },
    { transform: "translate(-50%, -50%) scale(1) rotate(-8deg)", opacity: 0 },
  ], { duration: 1900, easing: "ease-out", fill: "forwards" }).then(() => element.remove());
}

function spot(room, index = 0) {
  const r = rooms[room];
  const slots = r.slots.length ? r.slots : [r.box[0] + r.box[2] / 2];
  return { x: slots[Math.min(index, slots.length - 1)], y: r.floor };
}

// drive runs a vehicle along the curb from one x to another.
function drive(vehicle, fromX, toX, ms) {
  vehicle.classList.toggle("left", toX < fromX);
  return run(vehicle, [{ left: pct(fromX, width) }, { left: pct(toX, width) }], { duration: ms, easing: "cubic-bezier(.45,0,.25,1)", fill: "forwards" });
}

// play animates what changed between two snapshots. Suspects already stand in
// their final places in the page; each one that moved walks there from where
// it was, and cases that left get a stand-in to play their exit.
export async function play(ctx, { moved, arrived, gone }, events = []) {
  if (!canStage(ctx)) return;
  for (const { id, from, to } of moved) {
    const element = ctx.find(id);
    if (!element) continue;
    const points = route(from.room, from, to.room, to);
    if (from.room === to.room) {
      // Shuffling along the line as the room reorders: no arrest, no stamp.
      walk(element, points);
    } else if (to.room === "holding") {
      // Thrown out: cuffed on the spot, then marched to the tank.
      element.classList.add("busted");
      const escort = actor(ctx, "escort", robotCop("repo", "active"), from.x - 30, from.y);
      walk(escort, route(from.room, { x: from.x - 30, y: from.y }, to.room, { x: to.x - 30, y: to.y }), 800).then(() => escort.remove());
      walk(element, points, 800).then(() => {
        element.classList.remove("busted");
        stamp(ctx, to.room, markFor(from.room, to.room));
      });
    } else {
      walk(element, points).then(() => stamp(ctx, to.room, markFor(from.room, to.room)));
    }
  }
  // Cleared by Forensics: the slop walks down to Release, where it comes out
  // reformed and waits for the bus.
  const reforming = gone.filter((g) => g.reason === "rehabilitated");
  let reformTime = 0;
  for (const [i, { from }] of reforming.entries()) {
    const path = route(from.room, from, "release", spot("release", 5));
    reformTime = Math.max(reformTime, frames(path).duration);
    const ghost = actor(ctx, "suspect ghost", suspectArt("slop"), from.x, from.y);
    walk(ghost, path, i * 300).then(async () => {
      ghost.innerHTML = suspectArt("reformed");
      ghost.classList.add("reformed-now");
      stamp(ctx, "release", { text: "REHABILITATED", tone: "green" });
      await wait(1500);
      ghost.remove();
    });
  }
  for (const placed of arrived) {
    const element = ctx.find(placed.id);
    if (!element) continue;
    const origin = originOf(placed.id, events);
    if (placed.room === "release" && reforming.length) {
      run(element, [{ opacity: 0 }, { opacity: 0, offset: 0.92 }, { opacity: 1 }], { duration: reformTime + 1500 });
    } else if (origin === "street") {
      // A squad car drops the slop at the front door.
      const car = actor(ctx, "vehicle cruiser", `<span class="siren-call"><span>WEE</span><span>OO</span></span>${cruiser()}`, -60, CURB + 6);
      run(element, [{ opacity: 0 }, { opacity: 0 }], { duration: 1100 });
      await drive(car, -60, 70, 1100);
      walk(element, route("street", { x: 70, y: CURB }, placed.room, placed)).then(() => stamp(ctx, placed.room, markFor("street", placed.room)));
      setTimeout(() => drive(car, 70, width + 90, 1500).then(() => car.remove()), 400);
    } else if (origin && rooms[origin]) {
      walk(element, route(origin, spot(origin), placed.room, placed)).then(() => stamp(ctx, placed.room, markFor(origin, placed.room)));
    } else {
      run(element, [{ transform: "translate(-50%, -100%) scale(0.1)", opacity: 0 }, { transform: "translate(-50%, -100%) scale(1.15)", opacity: 1, offset: 0.6 }, { transform: "translate(-50%, -100%) scale(1)", opacity: 1 }], { duration: 650, easing: "ease-out" });
    }
  }
  // Done without a merge: reformed all the same, out the door and away.
  for (const { from } of gone.filter((g) => g.reason === "done")) {
    const ghost = actor(ctx, "suspect ghost reformed-now", suspectArt("reformed"), from.x, from.y);
    stamp(ctx, from.room, { text: "CASE CLOSED", tone: "green" });
    walk(ghost, [...route(from.room, from, "street"), { x: width + 60, y: CURB }]).then(() => ghost.remove());
  }
  // Closed without a reform: the slop slinks out the door.
  for (const { from } of gone.filter((g) => g.reason === "closed")) {
    const ghost = actor(ctx, "suspect ghost", suspectArt("slop"), from.x, from.y);
    stamp(ctx, from.room, { text: "CASE CLOSED", tone: "blue" });
    walk(ghost, [...route(from.room, from, "street"), { x: width + 60, y: CURB }]).then(() => ghost.remove());
  }
  for (const { from } of gone.filter((g) => g.reason === "continued" || g.reason === "gone")) {
    const ghost = actor(ctx, "suspect ghost", suspectArt("slop"), from.x, from.y);
    run(ghost, [{ opacity: 1 }, { opacity: 0, transform: "translate(-50%, -100%) scale(0.6)" }], { duration: 700 }).then(() => ghost.remove());
  }
  // Released: the bus pulls up, the reformed board, and they leave for good.
  const shipped = gone.filter((g) => g.reason === "shipped");
  if (shipped.length) {
    const coach = actor(ctx, "vehicle coach", bus(), -260, CURB + 8);
    await drive(coach, -260, BUS_STOP.x - 20, 1700);
    stamp(ctx, "release", { text: "SHIPPED", tone: "green" });
    await Promise.all(
      shipped.map(({ from }, i) => {
        const ghost = actor(ctx, "suspect ghost", suspectArt("reformed"), from.x, from.y);
        return walk(ghost, [...route("release", from, "bus"), BUS_STOP], i * 260).then(() => ghost.remove());
      }),
    );
    await wait(300);
    await drive(coach, BUS_STOP.x - 20, width + 300, 2000);
    coach.remove();
  }
}
