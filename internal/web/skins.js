import { houseNames } from "./town.js";
import { landscape, drawWorking } from "./scenery.js";
import {
  factions,
  factionIds,
  factionOf,
  factionName,
  rivalFaction,
  sectorLabel,
  installationName,
  installationShortName,
  installationTagline,
  paintInstallation as paintFrontlineInstallation,
  drawGarrison,
  drawStrike as drawFrontlineStrike,
  drawImpact as drawFrontlineImpact,
  frontlineLandscape,
  frontlineWords,
} from "./frontline.js";

// A skin is presentation only. Both skins read the same snapshot, draw the
// same committed events, and hand every command back to town.js untouched.
// Switching one never changes town state, opens a socket, or writes to GitHub.

export const skinIds = ["town", "frontline"];

// Elements in index.html carry data-skin-text="KEY"; a skin that leaves a key
// out would leave that label blank, so the list is shared with the tests.
export const skinTextKeys = [
  "sidebar-eyebrow",
  "all-towns",
  "new-town",
  "clock-eyebrow",
  "clock-note",
  "activity-heading",
  "activity-eyebrow",
  "legend-active",
  "legend-waiting",
  "legend-blocked",
  "legend-quiet",
  "world-aria",
  "faction-label",
  "overview-owner",
  "overview-title",
  "overview-empty-body",
  "unit-repository",
  "unit-repositories",
  "empty-meta",
  "inspector-empty-title",
  "hall-title",
];

const townTaglines = {
  bug: "THE GREENHOUSE",
  simplifier: "THE CLARIFIER",
  feature: "THE STUDY",
  issue: "THE WORKSHOP",
  review: "THE OBSERVATORY",
  release: "THE SHIPPING DEPOT",
  repo: "THE WATCHTOWER",
  hall: "TOWN HALL",
};

export const townText = {
  "sidebar-eyebrow": "YOUR NEIGHBORHOODS",
  "all-towns": "▧ All towns",
  "new-town": "＋ New town",
  "clock-eyebrow": "TOWN CLOCK",
  "clock-note": "Every delivery has a story. Choose a house to follow it.",
  "activity-heading": "Along the way",
  "activity-eyebrow": "LIVE TOWN JOURNAL",
  "legend-active": "Working",
  "legend-waiting": "Waiting",
  "legend-blocked": "Needs attention",
  "legend-quiet": "Quiet hours",
  "world-aria": "Animated deliveries between the agent houses",
  "faction-label": "Sector faction",
  "overview-owner": "YOUR LOCAL WORLD",
  "overview-title": "Every town, together.",
  "overview-empty-body":
    "Add a town using New town above. Each repository gets its own team of agents.",
  "unit-repository": "repository",
  "unit-repositories": "repositories",
  "empty-meta": "Connect a repository to bring its agents together.",
  "inspector-empty-title": "House inspector",
  "hall-title": "Mayoral decisions",
};

export const frontlineText = {
  "sidebar-eyebrow": "YOUR BASES",
  "all-towns": "▧ All bases",
  "new-town": "＋ New base",
  "clock-eyebrow": "CAMPAIGN CLOCK",
  "clock-note": "Every strike has a story. Choose a structure to follow it.",
  "activity-heading": "Along the front",
  "activity-eyebrow": "LIVE FIELD JOURNAL",
  "legend-active": "Engaged",
  "legend-waiting": "Standing by",
  "legend-blocked": "Needs support",
  "legend-quiet": "Stood down",
  "world-aria": "Animated strikes between the bases of your campaign",
  "faction-label": "Sector faction",
  "overview-owner": "YOUR CAMPAIGN",
  "overview-title": "Every base, together.",
  "overview-empty-body":
    "Add a base using New base above. Each repository gets its own garrison.",
  "unit-repository": "base",
  "unit-repositories": "bases",
  "empty-meta": "Connect a repository to raise its first base.",
  "inspector-empty-title": "Structure inspector",
  "hall-title": "Command decisions",
};

export function normalizeSkin(value) {
  return skinIds.includes(value) ? value : "town";
}

// The town skin keeps the wheelbarrow and truck run it always had, including
// the cargo label, dust and shadow.
function townStrike(c, { x, y, from, to, cargo, now, direction }, paintActor) {
  const truck = from === "outside" || to === "outside",
    bob = y + Math.sin(now / 100) * 1.3;
  c.fillStyle = "#192b2270";
  c.beginPath();
  c.ellipse(x + 6, bob + 25, truck ? 38 : 23, 7, 0, 0, Math.PI * 2);
  c.fill();
  for (let i = 0; i < 3; i++) {
    const dust = (now / 500 + i / 3) % 1;
    c.fillStyle = `rgba(219,202,155,${(1 - dust) * 0.3})`;
    c.beginPath();
    c.ellipse(
      x - direction * (20 + dust * 25),
      bob + 22 - dust * 5,
      2 + dust * 5,
      2 + dust * 2,
      0,
      0,
      Math.PI * 2,
    );
    c.fill();
  }
  paintActor(
    truck ? (direction >= 0 ? 3 : 4) : direction >= 0 ? 0 : 1,
    x,
    bob,
    truck ? 57 : 58,
  );
  c.fillStyle = "#0b1a10";
  c.fillRect(x - 34, bob - 48, 68, 19);
  c.fillStyle = "#dbe6cc";
  c.font = "11px monospace";
  c.textAlign = "center";
  c.fillText(
    String(cargo ?? "")
      .replace("issue:", "#")
      .replace("pr:", "PR #")
      .slice(0, 10) || "cargo",
    x,
    bob - 35,
  );
}

export const skins = {
  town: {
    id: "town",
    label: "Brokk Town",
    switchLabel: "Theme: Town",
    switchTitle: "Switch to the Frontline war theme",
    note: "",
    supportsFactions: false,
    travelMs: 6500,
    impactMs: 0,
    faction: () => null,
    factionLabel: () => "",
    strikeFaction: () => null,
    rewrite: (text) => text,
    landscape: (townId) => landscape(townId),
    paintInstallation(c, _house, sprite) {
      sprite();
    },
    paintOccupants(c, { role, x, y, now, motion }, paintWorker) {
      drawWorking(c, role, x, y, now, motion, paintWorker);
    },
    drawStrike: townStrike,
    drawImpact() {},
    houseLabel: (role) => houseNames[role] || "House",
    houseLabelMarkup: (role) =>
      (houseNames[role] || "House").replace(' BOT', '<span class="bot-suffix"> BOT</span>'),
    houseTagline: (role) => townTaglines[role] || "TOWN HALL",
    routeLabel: (event) => `${event.from} → ${event.to}`,
    queueHeading: (count) => `BOT QUEUE · ${count}`,
    meta: (_town, base) => base,
    stats: {
      working: "working",
      queued: "at the doors",
      decisions: "to decide",
      attention: "need attention",
    },
    visit: "Visit town →",
    text: townText,
  },
  frontline: {
    id: "frontline",
    label: "Frontline",
    switchLabel: "Theme: Frontline",
    switchTitle: "Switch back to the Brokk Town neighbourhood",
    note: "Frontline is a look, not a lever: every strike animates a delivery Town already committed, and nothing here writes to GitHub.",
    supportsFactions: true,
    travelMs: 6500,
    impactMs: 900,
    faction: (townId, override) => factionOf(townId, override),
    factionLabel: (faction) => `${factionName(faction)} · ${factions.find((f) => f.id === faction)?.species || ""}`,
    strikeFaction: (event, faction) =>
      event.from === "outside" ? rivalFaction(faction) : faction,
    rewrite: (text) => frontlineWords(text),
    landscape: (townId, faction) => frontlineLandscape(townId, faction),
    paintInstallation(c, { role, x, y, faction, now, motion }) {
      paintFrontlineInstallation(c, { role, x, y, faction, now, motion });
    },
    paintOccupants(c, { role, x, y, faction, now, motion }) {
      drawGarrison(c, { role, x, y, faction, now, motion });
    },
    drawStrike(c, strike) {
      drawFrontlineStrike(c, {
        x: strike.x,
        y: strike.y,
        cargo: strike.cargo,
        faction: strike.faction,
        now: strike.now,
        progress: strike.progress,
        direction: strike.direction,
        motion: strike.motion,
      });
    },
    drawImpact(c, { x, y, faction, age }) {
      drawFrontlineImpact(c, { x, y, faction, age });
    },
    houseLabel: (role, faction) => installationName(role, faction),
    houseLabelMarkup: (role, faction) => installationName(role, faction),
    houseTagline: (role, faction) => installationTagline(role, faction),
    routeLabel: (event, faction) =>
      `⚔ ${installationShortName(event.from, faction)} → ${installationShortName(event.to, faction)}`,
    queueHeading: (count) => `TARGET QUEUE · ${count}`,
    meta: (town, base, faction) => `${sectorLabel(town, faction)} · ${base}`,
    stats: {
      working: "engaged",
      queued: "awaiting orders",
      decisions: "to decide",
      attention: "need support",
    },
    visit: "Visit base →",
    text: frontlineText,
  },
};

export { factionIds, factions };

export function skinFor(value) {
  return skins[normalizeSkin(value)];
}
