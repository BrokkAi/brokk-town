import {
  positions,
  roadSegments,
  roadLevels,
  houseDoor,
  houseRoad,
  routePosition,
} from "./town.js";
import { seededRandom, easeDelivery, workerPose, cargoLabel } from "./scenery.js";

// The swarm skin reads the same committed state as the village and paints it
// as a defence: every house is an encampment, a delivery is a raid that leaves
// a burrow at the target's gate, and the burrows a house shows are its queue.
// Nothing here keeps per-frame state; every position is a function of the
// raid's progress, the clock and a seed, so reloads and reduced motion cost
// nothing.
export const DURATION = 8000;
export const STRIKE = [0.6, 0.75];
export const MAX_PACK = 16;
export const MAX_BURROWS = 6;

export function raidKind(m) {
  if (m.to === "outside") return "sortie";
  if (m.from === "hall") return "airdrop";
  return m.from === "outside" ? "incursion" : "raid";
}
export function packSize(m) {
  const kind = raidKind(m);
  if (kind === "airdrop") return 0;
  if (kind === "sortie") return String(m.cargo || "").startsWith("release:") ? 6 : 4;
  const cargo = String(m.cargo || "");
  let size = cargo.startsWith("issue:") ? 6 : cargo.startsWith("pr:") ? 9 : cargo.startsWith("release:") ? 12 : 5;
  if (kind === "incursion") size += 4;
  return Math.min(MAX_PACK, size);
}
export function raidPhase(progress) {
  const p = Math.max(0, Math.min(1, progress));
  if (p < STRIKE[0]) return { stage: "approach", t: p / STRIKE[0] };
  if (p < STRIKE[1]) return { stage: "strike", t: (p - STRIKE[0]) / (STRIKE[1] - STRIKE[0]) };
  return { stage: "withdraw", t: (p - STRIKE[1]) / (1 - STRIKE[1]) };
}
const layouts = new WeakMap();
export function packLayout(m) {
  let pack = layouts.get(m);
  if (pack) return pack;
  const rand = seededRandom(`${m.cargo}|${m.from}|${m.to}`),
    size = packSize(m);
  pack = Array.from({ length: size }, (_, i) => ({
    lag: i === 0 ? 0 : rand() * 0.42,
    side: (rand() - 0.5) * 34,
    scale: 0.75 + rand() * 0.45,
    gait: rand() * Math.PI * 2,
  }));
  layouts.set(m, pack);
  return pack;
}
function trail(q0, lag) {
  return Math.max(0, Math.min(1, q0 * (1 + lag) - lag));
}
// The house labels sit over the upper road, so a horde cuts across the lawns
// just above it instead of marching unseen behind the labels. The lift fades
// in over the last stretch of a side path so the pack never jumps.
export const LIFT = 62;
export function lift(p) {
  const near = Math.max(0, 1 - Math.abs(p.y - roadLevels.upper) / 40);
  return near ? -LIFT * near : 0;
}
function offset(p, side) {
  return { x: p.x + side * 0.35, y: p.y + side * 0.6 + lift(p), direction: p.direction };
}
// A creature's place during a raid: it trails the leader on the road, boils
// around the gate during the strike, then scuttles home and fades.
export function creaturePosition(m, i, progress, now = 0, motion = true) {
  const unit = packLayout(m)[i] || { lag: 0, side: 0, scale: 1, gait: 0 },
    { stage, t } = raidPhase(progress),
    gate = houseDoor(m.to),
    jitter = motion ? Math.sin(now / 90 + unit.gait * 7) * 3 : 0;
  if (stage === "approach") {
    const p = offset(routePosition(m.from, m.to, trail(easeDelivery(t), unit.lag)), unit.side);
    return { ...p, alpha: 1, frenzy: 0 };
  }
  if (stage === "strike") {
    const spread = 0.6 + t * 0.9;
    return {
      x: gate[0] + unit.side * 2.2 * spread + jitter,
      y: gate[1] - 4 + (unit.lag * 60 - 12) * spread + jitter * 0.4,
      direction: unit.side >= 0 ? -1 : 1,
      alpha: 1,
      frenzy: 1,
    };
  }
  const p = offset(routePosition(m.to, m.from, trail(easeDelivery(t), unit.lag)), unit.side);
  return { ...p, alpha: Math.max(0, 1 - t * 1.25), frenzy: 0 };
}
// How hard a raid is hitting its gate right now: full on impact, fading as
// the pack scatters. Airdrops and sorties never strike.
export function strike(m, progress) {
  const kind = raidKind(m);
  if (kind === "airdrop" || kind === "sortie") return 0;
  const { stage, t } = raidPhase(progress);
  return stage === "strike" ? 1 - t * 0.7 : 0;
}
function lifted(p) {
  return { ...p, y: p.y + lift(p) };
}
export function deliveryPoint(m, progress) {
  const kind = raidKind(m);
  if (kind === "airdrop" || kind === "sortie")
    return lifted(routePosition(m.from, m.to, easeDelivery(progress)));
  const { stage, t } = raidPhase(progress);
  if (stage === "approach") return lifted(routePosition(m.from, m.to, easeDelivery(t)));
  const [x, y] = houseDoor(m.to);
  return { x, y, direction: 1 };
}
// Burrows are the house's queue, so they come from workload counts rather than
// from the raid animation that explained their arrival.
export function burrowLayout(counts = {}) {
  const active = Math.min(1, counts.active || 0),
    blocked = Math.min(MAX_BURROWS - active, counts.blocked || 0),
    waiting = Math.min(MAX_BURROWS - active - blocked, counts.waiting || 0),
    out = [];
  if (active) out.push({ kind: "active", x: -46, y: 82 });
  for (let i = 0; i < waiting; i++)
    out.push({ kind: "waiting", x: -92 + (i % 2) * 22, y: 62 + Math.floor(i / 2) * 14 - (i % 2) * 5 });
  for (let i = 0; i < blocked; i++)
    out.push({ kind: "blocked", x: 108 - (i % 2) * 22, y: 62 + Math.floor(i / 2) * 14 - (i % 2) * 5 });
  return out;
}

function oval(c, x, y, rx, ry, color) {
  c.fillStyle = color;
  c.beginPath();
  c.ellipse(x, y, rx, ry, 0, 0, Math.PI * 2);
  c.fill();
}
function onRoad(x, y) {
  return roadSegments.some(([a, b]) =>
    a[0] === b[0]
      ? Math.abs(x - a[0]) < 26 && y >= Math.min(a[1], b[1]) && y <= Math.max(a[1], b[1])
      : Math.abs(y - a[1]) < 26 && x >= Math.min(a[0], b[0]) && x <= Math.max(a[0], b[0]),
  );
}
function nearHouse(x, y) {
  return Object.entries(positions).some(
    ([role, [hx, hy]]) => role !== "outside" && Math.abs(x - hx) < 150 && Math.abs(y - hy) < 135,
  );
}
function crater(c, x, y, s) {
  oval(c, x + 3, y + 3, 30 * s, 12 * s, "#0d0b0955");
  oval(c, x, y, 28 * s, 11 * s, "#15120e");
  oval(c, x, y - 2, 22 * s, 8 * s, "#221c15");
  c.strokeStyle = "#5c5340";
  c.lineWidth = 2;
  c.beginPath();
  c.ellipse(x, y - 4, 30 * s, 12 * s, 0, Math.PI * 1.1, Math.PI * 1.9);
  c.stroke();
}
function stockade(c, x, y) {
  const cx = x,
    cy = y + 52,
    rx = 122,
    ry = 46,
    gap = 0.16;
  c.lineCap = "butt";
  for (const [width, color, lift] of [
    [10, "#1d1811", 3],
    [8, "#5a4d38", 0],
    [3, "#8b7a58", -2],
  ]) {
    c.strokeStyle = color;
    c.lineWidth = width;
    c.beginPath();
    c.ellipse(cx, cy + lift, rx, ry, 0, 0.08, Math.PI / 2 - gap);
    c.stroke();
    c.beginPath();
    c.ellipse(cx, cy + lift, rx, ry, 0, Math.PI / 2 + gap, Math.PI - 0.08);
    c.stroke();
  }
  for (let a = 0.16; a < Math.PI - 0.1; a += 0.13) {
    if (Math.abs(a - Math.PI / 2) < gap + 0.04) continue;
    const px = cx + Math.cos(a) * rx,
      py = cy + Math.sin(a) * ry;
    c.strokeStyle = "#a08d63";
    c.lineWidth = 2.5;
    c.beginPath();
    c.moveTo(px, py + 2);
    c.lineTo(px, py - 11);
    c.stroke();
  }
  for (const side of [-1, 1]) {
    const px = cx + side * 24,
      py = cy + ry - 2;
    c.fillStyle = "#3a3126";
    c.fillRect(px - 2, py - 26, 4, 28);
    c.fillStyle = "#f4c467";
    c.fillRect(px - 3, py - 30, 6, 5);
    oval(c, px, py - 28, 9, 6, "#f4c46722");
  }
}
export function landscape(seed) {
  const canvas = document.createElement("canvas");
  canvas.width = 1120;
  canvas.height = 680;
  const c = canvas.getContext("2d"),
    rand = seededRandom(`swarm:${seed}`);
  const ground = c.createLinearGradient(0, 0, 900, 680);
  ground.addColorStop(0, "#3d3629");
  ground.addColorStop(0.55, "#2c2a23");
  ground.addColorStop(1, "#1e231f");
  c.fillStyle = ground;
  c.fillRect(0, 0, 1120, 680);
  for (let i = 0; i < 80; i++)
    oval(c, rand() * 1120, rand() * 680, 40 + rand() * 110, 12 + rand() * 32, i % 2 ? "#6a5a3a12" : "#0f0d0a2a");
  for (let i = 0; i < 2600; i++) {
    const x = rand() * 1120,
      y = rand() * 680;
    c.fillStyle = ["#6d665366", "#2a262066", "#8c7d5c44", "#4d4b3c66"][i % 4];
    c.fillRect(x, y, 1 + rand() * 3, 1 + rand() * 2);
  }
  c.lineCap = "round";
  c.lineJoin = "round";
  c.beginPath();
  for (const [a, b] of roadSegments) {
    c.moveTo(...a);
    c.lineTo(...b);
  }
  c.strokeStyle = "#14120f";
  c.lineWidth = 42;
  c.stroke();
  c.strokeStyle = "#48412f";
  c.lineWidth = 30;
  c.stroke();
  c.strokeStyle = "#57503d";
  c.lineWidth = 16;
  c.stroke();
  for (let i = 0; i < 3800; i++) {
    const x = rand() * 1120,
      y = rand() * 680;
    if (onRoad(x, y)) {
      c.fillStyle = i % 3 ? "#2b261d70" : "#7d7156aa";
      c.fillRect(x, y, 1 + rand() * 3, 1.5);
    }
  }
  let craters = 0;
  for (let tries = 0; tries < 400 && craters < 14; tries++) {
    const x = rand() * 1120,
      y = rand() * 680;
    if (onRoad(x, y) || nearHouse(x, y)) continue;
    crater(c, x, y, 0.5 + rand() * 0.9);
    craters++;
  }
  for (let i = 0; i < 26; i++) {
    const x = rand() * 1120,
      y = rand() * 680;
    if (onRoad(x, y) || nearHouse(x, y)) continue;
    c.strokeStyle = i % 2 ? "#cfc3a0" : "#9d947a";
    c.lineWidth = 2;
    c.beginPath();
    c.moveTo(x, y);
    c.lineTo(x + 6 + rand() * 12, y - 2 + rand() * 6);
    c.stroke();
  }
  for (const [role, [x, y]] of Object.entries(positions)) {
    if (role === "outside") continue;
    oval(c, x, y + 62, 118, 36, "#1a1712aa");
    oval(c, x - 6, y + 58, 100, 27, "#5b533e33");
    for (let i = 0; y + 86 + i * 7 < houseRoad(role); i++) {
      c.fillStyle = i % 2 ? "#5d5443" : "#77694f";
      c.fillRect(x - 9 + (i % 2) * 2, y + 86 + i * 7, 17, 4);
    }
    stockade(c, x, y);
  }
  const fog = c.createRadialGradient(560, 320, 200, 560, 340, 720);
  fog.addColorStop(0, "#05070600");
  fog.addColorStop(1, "#050706b0");
  c.fillStyle = fog;
  c.fillRect(0, 0, 1120, 680);
  return canvas;
}

export function creature(c, x, y, { scale = 1, direction = 1, gait = 0, hostile = false, alpha = 1, frenzy = 0 }) {
  c.save();
  c.globalAlpha = alpha;
  c.translate(x, y);
  c.scale(scale * (direction >= 0 ? 1 : -1), scale);
  oval(c, 1, 5, 9, 3, "#00000055");
  for (let i = 0; i < 3; i++) {
    const swing = Math.sin(gait + i * 2.1) * (2 + frenzy * 2);
    c.strokeStyle = hostile ? "#3d0f16" : "#2e150d";
    c.lineWidth = 1.5;
    for (const s of [-1, 1]) {
      c.beginPath();
      c.moveTo(-4 + i * 4, 0);
      c.lineTo(-6 + i * 5 + swing, s * (5 + i));
      c.stroke();
    }
  }
  oval(c, 0, 0, 7, 4, hostile ? "#7a1f2b" : "#5e2a1c");
  oval(c, -1, -1, 5, 2, hostile ? "#b8323f" : "#a5462f");
  oval(c, 7, -0.5, 3.2, 2.6, hostile ? "#5c1520" : "#4a1f14");
  c.strokeStyle = hostile ? "#b8323f" : "#a5462f";
  c.lineWidth = 1.2;
  c.beginPath();
  c.moveTo(-6, 0);
  c.lineTo(-12, -2 + Math.sin(gait * 1.3) * 2);
  c.stroke();
  c.fillStyle = "#f2e26a";
  c.fillRect(7.5, -2, 1.4, 1.4);
  c.fillRect(7.5, 0.6, 1.4, 1.4);
  c.restore();
}
function walker(c, x, y, direction, gait, alpha = 1) {
  c.save();
  c.globalAlpha = alpha;
  c.translate(x, y);
  c.scale(direction >= 0 ? 1 : -1, 1);
  oval(c, 0, 8, 10, 3, "#00000055");
  c.strokeStyle = "#2f2b22";
  c.lineWidth = 2.5;
  for (const s of [-1, 1]) {
    c.beginPath();
    c.moveTo(s * 3, 2);
    c.lineTo(s * 5 + Math.sin(gait + s) * 3, 8);
    c.stroke();
  }
  c.fillStyle = "#8f8a6a";
  c.fillRect(-7, -6, 14, 9);
  c.fillStyle = "#c9c19a";
  c.fillRect(-7, -6, 14, 3);
  c.fillStyle = "#3f3b2e";
  c.fillRect(6, -3, 8, 2.5);
  c.fillStyle = "#9be07a";
  c.fillRect(-4, -4, 2, 2);
  c.restore();
}
function burrow(c, x, y, kind, now, motion) {
  const pulse = motion ? 0.5 + Math.sin(now / 260) * 0.5 : 0.5;
  oval(c, x + 2, y + 3, 24, 9, "#00000066");
  oval(c, x, y, 22, 8, "#2a1c16");
  oval(c, x, y - 3, 16, 5, "#5a3a2c");
  oval(c, x, y - 2, 9, 3.5, "#0d0806");
  if (kind === "waiting") {
    oval(c, x, y - 2, 6, 2, `rgba(160,60,40,${0.15 + pulse * 0.25})`);
    return;
  }
  if (kind === "active") {
    oval(c, x, y - 2, 8, 3, `rgba(255,150,60,${0.35 + pulse * 0.4})`);
    return;
  }
  for (let i = 0; i < 3; i++) {
    const p = motion ? (now / 1400 + i / 3) % 1 : i / 3;
    oval(c, x - 4 + i * 4 + p * 6, y - 8 - p * 26, 3 + p * 6, 2 + p * 4, `rgba(120,110,100,${(1 - p) * 0.3})`);
  }
  for (let i = 0; i < 2; i++) {
    const a = (motion ? now / 700 : 0) + i * Math.PI;
    creature(c, x + Math.cos(a) * 20, y + 2 + Math.sin(a) * 7, {
      scale: 1.05,
      direction: Math.cos(a + 0.3) - Math.cos(a) >= 0 ? 1 : -1,
      gait: motion ? now / 60 : 0,
      hostile: true,
    });
  }
}
export function drawHouse(c, role, x, y, { selected, sprites, counts, worker, now, motion, strike = 0 }) {
  if (selected) {
    c.save();
    c.strokeStyle = "#9be07a";
    c.lineWidth = 2;
    c.setLineDash([14, 10]);
    c.lineDashOffset = motion ? -now / 40 : 0;
    c.beginPath();
    c.ellipse(x, y + 72, 122, 30, 0, 0, Math.PI * 2);
    c.stroke();
    c.restore();
    c.fillStyle = "#9be07a12";
    c.beginPath();
    c.ellipse(x, y + 72, 122, 30, 0, 0, Math.PI * 2);
    c.fill();
  }
  const shake = motion ? strike : 0,
    dx = Math.sin(now / 23) * 4 * shake,
    dy = Math.cos(now / 29) * 2 * shake;
  sprites.building(role, x + dx, y + dy);
  if (strike > 0) {
    const [gx, gy] = houseDoor(role);
    oval(c, gx, gy - 6, 26 + 18 * strike, 12 + 8 * strike, `rgba(255,170,60,${0.45 * strike})`);
    oval(c, gx, gy - 6, 12, 6, `rgba(255,240,200,${0.6 * strike})`);
  }
  for (const b of burrowLayout(counts)) burrow(c, x + b.x, y + b.y, b.kind, now, motion);
  const breached = worker?.status === "blocked" || worker?.status === "failed" || (counts?.blocked || 0) > 0;
  if (breached) {
    for (let i = 0; i < 4; i++) {
      const p = motion ? (now / 1600 + i / 4) % 1 : i / 4;
      oval(c, x + 74 + p * 14, y + 50 - p * 60, 5 + p * 10, 4 + p * 6, `rgba(90,80,75,${(1 - p) * 0.35})`);
    }
    const beacon = motion ? 0.4 + Math.abs(Math.sin(now / 350)) * 0.6 : 0.8;
    c.fillStyle = "#3a3126";
    c.fillRect(x + 100, y + 18, 4, 30);
    oval(c, x + 102, y + 16, 9, 9, `rgba(255,70,60,${beacon * 0.25})`);
    oval(c, x + 102, y + 16, 4, 4, `rgba(255,90,70,${beacon})`);
  }
}
// The defender burns whichever burrow is being worked; with nothing queued
// yet, it scorches the ground where the next one will land.
export function drawWorking(c, role, x, y, now, motion, paintWorker, counts) {
  const pose = workerPose(role, now, motion),
    t = pose.phase,
    wx = x + pose.x,
    wy = y + pose.y,
    target = burrowLayout(counts)[0] || burrowLayout({ active: 1 })[0],
    tx = x + target.x,
    ty = y + target.y - 3;
  oval(c, wx + 3, wy + 24, 19, 5, "#00000090");
  if (pose.working || !motion) {
    const flicker = motion ? Math.sin(now / 35) * 0.15 : 0;
    for (let i = 0; i < 7; i++) {
      const p = (i + 0.5) / 7,
        fx = wx + 14 + (tx - wx - 14) * p,
        fy = wy + 2 + (ty - wy - 2) * p + Math.sin(now / 70 + i) * (motion ? 2 : 0),
        colors = ["#fff4c0", "#ffd36b", "#ff9a3c", "#ff6a2c", "#c8402a"],
        color = colors[Math.min(colors.length - 1, Math.floor(p * colors.length))];
      c.globalAlpha = Math.max(0.15, 0.9 - p * 0.6 + flicker);
      oval(c, fx, fy, 4 + p * 9, 3 + p * 5, color);
    }
    c.globalAlpha = 1;
    for (let i = 0; i < 5; i++) {
      const p = motion ? (t * 1.1 + i / 5) % 1 : i / 5;
      c.fillStyle = `rgba(255,${160 - p * 80},60,${1 - p})`;
      c.fillRect(tx - 10 + Math.sin(i * 2.4) * p * 18, ty - 6 - p * 28, 2.5, 2.5);
    }
    const cycle = motion ? t % 7 : 3.5,
      fill = Math.max(0.05, Math.min(1, (cycle - 2.8) / 4.2));
    c.fillStyle = "#0b1a10";
    c.fillRect(wx - 22, wy - 40, 44, 7);
    c.fillStyle = "#8fe36a";
    c.fillRect(wx - 20, wy - 38, 40 * fill, 3);
  }
  c.save();
  c.translate(wx, wy);
  c.rotate(pose.tilt);
  paintWorker(5);
  c.restore();
}
export function drawDelivery(c, m, progress, now, sprites, motion = true) {
  const kind = raidKind(m),
    label = cargoLabel(m.cargo);
  if (kind === "airdrop") {
    const p = lifted(routePosition(m.from, m.to, easeDelivery(progress))),
      hover = Math.sin(now / 140) * 2,
      right = p.direction >= 0;
    oval(c, p.x, p.y + 20, 16, 5, "#00000055");
    c.fillStyle = "#6d6a52";
    c.fillRect(p.x - 7, p.y - 6 + hover, 14, 12);
    c.fillStyle = "#9be07a";
    c.fillRect(p.x - 4, p.y - 2 + hover, 8, 2);
    c.fillStyle = "#c9c19a";
    c.fillRect(p.x - 12, p.y - 28 + hover, 24, 4);
    c.fillStyle = "#e7e2c3";
    c.fillRect(p.x - 18 + (right ? 0 : 12), p.y - 24 + hover, 6, 12);
    c.fillRect(p.x + 12 - (right ? 0 : 12), p.y - 24 + hover, 6, 12);
    c.strokeStyle = "#c9c19a";
    c.lineWidth = 1;
    c.beginPath();
    c.moveTo(p.x, p.y - 24 + hover);
    c.lineTo(p.x, p.y - 6 + hover);
    c.stroke();
    tag(c, p.x, p.y - 40 + hover, label, "#9be07a");
    return;
  }
  if (kind === "sortie") {
    const q = easeDelivery(progress),
      units = packSize(m);
    for (let i = 0; i < units; i++) {
      const p = lifted(routePosition(m.from, m.to, trail(q, i * 0.035)));
      walker(c, p.x + (i % 2 ? 9 : -9), p.y + (i % 2 ? 6 : -4), p.direction, motion ? now / 90 + i : 0);
    }
    const lead = lifted(routePosition(m.from, m.to, q));
    c.fillStyle = "#3a3126";
    c.fillRect(lead.x - 1, lead.y - 46, 2, 36);
    c.fillStyle = "#9be07a";
    c.beginPath();
    c.moveTo(lead.x + 1, lead.y - 46);
    c.lineTo(lead.x + 22, lead.y - 40);
    c.lineTo(lead.x + 1, lead.y - 33);
    c.closePath();
    c.fill();
    tag(c, lead.x, lead.y - 56, label, "#dbe6cc");
    return;
  }
  const hostile = kind === "incursion",
    { stage, t } = raidPhase(progress),
    pack = packLayout(m);
  if (stage === "strike") {
    const [gx, gy] = houseDoor(m.to);
    for (let i = 0; i < 6; i++) {
      const p = motion ? (now / 380 + i / 6) % 1 : i / 6;
      oval(c, gx - 30 + i * 12 + p * 8, gy - 2 - p * 22, 3 + p * 8, 2 + p * 5, `rgba(120,100,80,${(1 - p) * 0.4})`);
    }
  }
  const order = pack.map((_, i) => i).sort((a, b) => creaturePosition(m, a, progress, now, motion).y - creaturePosition(m, b, progress, now, motion).y);
  for (const i of order) {
    const p = creaturePosition(m, i, progress, now, motion);
    if (p.alpha <= 0) continue;
    creature(c, p.x, p.y, {
      scale: pack[i].scale * 1.5,
      direction: p.direction,
      gait: motion ? now / (p.frenzy ? 45 : 70) + pack[i].gait : pack[i].gait,
      hostile,
      alpha: p.alpha,
      frenzy: p.frenzy,
    });
  }
  if (stage !== "withdraw" || t < 0.4) {
    const lead = deliveryPoint(m, progress);
    tag(c, lead.x, lead.y - 30, label, hostile ? "#ff8a80" : "#f0c9b2", stage === "withdraw" ? 1 - t / 0.4 : 1);
  }
}
function tag(c, x, y, text, color, alpha = 1) {
  c.save();
  c.globalAlpha = alpha;
  c.fillStyle = "#0b0a08";
  c.fillRect(x - 34, y - 13, 68, 19);
  c.fillStyle = color;
  c.font = "11px monospace";
  c.textAlign = "center";
  c.fillText(text, x, y);
  c.restore();
}
export const swarm = {
  id: "swarm",
  label: "Swarm",
  duration: DURATION,
  landscape,
  drawHouse,
  drawWorking,
  deliveryPoint,
  drawDelivery,
  strike,
};
