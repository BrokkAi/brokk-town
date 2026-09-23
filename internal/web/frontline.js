import { positions, roadSegments } from "./town.js";
import { seededRandom, workerPose } from "./scenery.js";

// Frontline is a look, not a lever. Every strike it draws is one delivery Town
// already committed: the same task moving between the same two houses. It
// reads a snapshot and paints it; it never writes to GitHub or changes what a
// delivery means, so the theme can never move work by itself.

const palettes = {
  vanguard: {
    id: "vanguard",
    name: "Vanguard",
    species: "Humans",
    motto: "Hold the line, then take theirs.",
    armor: "#a8bccb",
    mid: "#3f7196",
    dark: "#152738",
    accent: "#8fd0ff",
    glow: "#e9f7ff",
    ground: "#1d2a33",
    hazard: "#f2c14e",
  },
  ascendancy: {
    id: "ascendancy",
    name: "Ascendancy",
    species: "Humanoid aliens",
    motto: "We have already won, in a longer frame.",
    armor: "#c9b6f2",
    mid: "#6a4fbd",
    dark: "#241a3f",
    accent: "#d9a6ff",
    glow: "#f6ecff",
    ground: "#2a2440",
    hazard: "#ffd27a",
  },
  hive: {
    id: "hive",
    name: "Hive",
    species: "Swarm of bugs",
    motto: "One mind, ten thousand mouths.",
    armor: "#c9d98a",
    mid: "#5c7a2e",
    dark: "#1e2a12",
    accent: "#c8f45f",
    glow: "#eaffc0",
    ground: "#2a3320",
    hazard: "#ff9d4d",
  },
};

export const factionIds = ["vanguard", "ascendancy", "hive"];
export const factions = factionIds.map((id) => palettes[id]);
export const installationRoles = [
  "hall",
  "repo",
  "bug",
  "feature",
  "issue",
  "review",
  "release",
  "simplifier",
];

// Each atlas has four columns and two rows in installationRoles order. Image
// loading happens outside draw calls; until an atlas is ready, the original
// canvas silhouettes keep the base readable.
const installationArt = new Map();
let craftArt;
let defenderArt;
let artLoadScheduled = false;
export function preloadFrontlineArt() {
  if (typeof Image === "undefined" || artLoadScheduled) return;
  artLoadScheduled = true;
  // Start browser image requests in a separate task, outside the theme click
  // handler and the canvas animation loop.
  setTimeout(() => {
    if (!craftArt) {
      craftArt = new Image();
      craftArt.src = "/assets/frontline-craft.png";
    }
    if (!defenderArt) {
      defenderArt = new Image();
      defenderArt.src = "/assets/frontline-defenders.png";
    }
    for (const id of factionIds) {
      if (!installationArt.has(id)) {
        const atlas = new Image();
        atlas.src = `/assets/frontline-${id}.png`;
        installationArt.set(id, atlas);
      }
    }
  }, 0);
}
function ready(image) {
  return image?.complete && image.naturalWidth ? image : null;
}
function craftAtlas() {
  return ready(craftArt);
}
function atlasFor(faction) {
  const id = factionIds.includes(faction) ? faction : "vanguard";
  const atlas = installationArt.get(id);
  return ready(atlas);
}
function defenderAtlas() {
  return ready(defenderArt);
}

// Each base names its own installations. The first name is what a player
// reads on the map, the second is the compact form the journal has room for,
// and the third is the inspector's eyebrow.
const installationTables = {
  vanguard: {
    hall: ["Command Post", "Command", "THE COMMAND POST"],
    repo: ["Watchtower", "Tower", "THE WATCHTOWER"],
    bug: ["Interceptor Bay", "Bay", "THE INTERCEPTOR BAY"],
    feature: ["Prototype Works", "Works", "THE PROTOTYPE WORKS"],
    issue: ["Muster Yard", "Yard", "THE MUSTER YARD"],
    review: ["Sentry Line", "Sentry", "THE SENTRY LINE"],
    release: ["Launch Pad", "Pad", "THE LAUNCH PAD"],
    simplifier: ["Salvage Depot", "Depot", "THE SALVAGE DEPOT"],
  },
  ascendancy: {
    hall: ["Nexus Spire", "Nexus", "THE NEXUS SPIRE"],
    repo: ["Oculus Array", "Oculus", "THE OCULUS ARRAY"],
    bug: ["Stalker Roost", "Roost", "THE STALKER ROOST"],
    feature: ["Genesis Vault", "Vault", "THE GENESIS VAULT"],
    issue: ["Weaver Ring", "Ring", "THE WEAVER RING"],
    review: ["Warden Lattice", "Lattice", "THE WARDEN LATTICE"],
    release: ["Rift Gate", "Gate", "THE RIFT GATE"],
    simplifier: ["Refinery Choir", "Choir", "THE REFINERY CHOIR"],
  },
  hive: {
    hall: ["Queen's Chamber", "Queen", "THE QUEEN'S CHAMBER"],
    repo: ["Antenna Mound", "Mound", "THE ANTENNA MOUND"],
    bug: ["Hunter Warren", "Warren", "THE HUNTER WARREN"],
    feature: ["Hatchery", "Hatchery", "THE HATCHERY"],
    issue: ["Brood Vault", "Brood", "THE BROOD VAULT"],
    review: ["Sentinel Nest", "Nest", "THE SENTINEL NEST"],
    release: ["Spore Cannon", "Cannon", "THE SPORE CANNON"],
    simplifier: ["Digester", "Digester", "THE DIGESTER"],
  },
};

function hash(value) {
  let n = 2166136261;
  for (const c of String(value ?? "")) n = Math.imul(n ^ c.charCodeAt(0), 16777619);
  return Math.abs(n >>> 0);
}

// A base keeps its faction between restarts and machines: the assignment is a
// pure function of the repository, so two people looking at the same town see
// the same war.
export function factionFor(townId) {
  return factionIds[hash(townId) % factionIds.length];
}

// factionOf honors a player's choice and falls back to the derived base
// faction for anything else, so a stale saved value can never blank the map.
export function factionOf(townId, override) {
  return factionIds.includes(override) ? override : factionFor(townId);
}

export function paletteFor(factionId) {
  return palettes[factionId] || palettes.vanguard;
}

export function factionName(factionId) {
  return paletteFor(factionId).name;
}

// A raid that arrives from outside the map belongs to the next faction in the
// cycle: the invader is never the base it is hitting.
export function rivalFaction(factionId) {
  const index = factionIds.indexOf(factionId);
  return factionIds[(index + 1) % factionIds.length];
}

export function sectorNumber(townId) {
  return (hash(townId) % 89) + 10;
}

export function sectorLabel(townId, factionId) {
  return `${factionName(factionId)} base · sector ${sectorNumber(townId)}`;
}

// Status strings the service already writes ("Town is paused", "the house is
// waiting") carry the town's nouns. The skin renames the noun and leaves the
// meaning alone, so the header never claims anything the service did not say.
const wordMap = {
  town: "base",
  towns: "bases",
  house: "structure",
  houses: "structures",
  delivery: "strike",
  deliveries: "strikes",
  neighborhood: "base",
  neighborhoods: "bases",
};

export function frontlineWords(text) {
  return String(text ?? "").replace(
    /\b(town|towns|house|houses|delivery|deliveries|neighborhood|neighborhoods)\b/gi,
    (word) => {
      const replacement = wordMap[word.toLowerCase()];
      if (!replacement) return word;
      return word[0] === word[0].toUpperCase()
        ? replacement[0].toUpperCase() + replacement.slice(1)
        : replacement;
    },
  );
}

function tableFor(factionId) {
  return installationTables[factionId] || installationTables.vanguard;
}

export function installationName(role, factionId) {
  return (tableFor(factionId)[role] || [`Bastion`, `Bastion`, `THE BASTION`])[0];
}

export function installationShortName(role, factionId) {
  return (tableFor(factionId)[role] || [`Bastion`, `Bastion`, `THE BASTION`])[1];
}

export function installationTagline(role, factionId) {
  return (tableFor(factionId)[role] || [`Bastion`, `Bastion`, `THE BASTION`])[2];
}

// The canvas carries the real identifier a player would type into a bug
// report; the strike frame puts a target marker in front of it.
export function targetLabel(cargo) {
  const text = String(cargo ?? "").replace("issue:", "#").replace("pr:", "PR #");
  return text ? `▸ ${text.slice(0, 12)}` : "▸ strike";
}

function poly(c, points, fill) {
  c.fillStyle = fill;
  c.beginPath();
  points.forEach(([x, y], i) => (i ? c.lineTo(x, y) : c.moveTo(x, y)));
  c.closePath();
  c.fill();
}

function bar(c, x, y, w, h, fill) {
  c.fillStyle = fill;
  c.fillRect(x, y, w, h);
}

function ring(c, x, y, rx, ry, color, width = 2) {
  c.strokeStyle = color;
  c.lineWidth = width;
  c.beginPath();
  c.ellipse(x, y, rx, ry, 0, 0, Math.PI * 2);
  c.stroke();
}

function circle(c, x, y, r, color) {
  c.fillStyle = color;
  c.beginPath();
  c.ellipse(x, y, r, r, 0, 0, Math.PI * 2);
  c.fill();
}

function plate(c, p, x, y) {
  c.save();
  c.translate(x, y);
  poly(c, [[-84, 0], [-60, -26], [60, -26], [84, 0], [60, 26], [-60, 26]], p.dark);
  poly(c, [[-70, 0], [-52, -18], [52, -18], [70, 0], [52, 18], [-52, 18]], p.ground);
  c.strokeStyle = p.accent;
  c.lineWidth = 2;
  c.beginPath();
  c.moveTo(-70, 0);
  c.lineTo(-52, -18);
  c.lineTo(52, -18);
  c.lineTo(70, 0);
  c.stroke();
  // Hazard chevrons mark every base as claimed ground.
  for (let i = 0; i < 5; i++) {
    poly(
      c,
      [
        [-30 + i * 14, 14],
        [-22 + i * 14, 14],
        [-28 + i * 14, 22],
        [-36 + i * 14, 22],
      ],
      i % 2 ? p.hazard : p.mid,
    );
  }
  c.restore();
}

function banner(c, x, y, p, height, motion, now) {
  bar(c, x, y - height, 3, height, p.armor);
  const wave = motion ? Math.sin(now / 260 + x) * 3 : 0;
  poly(
    c,
    [
      [x + 3, y - height],
      [x + 3, y - height + 20],
      [x + 26 + wave, y - height + 10],
      [x + 3, y - height],
    ],
    p.accent,
  );
  poly(
    c,
    [
      [x + 3, y - height],
      [x + 3, y - height + 20],
      [x + 16 + wave / 2, y - height + 12],
    ],
    p.mid,
  );
}

// Every installation is built from the same base plate and faction palette so
// a base reads as one army, while the silhouette says which job it does.
const shapes = {
  hall(c, p, now, motion) {
    bar(c, -46, -22, 92, 24, p.mid);
    bar(c, -26, -112, 52, 92, p.dark);
    bar(c, -26, -112, 52, 7, p.armor);
    for (let i = 0; i < 4; i++)
      bar(c, -16, -100 + i * 21, 32, 8, i % 2 ? p.accent : p.glow);
    circle(c, 0, -132, 21, p.mid);
    circle(c, 0, -134, 15, p.dark);
    circle(c, 0, -136, 9, p.accent);
    bar(c, -2, -186, 4, 40, p.armor);
    circle(c, 0, -190, 4 + (motion ? Math.abs(Math.sin(now / 420)) * 3 : 0), p.glow);
    banner(c, 30, -26, p, 74, motion, now);
    banner(c, -34, -26, p, 60, motion, now + 90);
  },
  repo(c, p, now, motion) {
    bar(c, -30, -30, 60, 32, p.dark);
    poly(c, [[-30, -30], [0, -68], [30, -30]], p.mid);
    bar(c, -5, -150, 10, 86, p.armor);
    for (let i = 0; i < 4; i++) {
      const y = -64 - i * 22;
      bar(c, -34, y, 68, 4, p.armor);
    }
    // The sweep is animation only: it never implies a scan Town did not run.
    const angle = motion ? (now / 1400) % (Math.PI * 2) : -0.4;
    c.save();
    c.translate(0, -150);
    c.rotate(angle);
    poly(c, [[0, 0], [58, -13], [58, 6]], p.accent);
    c.restore();
    ring(c, 0, -150, 22, 8, p.glow, 3);
    circle(c, 0, -150, 7, p.glow);
  },
  bug(c, p, now, motion) {
    poly(c, [[-52, 0], [-38, -40], [38, -40], [52, 0]], p.dark);
    poly(c, [[-38, -40], [-20, -66], [20, -66], [38, -40]], p.mid);
    bar(c, -20, -70, 40, 8, p.armor);
    for (const side of [-1, 1]) {
      c.save();
      c.translate(side * 22, -66);
      c.rotate(side * 0.42);
      bar(c, -4, -62, 8, 62, p.armor);
      bar(c, -4, -62, 4, 62, p.glow);
      c.restore();
      const kick = motion ? Math.sin(now / 90 + side) * 3 : 0;
      circle(c, side * 40, -116 + kick, 6, p.hazard);
    }
    circle(c, 0, -78, 9, p.accent);
  },
  feature(c, p, now, motion) {
    bar(c, -50, -46, 100, 48, p.dark);
    bar(c, -50, -46, 100, 8, p.mid);
    bar(c, 14, -122, 20, 78, p.armor);
    bar(c, 14, -122, 20, 8, p.mid);
    for (let i = 0; i < 3; i++) {
      const drift = motion ? ((now / 900 + i / 3) % 1) : i / 3;
      circle(c, 24 + drift * 8, -132 - drift * 34, 7 + drift * 9, `rgba(233,247,255,${(1 - drift) * 0.35})`);
    }
    bar(c, -40, -34, 34, 30, p.dark);
    bar(c, -36, -30, 26, 22, p.hazard);
    bar(c, -36, -30, 26, 6, p.glow);
    banner(c, -52, -46, p, 58, motion, now);
  },
  issue(c, p, now, motion) {
    bar(c, -56, -44, 58, 46, p.dark);
    bar(c, -56, -44, 58, 7, p.armor);
    bar(c, 6, -36, 50, 38, p.mid);
    bar(c, 6, -36, 50, 6, p.armor);
    for (let i = 0; i < 3; i++) bar(c, -48 + i * 18, -34, 10, 14, p.glow);
    for (let i = 0; i < 2; i++) bar(c, 16 + i * 20, -28, 10, 12, p.glow);
    banner(c, -40, -46, p, 84, motion, now);
    banner(c, 40, -38, p, 66, motion, now + 140);
    poly(c, [[-70, 2], [-58, -12], [-46, 2]], p.hazard);
  },
  review(c, p, now, motion) {
    bar(c, -62, -54, 124, 56, p.dark);
    bar(c, -62, -54, 124, 9, p.armor);
    for (let i = 0; i < 5; i++) bar(c, -56 + i * 24, -66, 14, 14, p.mid);
    for (let i = 0; i < 5; i++) bar(c, -50 + i * 24, -40, 8, 20, p.glow);
    poly(c, [[-34, -54], [0, -96], [34, -54]], p.mid);
    poly(c, [[-22, -54], [0, -82], [22, -54]], p.accent);
    const pulse = motion ? 0.55 + Math.abs(Math.sin(now / 700)) * 0.35 : 0.7;
    c.globalAlpha = pulse;
    bar(c, -3, -76, 6, 22, p.glow);
    c.globalAlpha = 1;
    banner(c, 58, -54, p, 70, motion, now);
  },
  release(c, p, now, motion) {
    bar(c, -40, -20, 80, 22, p.mid);
    bar(c, -26, -122, 52, 102, p.dark);
    bar(c, -26, -122, 52, 8, p.armor);
    bar(c, -14, -112, 28, 84, p.ground);
    for (let i = 0; i < 5; i++)
      poly(
        c,
        [
          [-40 + i * 16, -12],
          [-32 + i * 16, -12],
          [-38 + i * 16, -2],
          [-46 + i * 16, -2],
        ],
        i % 2 ? p.hazard : p.mid,
      );
    bar(c, -52, -140, 6, 60, p.armor);
    bar(c, 46, -140, 6, 60, p.armor);
    bar(c, -52, -140, 104, 6, p.armor);
    const lift = motion ? Math.abs(Math.sin(now / 620)) : 0.4;
    poly(
      c,
      [
        [-12, -126],
        [12, -126],
        [4 + lift * 3, -150],
        [-4 - lift * 3, -150],
      ],
      p.glow,
    );
    circle(c, 0, -126, 6 + lift * 4, `rgba(255,255,255,${0.25 + lift * 0.4})`);
  },
  simplifier(c, p, now, motion) {
    for (let i = 0; i < 3; i++) {
      const x = -54 + i * 38;
      bar(c, x, -92, 30, 92, p.dark);
      bar(c, x, -92, 30, 7, p.armor);
      circle(c, x + 15, -92, 15, p.mid);
      bar(c, x + 4, -70, 22, 8, p.accent);
      bar(c, x + 4, -46, 22, 8, p.accent);
    }
    bar(c, -66, -30, 132, 8, p.armor);
    for (let i = 0; i < 3; i++) bar(c, -34 + i * 38, -128, 6, 36, p.armor);
    bar(c, 42, -150, 10, 66, p.mid);
    const flare = motion ? 0.4 + Math.abs(Math.sin(now / 300)) * 0.6 : 0.6;
    poly(
      c,
      [
        [36, -152],
        [58, -152],
        [47 + (motion ? Math.sin(now / 220) * 4 : 0), -176 - flare * 12],
      ],
      p.hazard,
    );
  },
};

export function paintInstallation(c, { role, x, y, faction, now = 0, motion = true }) {
  const p = paletteFor(faction),
    shape = shapes[role] || shapes.hall;
  plate(c, p, x, y + 52);
  const atlas = atlasFor(faction);
  const index = installationRoles.indexOf(role);
  if (atlas && index >= 0) {
    const cellWidth = atlas.naturalWidth / 4;
    const cellHeight = atlas.naturalHeight / 2;
    const height = role === "repo" ? 182 : 172;
    const width = height * cellWidth / cellHeight;
    c.drawImage(
      atlas,
      (index % 4) * cellWidth,
      Math.floor(index / 4) * cellHeight,
      cellWidth,
      cellHeight,
      x - width / 2,
      y + 69 - height,
      width,
      height,
    );
    return;
  }
  c.save();
  c.translate(x, y + 52);
  // Beams and domes read as one army without hiding what an installation does.
  shape(c, p, now, motion);
  c.restore();
}

// Craft carry deliveries. Ground units hold each installation, including when
// its worker is idle; working status only changes their stance and patrol.
function craft(c, p, { x, y, direction = 1, scale = 1, now = 0, motion = true, faction }) {
  c.save();
  c.translate(x, y);
  c.scale(direction * scale, scale);
  const atlas = craftAtlas();
  if (atlas) {
    const index = Math.max(0, factionIds.indexOf(faction));
    const cell = atlas.naturalWidth / 3;
    c.drawImage(atlas, index * cell, 0, cell, atlas.naturalHeight, -42, -25, 84, 50);
    c.restore();
    return;
  }
  const flick = motion ? Math.sin(now / 70) * 1.4 : 0;
  if (faction === "ascendancy") {
    poly(c, [[0, -9], [30, -3], [36, 6], [-8, 6]], p.armor);
    poly(c, [[0, -9], [18, -1], [4, 5]], p.accent);
    ring(c, 8, 0, 26, 8, p.mid, 2);
    circle(c, -10, 1, 4 + flick * 0.4, p.glow);
  } else if (faction === "hive") {
    circle(c, 0, 0, 12, p.mid);
    poly(c, [[-2, -6], [-34, -20], [-12, 2]], p.armor);
    poly(c, [[-2, 6], [-34, 22], [-12, 0]], p.armor);
    poly(c, [[6, -5], [30, -16], [25, 2]], p.accent);
    poly(c, [[6, 5], [30, 16], [25, -2]], p.accent);
    circle(c, 9, 0, 6, p.glow);
    circle(c, -13, 0, 4 + flick * 0.3, p.hazard);
  } else {
    poly(c, [[-24, -11], [22, -4], [30, 0], [22, 4], [-24, 11]], p.armor);
    poly(c, [[-6, -8], [16, -2], [16, 2], [-6, 8]], p.accent);
    poly(c, [[-24, -11], [-34, -18], [-16, -4]], p.mid);
    poly(c, [[-24, 11], [-34, 18], [-16, 4]], p.mid);
    circle(c, -26, 0, 5 + flick * 0.35, p.glow);
  }
  c.restore();
}

function defender(c, atlas, index, x, y, size, direction, p) {
  c.save();
  c.translate(x, y);
  c.scale(direction, 1);
  oval(c, 2, size * 0.35, size * 0.27, size * 0.09, "#02070aaa");
  if (atlas) {
    const cellWidth = atlas.naturalWidth / 3;
    const cellHeight = atlas.naturalHeight / 2;
    c.drawImage(atlas, (index % 3) * cellWidth, Math.floor(index / 3) * cellHeight,
      cellWidth, cellHeight, -size / 2, -size / 2, size, size);
  } else {
    // A small faction-specific silhouette is available until the atlas loads.
    if (index < 3) {
      circle(c, 0, -size * 0.18, size * 0.12, p.armor);
      poly(c, [[-size * 0.2, -size * 0.08], [size * 0.15, -size * 0.08],
        [size * 0.22, size * 0.31], [-size * 0.12, size * 0.31]], p.mid);
      bar(c, 1, 0, size * 0.35, 4, p.accent);
    } else {
      poly(c, [[-size * 0.38, size * 0.24], [-size * 0.22, -size * 0.12],
        [size * 0.24, -size * 0.12], [size * 0.38, size * 0.24]], p.mid);
      circle(c, 0, 0, size * 0.16, p.accent);
    }
  }
  c.restore();
}

function defend(c, p, faction, x, y, threat, now) {
  if (!threat) return;
  const tx = threat.x, ty = threat.y;
  if (faction === "ascendancy") {
    ring(c, x, y, 58, 17, `${p.accent}99`, 2);
    c.strokeStyle = `${p.glow}b8`;
    c.lineWidth = 3;
    c.beginPath();
    c.moveTo(x + 3, y + 72);
    c.quadraticCurveTo((x + tx) / 2, Math.min(y, ty) - 36, tx, ty);
    c.stroke();
  } else if (faction === "hive") {
    for (let i = -1; i <= 1; i++) {
      const sx = x + i * 23, sy = y + 78;
      c.strokeStyle = `${p.accent}aa`;
      c.lineWidth = 2;
      c.beginPath();
      c.moveTo(sx, sy);
      c.quadraticCurveTo((sx + tx) / 2, Math.min(sy, ty) - 22 - i * 8, tx + i * 8, ty);
      c.stroke();
    }
  } else {
    for (const side of [-1, 1]) {
      const sx = x + side * 83, sy = y + 54;
      c.strokeStyle = now % 260 < 130 ? "#ffe9a6d9" : "#8fd0ff88";
      c.lineWidth = 2;
      c.beginPath();
      c.moveTo(sx, sy);
      c.lineTo(tx, ty);
      c.stroke();
    }
  }
}

export function drawGarrison(c, { role, x, y, faction, now = 0, motion = true, working = false, threat = null }) {
  const p = paletteFor(faction);
  const index = Math.max(0, factionIds.indexOf(faction));
  const atlas = defenderAtlas();
  const pose = workerPose(role, now, motion);
  const patrol = working && motion ? Math.sin(pose.phase * 0.7) * 6 : 0;
  defend(c, p, faction, x, y, motion ? threat : null, now);
  defender(c, atlas, index, x - 83 + patrol, y + 56, 53, 1, p);
  defender(c, atlas, index, x + 83 - patrol, y + 56, 53, -1, p);
  defender(c, atlas, index + 3, x, y + 82, 54, motion && threat?.x < x ? -1 : 1, p);
  if (working) {
    const pulse = motion ? 0.55 + Math.sin(now / 370) * 0.18 : 0.6;
    c.globalAlpha = pulse;
    ring(c, x, y + 80, 25, 8, p.accent, 2);
    c.globalAlpha = 1;
  }
}

// Fire is drawn only for a committed delivery in flight. The destination is
// the actual installation, but projectile reach is bounded so a route never
// paints a beam across the entire map.
function drawVolley(c, { x, y, faction, to, direction, now, progress }) {
  if (progress < 0.48) return;
  const p = paletteFor(faction);
  const destination = positions[to];
  const sx = x + direction * 25, sy = y + 2;
  const dx = destination ? destination[0] - sx : direction * 130;
  const dy = destination ? destination[1] + 58 - sy : 0;
  const distance = Math.hypot(dx, dy) || 1;
  const reach = Math.min(155, distance);
  const tx = sx + dx / distance * reach;
  const ty = sy + dy / distance * reach;
  c.save();
  if (faction === "ascendancy") {
    const pulse = 0.55 + Math.abs(Math.sin(now / 180)) * 0.45;
    c.globalAlpha = pulse;
    c.strokeStyle = p.accent;
    c.lineWidth = 9;
    c.beginPath();
    c.moveTo(sx, sy);
    c.quadraticCurveTo((sx + tx) / 2, (sy + ty) / 2 - 15, tx, ty);
    c.stroke();
    c.strokeStyle = p.glow;
    c.lineWidth = 3;
    c.stroke();
    for (const offset of [-1, 1]) {
      c.strokeStyle = `${p.accent}99`;
      c.lineWidth = 2;
      c.beginPath();
      c.moveTo(sx, sy + offset * 7);
      c.lineTo((sx + tx) / 2, (sy + ty) / 2 + offset * 13);
      c.lineTo(tx, ty);
      c.stroke();
    }
    circle(c, tx, ty, 7 + pulse * 4, p.glow);
  } else if (faction === "hive") {
    for (let i = 0; i < 4; i++) {
      const phase = ((now / 520 + i / 4) % 1);
      const py = sy + (ty - sy) * phase - Math.sin(phase * Math.PI) * (22 + i * 5);
      const px = sx + (tx - sx) * phase;
      c.strokeStyle = `${p.accent}88`;
      c.lineWidth = 2;
      c.beginPath();
      c.moveTo(sx, sy);
      c.quadraticCurveTo((sx + px) / 2, Math.min(sy, py) - 19, px, py);
      c.stroke();
      circle(c, px, py, 3 + i % 2, i % 2 ? p.hazard : p.accent);
    }
    circle(c, sx, sy, 5, p.glow);
  } else {
    circle(c, sx, sy, 7 + (now % 140) / 100, p.hazard);
    for (let i = 0; i < 4; i++) {
      const phase = (now / 290 + i / 4) % 1;
      const bx = sx + (tx - sx) * phase;
      const by = sy + (ty - sy) * phase;
      c.strokeStyle = i % 2 ? "#fff0bc" : p.hazard;
      c.lineWidth = 3;
      c.beginPath();
      c.moveTo(bx - dx / distance * 17, by - dy / distance * 17);
      c.lineTo(bx, by);
      c.stroke();
      circle(c, bx, by, 3, p.glow);
    }
    for (let i = 0; i < 3; i++) {
      const drift = (now / 680 + i / 3) % 1;
      circle(c, sx - direction * drift * 34, sy + drift * 12, 4 + drift * 5,
        `rgba(143,163,172,${(1 - drift) * 0.4})`);
    }
  }
  c.restore();
}

// A strike is one committed delivery drawn as an attack run: a faction craft
// crossing the base, a target marker carrying the real issue or PR number, and
// a faction-specific volley and impact where the work actually arrived.
export function drawStrike(c, { x, y, cargo, faction, to, now, progress = 0, direction = 1, motion = true }) {
  const p = paletteFor(faction),
    lift = motion ? Math.sin(now / 110) * 2.4 : 0,
    bob = y + lift;
  // A halo keeps a craft legible over its own faction's dark ground.
  c.fillStyle = `${p.accent}22`;
  c.beginPath();
  c.ellipse(x, bob, 40, 22, 0, 0, Math.PI * 2);
  c.fill();
  c.fillStyle = "#0b1216a0";
  c.beginPath();
  c.ellipse(x + 4, y + 26, 30, 8, 0, 0, Math.PI * 2);
  c.fill();
  const trail = faction === "hive" ? p.accent : faction === "ascendancy" ? p.glow : p.hazard;
  for (let i = 1; i <= 4; i++)
    circle(c, x - direction * i * 13, bob + Math.sin(now / 120 + i) * 2,
      6 - i, `${trail}${Math.max(0, 68 - i * 12).toString(16).padStart(2, "0")}`);
  craft(c, p, { faction, x, y: bob, direction, scale: 1.2, now, motion });
  drawVolley(c, { x, y: bob, faction, to, direction, now, progress });
  const pulse = 1 + (motion ? Math.sin(now / 160) * 0.08 : 0);
  ring(c, x, bob, 30 * pulse, 30 * pulse, `${p.accent}bb`, 2);
  c.strokeStyle = `${p.glow}55`;
  c.lineWidth = 1;
  c.beginPath();
  c.moveTo(x - 46, bob);
  c.lineTo(x - 24, bob);
  c.moveTo(x + 24, bob);
  c.lineTo(x + 46, bob);
  c.stroke();
  const label = targetLabel(cargo);
  c.font = "11px ui-monospace, monospace";
  c.textAlign = "center";
  c.fillStyle = "#0b1216cc";
  c.fillRect(x - 46, bob - 52, 92, 18);
  c.fillStyle = p.accent;
  c.fillText(label, x, bob - 39);
  // The progress bar makes the run legible even with motion turned off.
  c.fillStyle = "#0b1216aa";
  c.fillRect(x - 30, bob + 30, 60, 4);
  c.fillStyle = p.accent;
  c.fillRect(x - 30, bob + 30, 60 * Math.max(0, Math.min(1, progress)), 4);
}

export function drawImpact(c, { x, y, faction, age = 0 }) {
  const p = paletteFor(faction),
    t = Math.max(0, Math.min(1, age)),
    radius = 16 + t * 74;
  c.save();
  c.globalAlpha = 1 - t;
  if (faction === "ascendancy") {
    ring(c, x, y, radius, radius * 0.65, p.accent, 5 - t * 3);
    ring(c, x, y, radius * 0.58, radius * 0.38, p.glow, 3);
    for (let i = 0; i < 8; i++) {
      const a = i * Math.PI / 4 + t * 0.5;
      poly(c, [[x + Math.cos(a) * radius, y + Math.sin(a) * radius * 0.5],
        [x + Math.cos(a + 0.18) * (radius + 18), y + Math.sin(a + 0.18) * (radius + 18) * 0.5],
        [x + Math.cos(a - 0.18) * (radius + 18), y + Math.sin(a - 0.18) * (radius + 18) * 0.5]], p.glow);
    }
    circle(c, x, y, 20 * (1 - t) + 4, p.glow);
  } else if (faction === "hive") {
    oval(c, x, y + 8, radius, radius * 0.43, `${p.accent}99`);
    for (let i = 0; i < 11; i++) {
      const a = i * Math.PI * 2 / 11;
      const d = radius * (0.5 + (i % 3) * 0.2);
      circle(c, x + Math.cos(a) * d, y + Math.sin(a) * d * 0.55 - t * 20,
        3 + i % 3, i % 2 ? p.hazard : p.glow);
    }
    ring(c, x, y + 7, radius * 0.7, radius * 0.28, p.hazard, 3);
  } else {
    circle(c, x, y, 24 * (1 - t) + 6, "#fff2cb");
    ring(c, x, y, radius, radius * 0.46, p.hazard, 6 - t * 4);
    for (let i = 0; i < 10; i++) {
      const a = i * Math.PI / 5;
      const d = radius * (0.75 + (i % 3) * 0.1);
      bar(c, x + Math.cos(a) * d, y + Math.sin(a) * d * 0.5, 5, 5,
        i % 2 ? p.hazard : p.armor);
    }
    oval(c, x, y + 12, radius * 0.55, radius * 0.22, "#17130ee0");
  }
  c.restore();
}

// The map itself: ashen ground, craters, armored trackways between the
// installations, and a scarred no-man's-land band across the middle. Every
// value comes from the base's own seed, so a base's terrain never moves.
export function frontlineLandscape(seed, faction) {
  const canvas = document.createElement("canvas");
  canvas.width = 1120;
  canvas.height = 680;
  const c = canvas.getContext("2d"),
    p = paletteFor(faction),
    rand = seededRandom(`${seed}:${faction}`),
    ground = c.createLinearGradient(0, 0, 900, 680);
  ground.addColorStop(0, p.dark);
  ground.addColorStop(0.55, "#12161c");
  ground.addColorStop(1, "#0a0e12");
  c.fillStyle = ground;
  c.fillRect(0, 0, 1120, 680);
  for (let i = 0; i < 70; i++) {
    const x = rand() * 1120,
      y = rand() * 680;
    circle(c, x, y, 24 + rand() * 70, i % 2 ? "#1b2430" : "#0e1218");
  }
  for (let i = 0; i < 2400; i++) {
    const x = rand() * 1120,
      y = rand() * 680;
    c.fillStyle = ["#2a3644", "#161d25", "#3b4a58", "#0f141a"][i % 4];
    c.fillRect(x, y, 1 + rand() * 3, 1 + rand() * 2);
  }
  // Craters: a lit rim on the far side and a shadow inside.
  for (let i = 0; i < 22; i++) {
    const x = 40 + rand() * 1040,
      y = 40 + rand() * 600,
      r = 12 + rand() * 46;
    ring(c, x, y, r, r * 0.5, "#46576755", 3);
    ring(c, x, y, r * 0.72, r * 0.36, "#05090e88", 6);
    arcRim(c, x, y, r, p.armor);
  }
  // No-man's-land: a diagonal scarred band that crosses the delivery routes.
  c.save();
  c.globalAlpha = 0.5;
  poly(c, [[0, 300], [1120, 240], [1120, 320], [0, 380]], "#0a0d11");
  c.globalAlpha = 1;
  for (let i = 0; i < 26; i++) {
    const x = rand() * 1120,
      y = 300 + (x / 1120) * -60 + rand() * 70;
    bar(c, x, y, 6 + rand() * 20, 3 + rand() * 5, i % 3 ? "#2b2118" : "#3d4a3a");
  }
  c.restore();
  c.lineCap = "round";
  c.lineJoin = "round";
  // Armored trackways follow the same geometry the deliveries travel.
  for (const [a, b] of roadSegments) {
    c.beginPath();
    c.moveTo(...a);
    c.lineTo(...b);
    c.strokeStyle = "#05090dcc";
    c.lineWidth = 44;
    c.stroke();
    c.strokeStyle = "#33404d";
    c.lineWidth = 34;
    c.stroke();
    c.strokeStyle = `${p.mid}77`;
    c.lineWidth = 26;
    c.stroke();
  }
  c.setLineDash([16, 18]);
  for (const [a, b] of roadSegments) {
    c.beginPath();
    c.moveTo(...a);
    c.lineTo(...b);
    c.strokeStyle = `${p.hazard}66`;
    c.lineWidth = 3;
    c.stroke();
  }
  c.setLineDash([]);
  for (const [role, [x, y]] of Object.entries(positions)) {
    if (role === "outside") continue;
    oval(c, x, y + 70, 118, 34, `${p.dark}bb`);
    oval(c, x - 6, y + 64, 104, 26, `${p.ground}cc`);
  }
  // Barricades, wreckage and spent shells break up the open ground.
  for (const [x, y, rotation] of [
    [168, 262, 0.1],
    [520, 246, -0.2],
    [884, 268, 0.15],
    [236, 596, -0.1],
    [768, 612, 0.2],
    [1046, 372, -0.12],
  ]) {
    c.save();
    c.translate(x, y);
    c.rotate(rotation);
    bar(c, -46, -6, 92, 12, "#2f3a44");
    bar(c, -46, -6, 92, 4, "#4d5b66");
    for (let i = 0; i < 4; i++) poly(c, [[-40 + i * 24, -14], [-24 + i * 24, -14], [-36 + i * 24, 2], [-52 + i * 24, 2]], i % 2 ? p.hazard : "#2f3a44");
    c.restore();
  }
  for (let i = 0; i < 14; i++) {
    const x = rand() * 1120,
      y = rand() * 680;
    poly(c, [[x, y], [x + 22, y - 8], [x + 30, y + 6], [x + 8, y + 14]], "#243039");
    bar(c, x + 4, y + 2, 18, 3, `${p.armor}66`);
  }
  const shade = c.createRadialGradient(560, 320, 180, 560, 360, 690);
  shade.addColorStop(0, "#00000000");
  shade.addColorStop(1, "#00000088");
  c.fillStyle = shade;
  c.fillRect(0, 0, 1120, 680);
  return canvas;
}

function arcRim(c, x, y, r, color) {
  c.strokeStyle = `${color}44`;
  c.lineWidth = 2;
  c.beginPath();
  c.ellipse(x, y - r * 0.18, r, r * 0.5, 0, Math.PI, Math.PI * 2);
  c.stroke();
}

function oval(c, x, y, rx, ry, color) {
  c.fillStyle = color;
  c.beginPath();
  c.ellipse(x, y, rx, ry, 0, 0, Math.PI * 2);
  c.fill();
}
