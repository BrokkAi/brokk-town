import { callsign, cargoLabel, caseNumber, laneOf } from "./precinct.js";

// The precinct's cartoon layer: robot cops on the roster, squad cars that run
// cases between lanes, rubber stamps, and the slop itself, which gets cuffed
// and booked into the tank. Everything here decorates something the snapshot
// already committed: a transfer the service recorded, a case it dismissed. It
// never invents state and never sends a command; a click only opens a file.
//
// The art is inline SVG and CSS; there are no image assets. Animation runs
// through CSS classes and the Web Animations API, so the Motion toggle and
// prefers-reduced-motion stop all of it.

// ───────────── the officers ─────────────

// Each unit's robot carries the tool of its trade.
const props = {
  repo: {
    on: `<g class="prop prop-sweep"><rect x="42" y="25" width="9" height="5" rx="1.5" fill="#5c6a80"/><path d="M51 24 L60 18 L60 37 L51 31 Z" class="beam"/></g>`,
    off: `<g class="prop prop-bob"><circle cx="48" cy="28" r="5.2" fill="#e8a25c"/><circle cx="48" cy="28" r="5.2" fill="none" stroke="#ff8fb3" stroke-width="2.4" stroke-dasharray="3 2"/><circle cx="48" cy="28" r="1.8" fill="#10161f"/></g>`,
  },
  bug: `<g class="prop prop-sweep"><line x1="44" y1="32" x2="40" y2="37" stroke="#b4bfd0" stroke-width="2.4" stroke-linecap="round"/><circle cx="48" cy="26" r="5.5" fill="#3b8cff33" stroke="#e9eef6" stroke-width="2"/><circle cx="46.5" cy="24.5" r="1.4" fill="#ffffffaa"/></g>`,
  feature: `<g class="prop prop-tilt"><rect x="41" y="22" width="7" height="8" rx="2" fill="#2b3749"/><rect x="49" y="22" width="7" height="8" rx="2" fill="#2b3749"/><rect x="47" y="24" width="3" height="3" fill="#2b3749"/><circle cx="44.5" cy="29" r="2.2" fill="#7dd3fc"/><circle cx="52.5" cy="29" r="2.2" fill="#7dd3fc"/></g>`,
  simplifier: `<g class="prop prop-mop"><line x1="45" y1="18" x2="49" y2="44" stroke="#b08a5a" stroke-width="2.2" stroke-linecap="round"/><path d="M44 43 h10 l-1 6 h-8 z" fill="#d6dde8"/><path d="M45 49 v3 M47.5 49 v4 M50 49 v3 M52.5 49 v4" stroke="#d6dde8" stroke-width="1.4"/></g><ellipse class="puddle" cx="49" cy="53" rx="8" ry="2" fill="#84cc16"/>`,
  hall: `<g class="prop prop-gavel"><line x1="41" y1="32" x2="50" y2="22" stroke="#a16b3f" stroke-width="2.6" stroke-linecap="round"/><rect x="46" y="15" width="10" height="7" rx="1.5" fill="#7c4a24" transform="rotate(45 51 18.5)"/></g><rect x="44" y="36" width="12" height="4" rx="1" fill="#7c4a24"/>`,
  issue: `<g class="prop prop-wrench"><line x1="42" y1="34" x2="51" y2="23" stroke="#a16b3f" stroke-width="2.6" stroke-linecap="round"/><rect x="46" y="17.5" width="11" height="5.5" rx="1.2" fill="#c9d3e0" transform="rotate(40 51.5 20.2)"/><rect x="46" y="17.5" width="3" height="5.5" rx="1" fill="#9aa6b8" transform="rotate(40 51.5 20.2)"/></g><g class="sparks"><circle cx="56" cy="27" r="1.1"/><circle cx="58" cy="23" r="0.9"/><circle cx="55" cy="31" r="0.8"/></g>`,
  review: `<g class="prop prop-tube"><path d="M45 16 h7 v15 a3.5 3.5 0 0 1 -7 0 z" fill="#ffffff22" stroke="#e9eef6" stroke-width="1.4"/><path d="M45.7 24 h5.6 v7 a2.8 2.8 0 0 1 -5.6 0 z" fill="#34d399"/></g><g class="bubbles"><circle cx="47.5" cy="22" r="1"/><circle cx="49.5" cy="19" r="0.8"/><circle cx="48" cy="16" r="0.7"/></g>`,
  release: `<g class="prop prop-keys"><circle cx="47" cy="25" r="3.4" fill="none" stroke="#ffd23f" stroke-width="1.6"/><path d="M48 28 v8 h2 M48 32 h2" stroke="#ffd23f" stroke-width="1.6" fill="none"/><path d="M45 28 l-3 7 l2 0.8 M43.5 31.5 l1.8 0.7" stroke="#e9c46a" stroke-width="1.6" fill="none"/></g>`,
};

// robotCop draws one unit's officer in the state its light reports: on a case
// (working its prop), standing by, asleep through quiet hours, or stuck and
// waving under a red light.
export function robotCop(role, light = "waiting") {
  const kit = props[role];
  const prop = kit && typeof kit === "object" ? (light === "active" ? kit.on : kit.off) : kit || "";
  const asleep = light === "quiet";
  const eyes = asleep
    ? `<path class="eyes" d="M22 21.5 h4 M30 21.5 h4" stroke="#7dd3fc" stroke-width="1.6" stroke-linecap="round"/>`
    : `<g class="eyes"><circle cx="24" cy="21.5" r="1.9" fill="#7dd3fc"/><circle cx="32" cy="21.5" r="1.9" fill="#7dd3fc"/></g>`;
  const extra = asleep
    ? `<g class="zzz" fill="#b4bfd0" font-family="monospace" font-weight="700"><text x="40" y="12" font-size="7">z</text><text x="45" y="7" font-size="8">z</text><text x="50" y="2" font-size="9">Z</text></g>`
    : light === "blocked"
      ? `<g class="help"><circle cx="46" cy="9" r="6" fill="#ff4757"/><text x="46" y="12.2" text-anchor="middle" font-family="monospace" font-weight="800" font-size="9" fill="#fff">!</text></g>`
      : "";
  return `<svg class="cop cop-${light}" viewBox="0 0 60 58" aria-hidden="true" focusable="false"><ellipse cx="28" cy="54" rx="14" ry="2.6" fill="#000" opacity=".4"/><g class="cop-body"><rect x="21" y="43" width="5" height="9" rx="1.5" fill="#16243d"/><rect x="30" y="43" width="5" height="9" rx="1.5" fill="#16243d"/><rect x="17" y="29" width="22" height="16" rx="4" fill="#1d3157"/><path d="M25 31 l1 2 2.2.3-1.6 1.5.4 2.2-2-1.1-2 1.1.4-2.2-1.6-1.5 2.2-.3z" fill="#ffd23f"/><rect x="17" y="41" width="22" height="2.2" fill="#0b1017"/><rect x="12" y="31" width="5" height="10" rx="2.5" fill="#1d3157"/><rect x="38" y="29" width="8" height="5" rx="2.5" fill="#1d3157"/><rect x="15" y="12" width="26" height="18" rx="6" fill="#cfd8e6"/><rect x="18" y="17" width="20" height="9" rx="3.5" fill="#0b1017"/>${eyes}<path d="M14 13 Q28 1 42 13 Z" fill="#13213a"/><rect x="14" y="11" width="28" height="3.6" rx="1" fill="#0b1017"/><path d="M13 14.4 h30 l-3 3 h-24 z" fill="#05070b"/><path d="M28 5 l.9 1.8 2 .3-1.4 1.3.3 2-1.8-.9-1.8.9.3-2-1.4-1.3 2-.3z" fill="#ffd23f"/></g>${prop}${extra}</svg>`;
}

// ───────────── the slop ─────────────

// The slop is a blob of green goo with googly eyes. A cuffed one has earned
// its placard in the tank.
export function slopBlob({ cuffed = false } = {}) {
  const mouth = cuffed
    ? `<path d="M15 26 q5 -3 10 0" stroke="#1a2e05" stroke-width="1.6" fill="none" stroke-linecap="round"/>`
    : `<path d="M14 24 q6 5 12 0" stroke="#1a2e05" stroke-width="1.6" fill="none" stroke-linecap="round"/>`;
  const cuffs = cuffed
    ? `<g class="cuffs" fill="none" stroke="#c9d3e0" stroke-width="1.8"><circle cx="14.5" cy="33" r="3"/><circle cx="25.5" cy="33" r="3"/><path d="M17.5 33 h5" stroke-dasharray="1.4 1"/></g>`
    : "";
  return `<svg class="slop${cuffed ? " cuffed" : ""}" viewBox="0 0 40 40" aria-hidden="true" focusable="false"><g class="goo"><path d="M6 31 C2 31 2 23 7 21 C6 13 13 8 20 9 C27 7 34 13 33 20 C38 22 38 31 33 31 C33 35 29 34 28 32 C26 36 22 36 21 32 C18 35 14 35 13 32 C11 34 7 34 6 31 Z" fill="#84cc16" stroke="#3f6212" stroke-width="1.2"/><ellipse cx="13" cy="15" rx="3.5" ry="2" fill="#ecfccb" opacity=".55"/><path class="drip" d="M29 32 q1.2 4 0 6 q-1.2 -2 0 -6z" fill="#84cc16"/><g class="googly"><circle cx="15" cy="18" r="4" fill="#fff"/><circle cx="25" cy="17" r="4" fill="#fff"/><circle class="pupil" cx="15.8" cy="18.6" r="1.9" fill="#0b1017"/><circle class="pupil" cx="25.8" cy="17.6" r="1.9" fill="#0b1017"/></g>${mouth}</g>${cuffs}</svg>`;
}

// ───────────── the motor pool ─────────────

// A squad car with its bar lit; a paddy wagon has a barred window with the
// suspect peering out.
export function cruiser({ wagon = false } = {}) {
  const wheels = `<g class="wheel"><circle cx="20" cy="28" r="5.5" fill="#05070b" stroke="#7b889d" stroke-width="1.6"/><path d="M17 28 h6 M20 25 v6" stroke="#7b889d" stroke-width="1"/></g><g class="wheel"><circle cx="62" cy="28" r="5.5" fill="#05070b" stroke="#7b889d" stroke-width="1.6"/><path d="M59 28 h6 M62 25 v6" stroke="#7b889d" stroke-width="1"/></g>`;
  const bar = `<rect class="bar-red" x="33" y="2" width="7" height="4" rx="1.5" fill="#ff4757"/><rect class="bar-blue" x="40" y="2" width="7" height="4" rx="1.5" fill="#3b8cff"/>`;
  const body = wagon
    ? `<path d="M4 28 V10 Q4 7 7 7 H54 L64 15 H75 Q79 15 79 19 V28 Z" fill="#0d1420" stroke="#e9eef6" stroke-width="1.3"/><rect x="10" y="11" width="28" height="11" rx="1.5" fill="#05070b"/><g class="inmate"><path d="M13 22 q2 -8 9 -8 q8 0 9 8 z" fill="#84cc16"/><circle cx="19" cy="17" r="2" fill="#fff"/><circle cx="25" cy="17" r="2" fill="#fff"/><circle cx="19.5" cy="17.4" r="1" fill="#0b1017"/><circle cx="25.5" cy="17.4" r="1" fill="#0b1017"/></g><path d="M15 11 v11 M20 11 v11 M25 11 v11 M30 11 v11 M35 11 v11" stroke="#9aa6b8" stroke-width="1.3"/><rect x="42" y="18" width="20" height="5" fill="#e9eef6"/><text x="52" y="22.4" text-anchor="middle" font-family="monospace" font-weight="800" font-size="4.4" fill="#0b1017">SLOP</text><path d="M56 9 l7 6 h-7 z" fill="#3b8cff55"/><rect x="76" y="19" width="3" height="3" fill="#ffd23f"/>`
    : `<path d="M4 28 L8 19 L20 17 L28 9 L52 9 L60 17 L75 19 L78 28 Z" fill="#0d1420" stroke="#e9eef6" stroke-width="1.3"/><rect x="24" y="18" width="30" height="8" fill="#e9eef6"/><path d="M38 19.3 l.9 1.8 2 .3-1.4 1.3.3 2-1.8-.9-1.8.9.3-2-1.4-1.3 2-.3z" fill="#ffd23f"/><path d="M30 10.5 L38 10.5 L38 16.5 L23 16.5 Z" fill="#3b8cff55"/><path d="M41 10.5 L51 10.5 L58 16.5 L41 16.5 Z" fill="#3b8cff55"/><rect x="74" y="20" width="3" height="3" fill="#ffd23f"/>`;
  return `<svg class="car${wagon ? " wagon" : ""}" viewBox="0 0 82 36" aria-hidden="true" focusable="false">${body}${bar}${wheels}</svg>`;
}

// ───────────── stamps and custody ─────────────

// A stamp says, in one word, what a transfer meant to the lane that got it.
export function stampFor(event) {
  if (event?.kind === "error") return { text: "STUCK", tone: "red" };
  if (event?.kind !== "delivery") return null;
  const { from, to } = event;
  if (to === "outside") return { text: "RELEASED", tone: "green" };
  if (from === "review" && to === "release") return { text: "CLEARED", tone: "green" };
  if (from === "review" && to === "issue") return { text: "SENT BACK", tone: "orange" };
  if (from === "hall") return { text: "ADMITTED", tone: "green" };
  if (to === "hall") return { text: "ON THE DOCKET", tone: "yellow" };
  if (to === "simplifier") return { text: "BOOKED", tone: "yellow" };
  if (to === "issue") return { text: "ASSIGNED", tone: "blue" };
  if (to === "review") return { text: "EVIDENCE IN", tone: "blue" };
  return { text: "TRANSFERRED", tone: "blue" };
}

// A finding Forensics sends back is a citation: the slop gets a ticket.
export function isCitation(event) {
  return event?.kind === "delivery" && event.from === "review" && event.to === "issue";
}

// inCustody is a case the squad threw out: dismissed at the Courthouse or by
// the Slop Squad. That is the slop in the tank.
export function inCustody(task) {
  return !!task && (task.stage === "declined" || task.mayoral_decision === "declined");
}

// custody lists the tank's occupants, newest booking first, and how many
// there are in all.
export function custody(town, limit = 12) {
  const perps = Object.values(town?.tasks || {})
    .filter(inCustody)
    .sort((a, b) => (Date.parse(b.updated || "") || 0) - (Date.parse(a.updated || "") || 0) || String(b.id).localeCompare(String(a.id)));
  return { total: perps.length, perps: perps.slice(0, limit), label: (task) => caseNumber(task) };
}

// newArrests compares two snapshots of one precinct and returns the cases that
// entered custody between them. Only committed state counts.
export function newArrests(before, after) {
  if (!before || !after) return [];
  return Object.values(after.tasks || {})
    .filter((task) => inCustody(task) && !inCustody(before.tasks?.[task.id]))
    .map((task) => task.id);
}

// ───────────── choreography ─────────────

const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function box(element, frame) {
  const r = element.getBoundingClientRect(), f = frame.getBoundingClientRect();
  return { x: r.left - f.left, y: r.top - f.top, w: r.width, h: r.height, cx: r.left - f.left + r.width / 2 };
}

function actor(layer, className, html) {
  const element = layer.ownerDocument.createElement("div");
  element.className = className;
  element.innerHTML = html;
  layer.append(element);
  return element;
}

async function run(element, frames, options) {
  try {
    await element.animate(frames, { fill: "forwards", ...options }).finished;
  } catch {
    /* A cancelled animation simply ends the scene. */
  }
}

// A scene needs a real layout and the Web Animations API; anywhere without
// them (a test shim, an old browser) it quietly does nothing.
export function canStage(ctx) {
  return !!(ctx?.frame?.getBoundingClientRect && ctx.layer?.ownerDocument && typeof ctx.layer.animate === "function");
}

function lane(ctx, role) {
  return role === "outside" ? null : ctx.flow.querySelector(`[data-lane="${laneOf(role)}"]`);
}

export function stamp(ctx, laneElement, mark) {
  if (!mark || !laneElement || !canStage(ctx)) return;
  const at = box(laneElement, ctx.frame);
  const element = actor(ctx.layer, `stamp tone-${mark.tone}`, mark.text);
  element.style.left = `${at.cx}px`;
  element.style.top = `${at.y + 46}px`;
  run(element, [
    { transform: "translate(-50%, 0) scale(2.4) rotate(-14deg)", opacity: 0 },
    { transform: "translate(-50%, 0) scale(0.92) rotate(-9deg)", opacity: 1, offset: 0.18 },
    { transform: "translate(-50%, 0) scale(1) rotate(-9deg)", opacity: 1, offset: 0.26 },
    { transform: "translate(-50%, 0) scale(1) rotate(-9deg)", opacity: 1, offset: 0.8 },
    { transform: "translate(-50%, 0) scale(1) rotate(-9deg)", opacity: 0 },
  ], { duration: 1700, easing: "ease-out" }).then(() => element.remove());
}

// streetY is where wheels meet the asphalt, relative to the frame.
function streetY(ctx) {
  const street = box(ctx.street, ctx.frame);
  return street.y + street.h - 32;
}

async function drive(ctx, car, fromX, toX) {
  const left = toX < fromX;
  car.classList.toggle("left", left);
  const distance = Math.abs(toX - fromX);
  await run(car, [
    { transform: `translate(${fromX - 41}px, 0)` },
    { transform: `translate(${toX - 41}px, 0)` },
  ], { duration: Math.min(3400, Math.max(1100, distance * 4.2)), easing: "cubic-bezier(.45, 0, .25, 1)" });
}

// patrolRun drives a case from the lane that sent it to the lane that got it,
// siren going, then stamps the arrival. Clicking the car opens the case.
export async function patrolRun(ctx, event) {
  if (!canStage(ctx)) return;
  const source = lane(ctx, event.from), target = lane(ctx, event.to);
  const width = ctx.frame.getBoundingClientRect().width;
  const fromX = source ? box(source, ctx.frame).cx : -90;
  const toX = target ? box(target, ctx.frame).cx : width + 90;
  if (Math.abs(toX - fromX) < 24) {
    stamp(ctx, target, stampFor(event));
    return;
  }
  const car = actor(ctx.layer, "cruiser", `<span class="siren-call"><span>WEE</span><span>OO</span></span><span class="cruiser-tag">${callsign(event.from)} → ${callsign(event.to)} · ${cargoLabel(event.cargo)}</span>${cruiser()}`);
  car.style.top = `${streetY(ctx)}px`;
  car.title = `${event.title || ""}`.trim();
  if (event.cargo && ctx.open) {
    car.classList.add("clickable");
    car.onclick = () => ctx.open(event.cargo, event.to);
  }
  await drive(ctx, car, fromX, toX);
  car.remove();
  stamp(ctx, target, stampFor(event));
  if (isCitation(event)) cite(ctx, source);
}

// alarm answers an error on the radio: the unit that hit trouble gets a
// STUCK stamp on its lane.
export function alarm(ctx, event) {
  if (!canStage(ctx)) return;
  stamp(ctx, lane(ctx, event.to && event.to !== "outside" ? event.to : event.from), stampFor(event));
}

// cite hands the slop a ticket where Forensics found it.
export async function cite(ctx, laneElement) {
  if (!laneElement || !canStage(ctx)) return;
  const at = box(laneElement, ctx.frame);
  const perp = actor(ctx.layer, "perp-actor small", `${slopBlob()}<span class="ticket">CITED</span>`);
  perp.style.left = `${at.cx - 20}px`;
  perp.style.top = `${at.y + 70}px`;
  await run(perp, [
    { transform: "translateY(18px) scale(0.2)", opacity: 0 },
    { transform: "translateY(0) scale(1.15)", opacity: 1, offset: 0.2 },
    { transform: "translateY(0) scale(1)", opacity: 1, offset: 0.3 },
    { transform: "translateY(0) scale(1)", opacity: 1, offset: 0.85 },
    { transform: "translateY(10px) scale(0.6)", opacity: 0 },
  ], { duration: 2000, easing: "ease-out" });
  perp.remove();
}

// arrest is the big one: the slop oozes out of the lane where it was thrown
// out, gets cuffed, and the paddy wagon hauls it to the tank.
export async function arrest(ctx, task) {
  if (!canStage(ctx) || !ctx.tank) return;
  const role = task.house === "simplifier" || task.simplification?.mode === "auto" ? "simplifier" : "hall";
  const scene = lane(ctx, role);
  if (!scene) return;
  const at = box(scene, ctx.frame);
  const road = streetY(ctx);
  const perp = actor(ctx.layer, "perp-actor", `${slopBlob()}<span class="busted">BUSTED!</span>`);
  perp.style.left = `${at.cx - 24}px`;
  perp.style.top = `${at.y + 64}px`;
  await run(perp, [
    { transform: "translateY(24px) scale(0.1, 0.1)", opacity: 0 },
    { transform: "translateY(0) scale(1.25, 0.8)", opacity: 1, offset: 0.45 },
    { transform: "translateY(0) scale(0.92, 1.1)", offset: 0.7 },
    { transform: "translateY(0) scale(1, 1)", opacity: 1 },
  ], { duration: 650, easing: "ease-out" });
  await wait(250);
  perp.innerHTML = `${slopBlob({ cuffed: true })}<span class="busted on">BUSTED!</span><span class="placard">${caseNumber(task)}</span>`;
  perp.classList.add("caught");
  await wait(900);
  const wagon = actor(ctx.layer, "cruiser wagon-run", `<span class="siren-call"><span>WEE</span><span>OO</span></span>${cruiser({ wagon: true })}`);
  wagon.style.top = `${road}px`;
  wagon.querySelector(".inmate")?.setAttribute("opacity", "0");
  await drive(ctx, wagon, -90, at.cx);
  await run(perp, [
    { transform: "translate(0, 0) scale(1)", opacity: 1 },
    { transform: `translate(${-10}px, ${road - (at.y + 64) - 10}px) scale(0.5)`, opacity: 0 },
  ], { duration: 450, easing: "ease-in" });
  perp.remove();
  wagon.querySelector(".inmate")?.setAttribute("opacity", "1");
  const tank = box(ctx.tank, ctx.frame);
  await drive(ctx, wagon, at.cx, tank.cx);
  wagon.remove();
  ctx.booked?.(task.id);
}
